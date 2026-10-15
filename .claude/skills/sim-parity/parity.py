#!/usr/bin/env python3
"""Hit-rate parity of otter with the reference cache (Caffeine) on identical key streams.

Subcommands (run from the repository root; everything is written under .local/parity/):

  setup  [--ref master]           clone the reference at a ref and build its simulator
  traces [--large]                rewrite real traces to LIRS and generate the climber traps
  build  [--ref WORKTREE|<git ref>]  build otter's simulator with the reference's sketch hash
  run    [--only REGEX] [--reps N] [--jobs N] [--otter NAME]
  report [RUN_DIR]                print the parity table of a run (default: the latest)

The otter build differs from otter in one way: the frequency sketch hashes a key as the
reference does (Long.hashCode, then its 32-bit spread and rehash). otter's own hash is random
per cache, which makes small traces differ by points from run to run; with the reference's hash
the two caches take the same decisions, and only the random admission of a warm candidate
(1 in 128) remains as noise on either side.
"""

import argparse
import concurrent.futures as futures
import csv
import gzip
import json
import math
import os
import re
import shutil
import signal
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], text=True,
                           capture_output=True, check=True).stdout.strip())
SKILL = Path(__file__).resolve().parent
WORK = ROOT / ".local" / "parity"
REF_DIR = WORK / "caffeine"
TRACES = WORK / "traces"
BIN = WORK / "bin"
RUNS = WORK / "runs"
OTTER_TRACES = ROOT / "benchmarks" / "simulator" / "trace"

REF_REPO = "https://github.com/ben-manes/caffeine.git"
PARSER = "com/github/benmanes/caffeine/cache/simulator/parser"

# Unweighted traces bundled with the reference's simulator: (name, format, path in its parser
# resources). Weighted ones are left out: the reference sizes them by weight, otter's adapter
# does not.
BUNDLED = [(n, "LIRS", f"lirs/{n}.trace.gz") for n in
           ["2_pools", "backf", "cpp", "cs", "gli", "loop", "multi1", "multi2", "multi3",
            "ps", "scan", "sprite", "zigzag"]]
BUNDLED += [(n, "ADDRESS", f"address/{n}.trace.gz") for n in ["gcc", "gzip", "mcf", "swim", "twolf"]]
BUNDLED += [(n, "CACHE2K", f"cache2k/{n}.trace.xz") for n in ["orm-busy", "orm-night", "web07", "web12"]]
BUNDLED += [("w106", "CLOUD_PHYSICS", "cloud_physics/w106.trace.xz"),
            ("corda", "CORDA", "corda/trace_vaultservice.gz"),
            ("scarab-recs", "SCARAB", "scarab/scarab-recs.trace.xz")]
BUNDLED_LARGE = [("corda-large", "CORDA", "corda/trace_vaultservice_large.gz"),
                 ("scarab-recs-large", "SCARAB", "scarab/recs.trace.20160808T073231Z.xz")]
# otter's ARC traces, at the capacities of its sim-compare configs.
ARC = ["oltp", "p3", "p8", "s3", "ds1"]
ARC_LARGE = {"s3", "ds1"}

