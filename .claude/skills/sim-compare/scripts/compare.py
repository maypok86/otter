#!/usr/bin/env python3
"""Compare simulator hit rates of otter across git revisions, with repetitions.

otter randomizes its hashing per cache, so one simulator run on a small trace varies by up to
±3 points; differences are only meaningful over repeated runs. This script builds the
simulator (benchmarks/simulator) once per revision, runs every config N times, interleaving the
revisions, and summarizes mean ± standard deviation per cell with the difference to the first
revision.

Each revision is checked out as a detached git worktree under .local/sim/trees/ (gitignored);
WORKTREE means the current working tree, uncommitted changes included. The simulator renders a
chart through headless Chrome after printing its results and can hang there, so each run is
stopped as soon as it reports that all simulations are complete.

Usage (from the repository root):
  python3 .claude/skills/sim-compare/scripts/compare.py \
      --refs main,WORKTREE --reps 5 --caches otter \
      benchmarks/simulator/configs/oltp.toml benchmarks/simulator/configs/p8.toml
"""

import argparse
import csv
import datetime
import math
import os
import re
import signal
import subprocess
import sys
import tomllib
from collections import defaultdict
from pathlib import Path

RESULT = re.compile(r"Simulation for cache (\S+) at capacity (\d+) completed with hit ratio ([\d.]+)%")
DONE = "All simulations are complete"


def sh(cmd, cwd, check=True):
    return subprocess.run(cmd, cwd=cwd, check=check, text=True, capture_output=True)


def tree_name(ref):
    return re.sub(r"[^A-Za-z0-9._-]", "_", ref)


def prepare_tree(root, ref, sim_dir):
    """Returns the directory of a checkout of ref and builds the simulator binary in it."""
    tree = sim_dir / "trees" / tree_name(ref)
    if ref == "WORKTREE":
        tree.mkdir(parents=True, exist_ok=True)
        sh(["rsync", "-a", "--delete", "--exclude", ".git", "--exclude", ".local",
            f"{root}/", f"{tree}/"], cwd=root)
    else:
        commit = sh(["git", "rev-parse", "--verify", f"{ref}^{{commit}}"], cwd=root).stdout.strip()
        if tree.exists():
            head = sh(["git", "rev-parse", "HEAD"], cwd=tree, check=False).stdout.strip()
            if head != commit:
                sh(["git", "worktree", "remove", "--force", str(tree)], cwd=root)
        if not tree.exists():
            sh(["git", "worktree", "add", "--detach", str(tree), commit], cwd=root)
    bench = tree / "benchmarks"
    # benchmarks/go.sum is not always complete; this only touches the scratch checkout
    sh(["go", "mod", "tidy"], cwd=bench)
    binary = sim_dir / "bin" / tree_name(ref) / "simulator"
    binary.parent.mkdir(parents=True, exist_ok=True)
    sh(["go", "build", "-o", str(binary), "./simulator/cmd"], cwd=bench)
    return tree, binary


def write_config(src, caches, out):
    with open(src, "rb") as f:
        cfg = tomllib.load(f)
    if caches:
        cfg["caches"] = caches
    lines = [f'type = "{cfg["type"]}"', f'name = "{cfg["name"]}"',
             "capacities = [" + ", ".join(str(c) for c in cfg["capacities"]) + "]",
             "caches = [" + ", ".join(f'"{c}"' for c in cfg["caches"]) + "]"]
    if "limit" in cfg:
        lines.append(f"limit = {cfg['limit']}")
    if "zipf" in cfg:
        z = cfg["zipf"]
        lines += ["", "[zipf]", f"s = {z['s']}", f"v = {z['v']}", f"imax = {z['imax']}"]
    if "file" in cfg:
        paths = ", ".join(f'{{ trace_type = "{p["trace_type"]}", path = "{p["path"]}" }}'
                          for p in cfg["file"]["paths"])
        lines += ["", "[file]", f"paths = [ {paths} ]"]
    out.write_text("\n".join(lines) + "\n")
    return cfg["name"]


