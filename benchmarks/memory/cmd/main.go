package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/go-analyze/charts"

	"github.com/maypok86/otter/v2/benchmarks/internal/chart"
)

type memoryResult struct {
	cacheName string
	// perEntry is the memory the cache needs per entry it keeps, in bytes.
	perEntry float64
}

func main() {
	path := os.Args[1]
	dir := filepath.Dir(path)

	if err := run(path, dir); err != nil {
		log.Fatal(err)
	}
}

func run(path, dir string) error {
	memoryFile, err := os.Open(path)
	if err != nil {
		return err
	}
	defer memoryFile.Close()

	scanner := bufio.NewScanner(memoryFile)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// A line is: name, capacity, overhead "MB", per entry "B/entry",
	// entries "entries".
	var (
		capacities        []int
		names             []string
		capacityToResults = make(map[int][]memoryResult)
	)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 8 {
			return fmt.Errorf("can not parse benchmark output: %q", line)
		}
		cacheName := fields[0]
		capacity, err := strconv.Atoi(fields[1])
		if err != nil {
			return fmt.Errorf("can not parse benchmark output: %w", err)
		}
		perEntry, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			return fmt.Errorf("can not parse benchmark output: %w", err)
		}

		if _, ok := capacityToResults[capacity]; !ok {
			capacities = append(capacities, capacity)
		}
		if !slices.Contains(names, cacheName) {
			names = append(names, cacheName)
		}
		capacityToResults[capacity] = append(capacityToResults[capacity], memoryResult{
			cacheName: cacheName,
			perEntry:  perEntry,
		})
	}
	slices.Sort(capacities)

	formatBytes := func(v float64) string {
		return charts.FormatValueHumanize(v, 0, false) + " B"
	}

	// The chart shows the overhead per entry that the cache kept, because a
	// cache that drops entries would look smaller in total.
	x := make([]string, 0, len(capacities))
	lineValues := make([][]float64, len(names))
	for _, capacity := range capacities {
		x = append(x, strconv.Itoa(capacity))
		results := capacityToResults[capacity]

		barNames := make([]string, 0, len(results))
		values := make([]float64, 0, len(results))
		for _, res := range results {
			barNames = append(barNames, res.cacheName)
			values = append(values, res.perEntry)
		}
		for i, name := range names {
			v := 0.0
			if j := slices.Index(barNames, name); j >= 0 {
				v = values[j]
			}
			lineValues[i] = append(lineValues[i], v)
		}

		outputName := fmt.Sprintf("memory_%d", capacity)
		err := chart.SaveBar(filepath.Join(dir, outputName+".svg"), chart.Bar{
			Title:  fmt.Sprintf("Memory overhead per entry (capacity %d)", capacity),
			Unit:   "bytes",
			Names:  barNames,
			Values: values,
			Format: formatBytes,
		})
		if err != nil {
			return err
		}
	}

	return chart.SaveLine(filepath.Join(dir, "memory.svg"), chart.Line{
		Title:  "Memory overhead per entry",
		XName:  "capacity",
		YName:  "bytes",
		X:      x,
		Names:  names,
		Values: lineValues,
		Format: formatBytes,
	})
}