# The climber's adversarial traps (generators from the reference's climber-gate and
# audit-regret skills), with the cache sizes its gate runs them at. Each entry is the generator
# command (relative to traps/; OUT is the output directory) and the produced files.
TRAPS = [
    ("gen.py mixture --max 8192 --dfrac 0.10 --out OUT/mixture_8192_d010.lirs", None),
    ("gen.py mixture --max 8192 --dfrac 0.25 --out OUT/mixture_8192_d025.lirs", None),
    ("gen.py mixture --max 32768 --dfrac 0.25 --out OUT/mixture_32768_d025.lirs", None),
    ("gen.py mixture --max 8192 --dfrac 0.50 --out OUT/mixture_8192_d050.lirs", None),
    ("gen.py mixture --max 8192 --dfrac 0.50 --lengthmult 576 --out OUT/mixture_8192_d050_long.lirs", None),
    ("gen.py mixture --max 8192 --dfrac 0.10 --lengthmult 640 --out OUT/mixture_8192_d010_long.lirs", None),
    ("gen.py mixture --max 8192 --dfrac 0.25 --lengthmult 640 --out OUT/mixture_8192_d025_long.lirs", None),
    ("gen.py mixture --max 32768 --dfrac 0.25 --lengthmult 640 --out OUT/mixture_32768_d025_long.lirs", None),
    ("gen.py phases --max 8192 --dfrac 0.50 --out OUT/phases_8192_d050.lirs", None),
    ("gen.py phases --max 32768 --dfrac 0.50 --out OUT/phases_32768_d050.lirs", None),
    ("gen.py deadphase --max 8192 --out OUT/deadphase_8192.lirs", None),
    ("gen.py widepin --max 8192 --out OUT/widepin_8192.lirs", None),
    ("gen2.py straywall2 --max 8192 --dfrac 0.50 --out OUT/straywall2_8192_d050.lirs", None),
    ("gen2.py straywall2 --max 16384 --dfrac 0.50 --out OUT/straywall2_16384_d050.lirs", None),
    ("demoflood.py --max 8192 --out OUT/demoflood_8192.lirs", None),
    ("gen_attacks.py OUT", ["trickle_8192*.lirs", "lowmix_16384*.lirs"]),
    ("blind_gen.py OUT", ["blind_balloonflip.lirs", "blind_nullchurn.lirs"]),
    ("blind_gen.py OUT v2", ["blind_bandtrap2.lirs"]),
    ("@cd OUT whisper.py attack", ["attack_whisper.lirs"]),
    ("@cd OUT whisper.py quarter", ["attack_whisper_quarter.lirs"]),
    ("whisper_mod.py --amp 0.08 --period 12 --out OUT/whisper_a0.08_p12.lirs", None),
    ("whisper_mod.py --amp 0.08 --period 6 --out OUT/whisper_a0.08_p6.lirs", None),
    ("whisper_mod.py --amp 0.12 --period 12 --out OUT/whisper_a0.12_p12.lirs", None),
    ("gen_adv.py mixmod --max 32768 --dfrac 0.25 --lengthmult 400 --amp 0.00 --out OUT/mixmod_32768_a000.lirs", None),
    ("gen_adv.py mixmod --max 32768 --dfrac 0.25 --lengthmult 400 --amp 0.10 --period 6 --out OUT/mixmod_32768_a010.lirs", None),
    ("gen_adv.py metronome --max 8192 --ratio 1 --out OUT/metronome_8192_r1.lirs", None),
    ("gen_adv.py regimeramp --max 8192 --out OUT/regimeramp_8192.lirs", None),
    ("escgen.py jam --max 8192 --out OUT/esc_jam.lirs", None),
    ("a1_tenant.py --max 8192 --share 0.10 --dist 400 --out OUT/tenant_s10.lirs", None),
    ("gen_jam.py --mode flat --out OUT/posjam_flat_8192.lirs", None),
    ("gen_jam.py --mode jam --jitter 0.5 --out OUT/jam_j50.lirs", None),
    ("gen_jam.py --mode jam --period-req 81920 --out OUT/jam_d25.lirs", None),
    ("gen_jam.py --mode jam --out OUT/jam_d0.lirs", None),
    ("gen_adv.py shieldtrap --max 8192 --out OUT/shieldtrap_8192.lirs", None),
    ("gen_adv.py loopcliff --max 8192 --out OUT/loopcliff_8192.lirs", None),
    ("gen_adv.py climbtrend --max 8192 --out OUT/climbtrend_8192.lirs", None),
    ("gen_adv.py climbtrend --max 8192 --direction down --out OUT/climbtrend_dn_8192.lirs", None),
    ("gen_adv.py climbtrend --max 8192 --hot-lo 0.55 --hot-hi 0.55 --modamp 0.12 --modperiod 40 --out OUT/saw_p40.lirs", None),
    ("crashnoise.py --shape flat --amp 0.00 --out OUT/cn_flat.lirs", None),
    ("crashnoise.py --shape sine --amp 0.12 --period 12 --out OUT/cn_sine_a12.lirs", None),
    ("mixnoise.py --max 8192 --shape flat --out OUT/mn8_flat.lirs", None),
    ("mixnoise.py --max 8192 --shape sine --amp 0.10 --out OUT/mn8_sine_a10.lirs", None),
    ("gen_h4c1.py --phase 4 --order high-low --samples 260 --out OUT/h4c1_p4_attack_long.lirs", None),
    ("gen_h4c1.py --phase 4 --order low-high --samples 260 --out OUT/h4c1_p4_control_long.lirs", None),
    ("gen_moat.py --hotn 3000 --bandd 4000 --out OUT/moat_h3000_b4000.lirs", None),
    ("gen_moat.py --hotn 4000 --bandd 4000 --out OUT/moat_h4000_b4000.lirs", None),
    ("gen_moat.py --hotn 5000 --bandd 4000 --out OUT/moat_h5000_b4000.lirs", None),
    ("slowswap.py --ramp 1 --p3 139 --out OUT/slowswap_r1.lirs", None),
    ("slowswap.py --ramp 20 --p3 120 --out OUT/slowswap_r20.lirs", None),
    ("gen_blindlock.py --max 8192 --whisper 0 --out OUT/blindlock_blind.lirs", None),
    ("gen_blindlock.py --max 8192 --whisper 0.004 --out OUT/blindlock_sighted.lirs", None),
    ("gen_straddle.py --max 4096 --period 8 --out OUT/strad_p8.lirs", None),
    ("gen_straddle.py --max 4096 --period 8 --lo 0.325 --hi 0.325 --out OUT/strad_stat.lirs", None),
    ("gen_norank.py repeat --refs 6 --width 4096 --out OUT/rep_r6_w4096.lirs", None),
    ("gen_norank.py flood --jrate 1.0 --prologue --out OUT/flood_j100.lirs", None),
] + [(f"workload.py specs/{s}.json --out OUT/{s}_8192.lirs", None) for s in
     ["shallowmoat", "scarburst", "absolve", "absolve_p8", "ghostclaim", "crestpast", "parkveil",
      "parkveil_min", "latebloom", "latebloom_min", "mainsat", "sidecliff", "jumpslide", "lowbar",
      "ramp_retest", "deadpark", "needle", "needle_wide"]] + [
    ("workload.py specs/ramp_retest.json --max 16384 --out OUT/ramp_retest_16384.lirs", None),
]
TRAPS_LARGE = [
    ("gen_moat.py --hotn 7800 --bandd 4000 --n 12000000 --out OUT/moat_h7800_long.lirs", None),
    ("workload.py specs/hazefloor.json --out OUT/hazefloor_8192.lirs", None),
]
# Traps the gate runs at more than the size in their name.
TRAP_SIZES = {"attack_whisper": [8192, 16384], "strad_p8": [4096, 4097], "strad_stat": [4096, 4097]}

