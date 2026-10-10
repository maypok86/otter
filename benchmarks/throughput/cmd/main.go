package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-analyze/charts"
	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"

	"github.com/maypok86/otter/v2/benchmarks/internal/chart"
)

type cacheInfo struct {
	cacheName string
	opsPerSec benchmath.Summary
}

type workload struct {
	readPercentage string
	caches         []cacheInfo
}

func main() {
	path := os.Args[1]
	dir := filepath.Dir(path)

	if err := run(path, dir); err != nil {
		log.Fatal(err)
	}
}

// config returns the value of the key=value part of a benchmark name.
func config(name benchfmt.Name, key string) string {
	_, parts := name.Parts()
	prefix := "/" + key + "="
	for _, part := range parts {
		if v, ok := strings.CutPrefix(string(part), prefix); ok {
			return v
		}
	}
	return ""
}

// parse groups the ops/s of every run by workload and cache, keeping
// the order in which they first appear.
func parse(path string) ([]*workload, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		workloads []*workload
		byReads   = make(map[string]*workload)
		samples   = make(map[string]map[string][]float64)
	)
	r := benchfmt.NewReader(f, path)
	for r.Scan() {
		res, ok := r.Result().(*benchfmt.Result)
		if !ok {
			continue
		}
		opsPerSec, ok := res.Value("ops/s")
		if !ok {
			continue
		}
		reads := config(res.Name, "reads")
		cacheName := config(res.Name, "cache")
		if reads == "" || cacheName == "" {
			return nil, fmt.Errorf("unexpected benchmark name %q", res.Name)
		}

		w, ok := byReads[reads]
		if !ok {
			w = &workload{readPercentage: reads}
			byReads[reads] = w
			workloads = append(workloads, w)
			samples[reads] = make(map[string][]float64)
		}
		if _, ok := samples[reads][cacheName]; !ok {
			w.caches = append(w.caches, cacheInfo{cacheName: cacheName})
		}
		samples[reads][cacheName] = append(samples[reads][cacheName], opsPerSec)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	if len(workloads) == 0 {
		return nil, errors.New("no benchmark results found")
	}

	for _, w := range workloads {
		for i := range w.caches {
			c := &w.caches[i]
			sample := benchmath.NewSample(samples[w.readPercentage][c.cacheName], &benchmath.DefaultThresholds)
			c.opsPerSec = benchmath.AssumeNothing.Summary(sample, 0.95)
		}
	}
	return workloads, nil
}

func run(path, dir string) error {
	workloads, err := parse(path)
	if err != nil {
		return err
	}

	for _, w := range workloads {
		var reads int
		if _, err := fmt.Sscanf(w.readPercentage, "%d%%", &reads); err != nil {
			return fmt.Errorf("parse read percentage %q: %w", w.readPercentage, err)
		}
		title := fmt.Sprintf("reads=%d%%,writes=%d%%", reads, 100-reads)

		names := make([]string, 0, len(w.caches))
		values := make([]float64, 0, len(w.caches))
		for _, cache := range w.caches {
			fmt.Printf("%-22s %-11s %12.0f ops/s %s\n",
				title, cache.cacheName, cache.opsPerSec.Center, cache.opsPerSec.PctRangeString())
			names = append(names, cache.cacheName)
			values = append(values, cache.opsPerSec.Center)
		}

		// The docs link to these file names.
		outputName := strings.ReplaceAll(title, "%", "")
		err := chart.SaveBar(filepath.Join(dir, outputName+".svg"), chart.Bar{
			Title:  title,
			Unit:   "ops/s",
			Names:  names,
			Values: values,
			Format: func(v float64) string {
				return charts.FormatValueHumanizeShort(v, 1, false)
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}
