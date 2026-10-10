package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/go-analyze/charts"
	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"

	"github.com/maypok86/otter/v2/benchmarks/internal/chart"
)

// mainProcs is the GOMAXPROCS of the bar charts. The other values only
// go to the thread scaling charts.
const mainProcs = 8

// workload identifies the results that are compared on one chart.
type workload struct {
	bench string
	dist  string
	reads int
}

// name is the file name of the workload's chart without the extension.
func (w workload) name() string {
	mix := fmt.Sprintf("reads=%d,writes=%d", w.reads, 100-w.reads)
	// The docs link to the charts of the classic benchmark by these names.
	if w.bench == "Cache" {
		return mix
	}
	return fmt.Sprintf("%s_%s_%s", strings.ToLower(w.bench), w.dist, mix)
}

func (w workload) title() string {
	mix := fmt.Sprintf("reads=%d%%,writes=%d%%", w.reads, 100-w.reads)
	switch w.bench {
	case "Cache":
		return mix
	case "Loading":
		// Every operation is a read that may load.
		return fmt.Sprintf("%s (%s)", w.bench, w.dist)
	}
	return fmt.Sprintf("%s (%s): %s", w.bench, w.dist, mix)
}

type results struct {
	workloads []workload
	caches    map[workload][]string
	procs     map[workload][]int
	// samples holds the ops/s of every run by workload, GOMAXPROCS and cache.
	samples map[workload]map[int]map[string][]float64
}

func (r *results) median(w workload, procs int, cache string) benchmath.Summary {
	sample := benchmath.NewSample(r.samples[w][procs][cache], &benchmath.DefaultThresholds)
	return benchmath.AssumeNothing.Summary(sample, 0.95)
}

func main() {
	path := os.Args[1]
	dir := filepath.Dir(path)

	if err := run(path, dir); err != nil {
		log.Fatal(err)
	}
}

// config returns the value of the key=value part of a benchmark name.
func config(parts [][]byte, key string) string {
	prefix := "/" + key + "="
	for _, part := range parts {
		if v, ok := strings.CutPrefix(string(part), prefix); ok {
			return v
		}
	}
	return ""
}

// gomaxprocs returns the GOMAXPROCS suffix of a benchmark name. The
// testing package leaves it out when it is 1.
func gomaxprocs(parts [][]byte) (int, error) {
	if len(parts) == 0 {
		return 1, nil
	}
	last, ok := strings.CutPrefix(string(parts[len(parts)-1]), "-")
	if !ok {
		return 1, nil
	}
	return strconv.Atoi(last)
}

// parse groups the ops/s of every run, keeping the order in which the
// workloads and the caches first appear.
func parse(path string) (*results, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	res := &results{
		caches:  make(map[workload][]string),
		procs:   make(map[workload][]int),
		samples: make(map[workload]map[int]map[string][]float64),
	}
	r := benchfmt.NewReader(f, path)
	for r.Scan() {
		result, ok := r.Result().(*benchfmt.Result)
		if !ok {
			continue
		}
		opsPerSec, ok := result.Value("ops/s")
		if !ok {
			continue
		}
		base, parts := result.Name.Parts()
		var w workload
		w.bench = strings.TrimPrefix(string(base), "Benchmark")
		w.dist = config(parts, "dist")
		cache := config(parts, "cache")
		if _, err := fmt.Sscanf(config(parts, "reads"), "%d%%", &w.reads); err != nil || cache == "" {
			return nil, fmt.Errorf("unexpected benchmark name %q", result.Name)
		}
		procs, err := gomaxprocs(parts)
		if err != nil {
			return nil, fmt.Errorf("unexpected benchmark name %q: %w", result.Name, err)
		}

		if _, ok := res.samples[w]; !ok {
			res.workloads = append(res.workloads, w)
			res.samples[w] = make(map[int]map[string][]float64)
		}
		if _, ok := res.samples[w][procs]; !ok {
			res.procs[w] = append(res.procs[w], procs)
			res.samples[w][procs] = make(map[string][]float64)
		}
		if !slices.Contains(res.caches[w], cache) {
			res.caches[w] = append(res.caches[w], cache)
		}
		res.samples[w][procs][cache] = append(res.samples[w][procs][cache], opsPerSec)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	if len(res.workloads) == 0 {
		return nil, errors.New("no benchmark results found")
	}
	for _, procs := range res.procs {
		slices.Sort(procs)
	}
	return res, nil
}

func formatOps(v float64) string {
	return charts.FormatValueHumanizeShort(v, 1, false)
}

func run(path, dir string) error {
	res, err := parse(path)
	if err != nil {
		return err
	}

	for _, w := range res.workloads {
		procs := res.procs[w]
		barProcs := procs[len(procs)-1]
		if slices.Contains(procs, mainProcs) {
			barProcs = mainProcs
		}

		values := make([]float64, 0, len(res.caches[w]))
		for _, cache := range res.caches[w] {
			s := res.median(w, barProcs, cache)
			fmt.Printf("%-40s %-11s %12.0f ops/s %s\n", w.title(), cache, s.Center, s.PctRangeString())
			values = append(values, s.Center)
		}
		err := chart.SaveBar(filepath.Join(dir, w.name()+".svg"), chart.Bar{
			Title:  w.title(),
			Unit:   "ops/s",
			Names:  res.caches[w],
			Values: values,
			Format: formatOps,
		})
		if err != nil {
			return err
		}

		if len(procs) < 2 {
			continue
		}
		x := make([]string, 0, len(procs))
		for _, p := range procs {
			x = append(x, strconv.Itoa(p))
		}
		lines := make([][]float64, 0, len(res.caches[w]))
		for _, cache := range res.caches[w] {
			line := make([]float64, 0, len(procs))
			for _, p := range procs {
				line = append(line, res.median(w, p, cache).Center)
			}
			lines = append(lines, line)
		}
		err = chart.SaveLine(filepath.Join(dir, "scaling_"+w.name()+".svg"), chart.Line{
			Title:  "Scaling: " + w.title(),
			XName:  "threads",
			YName:  "ops/s",
			X:      x,
			Names:  res.caches[w],
			Values: lines,
			Format: formatOps,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