RESULT = re.compile(r"Simulation for cache (\S+) at capacity (\d+) completed with hit ratio ([\d.]+)%")
DONE = "All simulations are complete"


def sh(cmd, **kw):
    kw.setdefault("check", True)
    return subprocess.run(cmd, text=True, **kw)


def die(msg):
    print(f"parity: {msg}", file=sys.stderr)
    sys.exit(1)


# --------------------------------------------------------------------------------------- setup

def reference_java():
    """Returns the java that runs the reference's simulator: its classes target the Gradle
    daemon's toolchain version (gradle/gradle-daemon-jvm.properties), which Gradle provisions
    into GRADLE_USER_HOME/jdks; JAVA_HOME is the fallback."""
    if os.environ.get("PARITY_JAVA"):
        return os.environ["PARITY_JAVA"]
    props = REF_DIR / "gradle" / "gradle-daemon-jvm.properties"
    version = None
    if props.exists():
        m = re.search(r"toolchainVersion=(\d+)", props.read_text())
        version = m and m.group(1)
    gradle_home = Path(os.environ.get("GRADLE_USER_HOME", Path.home() / ".gradle"))
    if version:
        for java in sorted(gradle_home.glob(f"jdks/*-{version}-*/**/bin/java")):
            return str(java)
    if os.environ.get("JAVA_HOME"):
        return str(Path(os.environ["JAVA_HOME"]) / "bin" / "java")
    die("no java for the reference simulator: set PARITY_JAVA or JAVA_HOME")


