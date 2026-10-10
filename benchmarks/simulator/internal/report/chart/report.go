package chart

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pngchart "github.com/maypok86/otter/v2/benchmarks/internal/chart"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report/simulation"
)

type Chart struct {
	name  string
	table [][]simulation.Result
}

func NewChart(name string, table [][]simulation.Result) *Chart {
	return &Chart{
		name:  name,
		table: table,
	}
}

func (c *Chart) Report() error {
	if c == nil {
		return nil
	}

	dir := "results"
	imagePath := filepath.Join(dir, fmt.Sprintf("%s.svg", strings.ToLower(c.name)))

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	capacities := make([]string, 0, len(c.table[0]))
	for _, r := range c.table[0] {
		capacities = append(capacities, strconv.Itoa(r.Capacity()))
	}

	names := make([]string, 0, len(c.table))
	values := make([][]float64, 0, len(c.table))
	for _, results := range c.table {
		ratios := make([]float64, 0, len(results))
		for _, res := range results {
			ratios = append(ratios, res.Ratio())
		}
		names = append(names, results[0].Name())
		values = append(values, ratios)
	}

	return pngchart.SaveLine(imagePath, pngchart.Line{
		Title:  c.name,
		XName:  "capacity",
		YName:  "hit ratio",
		X:      capacities,
		Names:  names,
		Values: values,
		Format: func(v float64) string {
			return strconv.FormatFloat(v, 'f', -1, 64) + "%"
		},
	})
}
