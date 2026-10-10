package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-analyze/charts"

	"github.com/maypok86/otter/v2/benchmarks/internal/chart"
)

type memoryResult struct {
	cacheName string
	alloc     float64
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

	capacityToResults := make(map[int][]memoryResult)
	for _, line := range lines {
		fields := strings.Fields(line)
		cacheName := fields[0]
		capacity, err := strconv.Atoi(fields[1])
		if err != nil {
			return fmt.Errorf("can not parse benchmark output: %w", err)
		}
		alloc, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return fmt.Errorf("can not parse benchmark output: %w", err)
		}

		capacityToResults[capacity] = append(capacityToResults[capacity], memoryResult{
			cacheName: cacheName,
			alloc:     alloc,
		})
	}

	for capacity, results := range capacityToResults {
		names := make([]string, 0, len(results))
		values := make([]float64, 0, len(results))
		for _, res := range results {
			names = append(names, res.cacheName)
			values = append(values, res.alloc)
		}

		outputName := fmt.Sprintf("memory_%d", capacity)
		err := chart.SaveBar(filepath.Join(dir, outputName+".svg"), chart.Bar{
			Title:  fmt.Sprintf("Memory consumption (%d)", capacity),
			Unit:   "alloc",
			Names:  names,
			Values: values,
			Format: func(v float64) string {
				return charts.FormatValueHumanize(v, 1, false) + " MB"
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}