def cmd_setup(args):
    WORK.mkdir(parents=True, exist_ok=True)
    if not (REF_DIR / ".git").exists():
        REF_DIR.mkdir(parents=True, exist_ok=True)
        sh(["git", "init", "-q"], cwd=REF_DIR)
        sh(["git", "remote", "add", "origin", REF_REPO], cwd=REF_DIR)
    sh(["git", "fetch", "-q", "--depth", "1", "origin", args.ref], cwd=REF_DIR)
    sh(["git", "checkout", "-q", "FETCH_HEAD"], cwd=REF_DIR)
    commit = sh(["git", "log", "-1", "--format=%H %cs %s"], cwd=REF_DIR, capture_output=True).stdout.strip()
    print(f"reference at {commit}")
    if not os.environ.get("JAVA_HOME"):
        die("set JAVA_HOME to a JDK the reference's build accepts (25 or newer)")
    print("building the reference simulator (the first build downloads ~1 GB of dependencies)")
    sh(["./gradlew", "--console=plain", "-q", ":simulator:installDist"], cwd=REF_DIR)
    print(f"java for the simulator: {reference_java()}")


def ref_simulator_lib():
    lib = REF_DIR / "simulator" / "build" / "install" / "simulator" / "lib"
    if not lib.exists():
        die("the reference simulator is not built: run `parity.py setup` first")
    return lib


# -------------------------------------------------------------------------------------- traces

