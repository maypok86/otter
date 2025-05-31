package chart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/snapshot-chromedp/render"

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
	imagePath := filepath.Join(dir, fmt.Sprintf("%s.png", strings.ToLower(c.name)))

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithXAxisOpts(opts.XAxis{
			Name: "capacity",
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Name: "hit ratio",
			AxisLabel: &opts.AxisLabel{
				Formatter: "{value}%",
			},
		}),
		charts.WithTitleOpts(opts.Title{
			Title: c.name,
			Right: "50%",
		}),
		charts.WithLegendOpts(opts.Legend{
			Orient: "vertical",
			Right:  "0%",
			Top:    "10%",
		}),
		// for png render
		charts.WithAnimation(false),
	)

	capacities := make([]int, 0, len(c.table[0]))
	for _, r := range c.table[0] {
		capacities = append(capacities, r.Capacity())
	}

	line = line.SetXAxis(capacities)
	for _, results := range c.table {
		lineData := make([]opts.LineData, 0, len(results))
		for _, res := range results {
			lineData = append(lineData, opts.LineData{
				Value: res.Ratio(),
			})
		}
		line = line.AddSeries(results[0].Name(), lineData)
	}

	line.SetSeriesOptions(charts.WithLineChartOpts(
		opts.LineChart{
			Smooth: opts.Bool(true),
		}),
	)

	if err := render.MakeChartSnapshot(line.RenderContent(), imagePath); err != nil {
		return fmt.Errorf("save chart: %w", err)
	}
	return nil
}