def run_once(binary, config, cwd, timeout):
    proc = subprocess.Popen([str(binary), "-config", str(config)], cwd=cwd, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            start_new_session=True)
    results = []
    try:
        for line in proc.stdout:
            m = RESULT.search(line)
            if m:
                results.append((m.group(1), int(m.group(2)), float(m.group(3))))
            if DONE in line:
                break
    finally:
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        proc.wait(timeout=timeout)
    return results


def stats(xs):
    n = len(xs)
    mean = sum(xs) / n
    sd = math.sqrt(sum((x - mean) ** 2 for x in xs) / (n - 1)) if n > 1 else 0.0
    return mean, sd


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("configs", nargs="+", help="simulator TOML configs")
    ap.add_argument("--refs", default="HEAD,WORKTREE", help="comma-separated revisions; WORKTREE = current tree")
    ap.add_argument("--reps", type=int, default=5)
    ap.add_argument("--caches", default="otter", help="comma-separated caches; empty keeps the config's list")
    ap.add_argument("--timeout", type=int, default=60, help="seconds to wait for a stopped run to exit")
    args = ap.parse_args()

    root = Path(sh(["git", "rev-parse", "--show-toplevel"], cwd=os.getcwd()).stdout.strip())
    sim_dir = root / ".local" / "sim"
    run_dir = sim_dir / "runs" / datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    run_dir.mkdir(parents=True)
    refs = [r.strip() for r in args.refs.split(",") if r.strip()]
    caches = [c.strip() for c in args.caches.split(",") if c.strip()]

    trees = {}
    for ref in refs:
        print(f"preparing {ref}", file=sys.stderr)
        trees[ref] = prepare_tree(root, ref, sim_dir)

    rows = []
    for src in args.configs:
        src = Path(src).resolve()
        config = run_dir / src.name
        try:
            name = write_config(src, caches, config)
        except tomllib.TOMLDecodeError as e:
            print(f"skipping {src}: {e}", file=sys.stderr)
            continue
        for rep in range(args.reps):
            for ref in refs:
                tree, binary = trees[ref]
                print(f"{name} rep {rep + 1}/{args.reps} {ref}", file=sys.stderr)
                for cache, capacity, hit in run_once(binary, config, tree / "benchmarks" / "simulator", args.timeout):
                    rows.append({"ref": ref, "trace": name, "cache": cache, "capacity": capacity,
                                 "rep": rep, "hit_rate": hit})

    with open(run_dir / "results.csv", "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=["ref", "trace", "cache", "capacity", "rep", "hit_rate"])
        w.writeheader()
        w.writerows(rows)

    cells = defaultdict(lambda: defaultdict(list))
    for r in rows:
        cells[(r["trace"], r["cache"], r["capacity"])][r["ref"]].append(r["hit_rate"])
    base = refs[0]
    header = ["trace", "cache", "capacity"] + [f"{r} (mean±sd)" for r in refs] + [f"{r} − {base}" for r in refs[1:]]
    out = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    for (trace, cache, capacity), by_ref in sorted(cells.items()):
        s = {r: stats(by_ref[r]) for r in refs if by_ref.get(r)}
        if base not in s:
            continue
        line = [trace, cache, str(capacity)] + [f"{s[r][0]:.2f}±{s[r][1]:.2f}" if r in s else "—" for r in refs]
        for r in refs[1:]:
            if r not in s:
                line.append("—")
                continue
            d = s[r][0] - s[base][0]
            n = min(len(by_ref[r]), len(by_ref[base]))
            se = math.sqrt((s[r][1] ** 2 + s[base][1] ** 2) / n) if n else 0.0
            # marked when the difference exceeds twice its standard error and 0.1 points; with
            # fewer than 3 runs per side the standard deviation means nothing, so nothing is marked
            mark = " *" if n >= 3 and abs(d) > 2 * se and abs(d) >= 0.1 else ""
            line.append(f"{d:+.2f}{mark}")
        out.append("| " + " | ".join(line) + " |")
    summary = "\n".join(out) + f"\n\n`*`: |difference| > 2·SE and ≥ 0.1 points, only with ≥ 3 reps; {args.reps} reps; refs {', '.join(refs)}\n"
    (run_dir / "summary.md").write_text(summary)
    print(summary)
    print(f"results: {run_dir}", file=sys.stderr)


if __name__ == "__main__":
    main()