def rewrite(name, fmt, src, out):
    """Rewrites a trace to LIRS with the reference's Rewriter (which only reads files, not the
    resources inside its jar)."""
    plain = out.with_suffix("")
    sh([reference_java(), "-Xmx8g", "-cp", f"{ref_simulator_lib()}/*",
        "com.github.benmanes.caffeine.cache.simulator.parser.Rewriter",
        f"--inputFiles={src}", f"--inputFormat={fmt}", f"--outputFile={plain}", "--outputFormat=LIRS"],
       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    sh(["gzip", "-f", str(plain)])


def distinct_keys(path):
    opener = gzip.open if str(path).endswith(".gz") else open
    with opener(path, "rt") as f:
        return len({line for line in f if line.strip()})


def fraction_capacities(distinct):
    caps = set()
    for f in (0.05, 0.1, 0.2, 0.35, 0.5, 0.75):
        x = distinct * f
        p = 10 ** max(0, len(str(int(x))) - 2)
        caps.add(max(10, int(round(x / p) * p)))
    return sorted(caps)


def config_capacities(name):
    text = (ROOT / "benchmarks" / "simulator" / "configs" / f"{name}.toml").read_text()
    m = re.search(r"capacities\s*=\s*\[([^\]]*)\]", text)
    return [int(c.replace("_", "")) for c in m.group(1).split(",") if c.strip()]


def run_trap(entry, out):
    cmd, _ = entry
    cwd = SKILL / "traps"
    if cmd.startswith("@cd OUT "):
        cmd, cwd = cmd[len("@cd OUT "):], out
    argv = cmd.replace("OUT", str(out)).split()
    script = SKILL / "traps" / argv[0]
    env = dict(os.environ, PYTHONPATH=str(SKILL / "traps"))
    sh([sys.executable, str(script)] + argv[1:], cwd=cwd, env=env, stdout=subprocess.DEVNULL)


def trap_capacities(path):
    stem = path.name.split(".")[0]
    for prefix, caps in TRAP_SIZES.items():
        if stem.startswith(prefix):
            return caps
    m = re.search(r"_(4096|8192|16384|32768)(?:_|$)", stem)
    return [int(m.group(1))] if m else [8192]


def cmd_traces(args):
    real, traps = TRACES / "real", TRACES / "traps"
    real.mkdir(parents=True, exist_ok=True)
    traps.mkdir(parents=True, exist_ok=True)
    manifest = []

    resources = REF_DIR / "simulator" / "src" / "main" / "resources" / PARSER
    for name, fmt, rel in BUNDLED + (BUNDLED_LARGE if args.large else []):
        out = real / f"{name}.lirs.gz"
        if not out.exists():
            print(f"rewriting {name}")
            rewrite(name, fmt, resources / rel, out)
        manifest.append((name, out, fraction_capacities(distinct_keys(out))))
    for name in ARC:
        if name in ARC_LARGE and not args.large:
            continue
        out = real / f"arc-{name}.lirs.gz"
        if not out.exists():
            print(f"rewriting {name}")
            rewrite(name, "ARC", OTTER_TRACES / "arc" / f"{name}.gz", out)
        manifest.append((f"arc-{name}", out, config_capacities(name)))

    for entry in TRAPS + (TRAPS_LARGE if args.large else []):
        cmd, produced = entry
        names = produced or [cmd.split("OUT/")[-1]]
        if all(list(traps.glob(n)) or list(traps.glob(n + ".gz")) for n in names):
            continue
        print(f"generating {', '.join(names)}")
        run_trap(entry, traps)
    for plain in sorted(traps.glob("*.lirs")):
        sh(["gzip", "-f", str(plain)])
    for path in sorted(traps.glob("*.lirs.gz")):
        manifest.append((path.name[:-len(".lirs.gz")], path, trap_capacities(path)))

    with open(TRACES / "manifest.tsv", "w") as f:
        for name, path, caps in manifest:
            f.write(f"{name}\t{path}\t{','.join(map(str, caps))}\n")
    print(f"{len(manifest)} traces in {TRACES / 'manifest.tsv'}")


# --------------------------------------------------------------------------------------- build

HASH_FROM = """func (s *sketch[K]) hash(k K) uint64 {
	return spread(s.hasher.Hash(k))
}"""
HASH_TO = """func (s *sketch[K]) hash(k K) uint64 {
	v := any(k).(uint64)
	h := uint32(v ^ (v >> 32))
	h ^= h >> 17
	h *= 0xed5ad4bb
	h ^= h >> 11
	h *= 0xac4c1b51
	h ^= h >> 15
	return uint64(h)
}"""
REHASH_FROM = """func rehash(h uint64) uint64 {
	h *= 0x31848bab
	h ^= h >> 14
	return h
}"""
REHASH_TO = """func rehash(h64 uint64) uint64 {
	h := uint32(h64)
	h *= 0x31848bab
	h ^= h >> 14
	return uint64(h)
}"""


def cmd_build(args):
    name = re.sub(r"[^A-Za-z0-9._-]", "_", args.ref)
    tree = WORK / f"otter-{name}"
    if tree.exists():
        shutil.rmtree(tree)
    tree.mkdir(parents=True)
    if args.ref == "WORKTREE":
        sh(["rsync", "-a", "--exclude", "/.git", "--exclude", "/.local",
            "--exclude", "/benchmarks/simulator/trace", "--exclude", "/benchmarks/simulator/results",
            f"{ROOT}/", f"{tree}/"])
    else:
        archive = subprocess.Popen(["git", "archive", args.ref], cwd=ROOT, stdout=subprocess.PIPE)
        sh(["tar", "-x", "-C", str(tree)], stdin=archive.stdout)
        archive.wait()

    sketch = tree / "sketch.go"
    src = sketch.read_text()
    if HASH_FROM not in src or REHASH_FROM not in src:
        die("sketch.go no longer has the hash and rehash this build replaces: update HASH_FROM/REHASH_FROM")
    sketch.write_text(src.replace(HASH_FROM, HASH_TO).replace(REHASH_FROM, REHASH_TO))

    BIN.mkdir(parents=True, exist_ok=True)
    binary = BIN / f"otter-{name}"
    bench = tree / "benchmarks"
    # benchmarks/go.sum is not always complete; this only touches the scratch copy
    sh(["go", "mod", "tidy"], cwd=bench, capture_output=True)
    sh(["go", "build", "-o", str(binary), "./simulator/cmd"], cwd=bench)
    print(f"built {binary}")


# ----------------------------------------------------------------------------------------- run

def run_reference(path, cap):
    conf = tempfile.NamedTemporaryFile("w", suffix=".conf", delete=False)
    conf.write(f"""include "reference"
caffeine.simulator {{
  maximum-size = {cap}
  policies = [ product.Caffeine ]
  admission = [ Always ]
  files {{ paths = [ "lirs:{path}" ] }}
  report {{ format = csv }}
}}
""")
    conf.close()
    java = reference_java()
    env = dict(os.environ, JAVA_HOME=str(Path(java).parent.parent),
               JAVA_OPTS=f"-Dconfig.file={conf.name} -Xmx6g")
    out = sh([str(REF_DIR / "simulator/build/install/simulator/bin/simulator")], env=env,
             capture_output=True, check=False)
    os.unlink(conf.name)
    for line in out.stdout.splitlines():
        cols = [c.strip('"') for c in line.split(",")]
        if cols and cols[0] == "product.Caffeine":
            return float(cols[1].rstrip("%"))
    raise RuntimeError(f"reference: no result for {path}@{cap}\n{out.stdout[-1500:]}\n{out.stderr[-1500:]}")


def run_otter(binary, name, path, caps):
    with tempfile.NamedTemporaryFile("w", suffix=".toml", delete=False) as conf:
        conf.write(f'type = "file"\nname = "{name}"\ncapacities = [{", ".join(map(str, caps))}]\n'
                   f'caches = ["otter"]\n\n[file]\npaths = [ {{ trace_type = "lirs", path = "{path}" }} ]\n')
    # The simulator renders a chart through headless Chrome after its results and can hang
    # there, so it is stopped once it reports completion.
    proc = subprocess.Popen([str(binary), "-config", conf.name], cwd=ROOT / "benchmarks" / "simulator",
                            text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            start_new_session=True)
    results = {}
    try:
        for line in proc.stdout:
            m = RESULT.search(line)
            if m:
                results[int(m.group(2))] = float(m.group(3))
            if DONE in line:
                break
    finally:
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        proc.wait(timeout=60)
        os.unlink(conf.name)
    if set(results) != set(caps):
        raise RuntimeError(f"otter: missing results for {name}: got {sorted(results)}")
    return results


def cmd_run(args):
    manifest = TRACES / "manifest.tsv"
    if not manifest.exists():
        die("no traces: run `parity.py traces` first")
    binary = BIN / f"otter-{args.otter}"
    if not binary.exists():
        die(f"no {binary}: run `parity.py build --ref {args.otter}` first")
    rows = []
    for line in manifest.read_text().splitlines():
        name, path, caps = line.split("\t")
        if args.only and not re.search(args.only, name):
            continue
        rows.append((name, path, [int(c) for c in caps.split(",")]))

    run_dir = RUNS / time.strftime("%Y%m%d-%H%M%S")
    run_dir.mkdir(parents=True)
    commit = sh(["git", "log", "-1", "--format=%H %cs"], cwd=REF_DIR, capture_output=True).stdout.strip()
    (run_dir / "meta.json").write_text(json.dumps({"reference": commit, "otter": args.otter,
                                                   "reps": args.reps}, indent=2))
    out = open(run_dir / "results.csv", "w", newline="")
    writer = csv.writer(out)
    writer.writerow(["trace", "capacity", "cache", "rep", "hit_rate"])

    jobs = [("reference", name, path, cap, rep) for rep in range(args.reps)
            for name, path, caps in rows for cap in caps]
    jobs += [("otter", name, path, caps, rep) for rep in range(args.otter_reps) for name, path, caps in rows]
    total, done = len(jobs), 0
    with futures.ThreadPoolExecutor(args.jobs) as pool:
        pending = {}
        for job in jobs:
            kind, name, path, cap, rep = job
            if kind == "reference":
                pending[pool.submit(run_reference, path, cap)] = job
            else:
                pending[pool.submit(run_otter, binary, name, path, cap)] = job
        for fut in futures.as_completed(pending):
            kind, name, path, cap, rep = pending[fut]
            result = fut.result()
            if kind == "reference":
                writer.writerow([name, cap, "reference", rep, result])
            else:
                for c, hit in sorted(result.items()):
                    writer.writerow([name, c, "otter", rep, hit])
            out.flush()
            done += 1
            print(f"[{done}/{total}] {kind} {name}", flush=True)
    out.close()
    report(run_dir)


# -------------------------------------------------------------------------------------- report

def report(run_dir):
    cells = {}
    with open(run_dir / "results.csv") as f:
        for row in csv.DictReader(f):
            cells.setdefault((row["trace"], int(row["capacity"])), {}).setdefault(row["cache"], []).append(
                float(row["hit_rate"]))
    lines = [f"{'trace':28}{'capacity':>10} {'reference':>14} {'otter':>14} {'delta':>7}  verdict"]
    counts = {"exact": 0, "noise": 0, "CHECK": 0, "MISMATCH": 0}
    deltas = []
    for (trace, cap), by in sorted(cells.items()):
        ref, ott = by.get("reference", []), by.get("otter", [])
        if not ref or not ott:
            continue
        rm, om = statistics.mean(ref), statistics.mean(ott)
        rsd = statistics.stdev(ref) if len(ref) > 1 else 0.0
        osd = statistics.stdev(ott) if len(ott) > 1 else 0.0
        delta = om - rm
        deltas.append(abs(delta))
        # The simulators print two decimals. The only nondeterminism left is the random
        # admission of a warm candidate, which shows as a spread across repetitions; a difference
        # beyond three of those spreads is a real departure. A cell with too few repetitions to
        # tell is CHECK: rerun it with more.
        if abs(delta) < 0.005:
            verdict = "exact"
        elif min(len(ref), len(ott)) < 3:
            verdict = "noise" if abs(delta) <= 0.05 else "CHECK"
        else:
            verdict = "noise" if abs(delta) <= max(3 * max(rsd, osd), 0.05) else "MISMATCH"
        counts[verdict] += 1
        lines.append(f"{trace:28}{cap:>10} {rm:8.2f}±{rsd:4.2f} {om:8.2f}±{osd:4.2f} {delta:+7.2f}  {verdict}")
    meta = json.loads((run_dir / "meta.json").read_text())
    summary = (f"reference {meta['reference']}, otter {meta['otter']}, {meta['reps']} reference reps; "
               f"cells: {counts['exact']} exact, {counts['noise']} within admission noise, "
               f"{counts['CHECK']} CHECK, {counts['MISMATCH']} MISMATCH; mean |delta| {statistics.mean(deltas):.3f}, "
               f"max {max(deltas):.2f}" if deltas else "no cells")
    text = "\n".join(lines + ["", summary]) + "\n"
    (run_dir / "summary.txt").write_text(text)
    print(text)


def cmd_report(args):
    run_dir = Path(args.run_dir) if args.run_dir else max(RUNS.iterdir(), key=lambda p: p.name)
    report(run_dir)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("setup")
    p.add_argument("--ref", default="master")
    p = sub.add_parser("traces")
    p.add_argument("--large", action="store_true", help="also the large real traces and traps")
    p = sub.add_parser("build")
    p.add_argument("--ref", default="WORKTREE", help="WORKTREE (uncommitted tree) or a git ref")
    p = sub.add_parser("run")
    p.add_argument("--only", help="regex over trace names")
    p.add_argument("--reps", type=int, default=3, help="reference repetitions per cell")
    p.add_argument("--otter-reps", type=int, default=3, help="otter repetitions per cell")
    p.add_argument("--jobs", type=int, default=max(1, (os.cpu_count() or 2) // 2))
    p.add_argument("--otter", default="WORKTREE", help="the otter build to run (its --ref)")
    p = sub.add_parser("report")
    p.add_argument("run_dir", nargs="?")
    args = ap.parse_args()
    {"setup": cmd_setup, "traces": cmd_traces, "build": cmd_build, "run": cmd_run,
     "report": cmd_report}[args.cmd](args)


if __name__ == "__main__":
    main()
