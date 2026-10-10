// Package chart renders the benchmark results to SVG images.
//
// It draws the charts in pure Go, so no browser is needed. SVG stays
// sharp at any zoom and on high-density screens.
package chart

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/go-analyze/charts"
)

const (
	width  = 900
	height = 480
)

// Bar is a chart with one bar per series.
type Bar struct {
	Title string
	// Unit is the name of the value axis.
	Unit   string
	Names  []string
	Values []float64
	// Format renders the values on the axis and above the bars.
	Format func(float64) string
}

// Line is a chart with one line per series over the same x values.
type Line struct {
	Title string
	XName string
	YName string
	X     []string
	Names []string
	// Values holds one slice per series, aligned with X.
	Values [][]float64
	// Format renders the values on the y axis.
	Format func(float64) string
}

// colors keeps the color of a cache the same on every chart, even when
// some caches are missing from it.
var colors = map[string]string{
	"otter":      "#5470c6",
	"theine":     "#91cc75",
	"ristretto":  "#fac858",
	"sturdyc":    "#ee6666",
	"ccache":     "#73c0de",
	"gcache":     "#3ba272",
	"ttlcache":   "#fc8452",
	"golang-lru": "#9a60b4",
	"lru":        "#9a60b4",
	"arc":        "#ea7ccc",
	"s3-fifo":    "#2f4554",
	"clock-pro":  "#61a0a8",
}

// fallbackColors are used for the series without a fixed color.
var fallbackColors = []string{"#d48265", "#749f83", "#ca8622", "#bda29a", "#6e7074", "#546570"}

func theme(names []string) charts.ColorPalette {
	series := make([]charts.Color, 0, len(names))
	fallback := 0
	for _, name := range names {
		c, ok := colors[name]
		if !ok {
			c = fallbackColors[fallback%len(fallbackColors)]
			fallback++
		}
		series = append(series, charts.ParseColor(c))
	}
	return charts.GetDefaultTheme().WithSeriesColors(series)
}

func newPainter() *charts.Painter {
	return charts.NewPainter(charts.PainterOptions{
		OutputFormat: charts.ChartOutputSVG,
		Width:        width,
		Height:       height,
	})
}

func legend(names []string) charts.LegendOption {
	return charts.LegendOption{
		SeriesNames: names,
		Offset:      charts.OffsetStr{Left: charts.PositionCenter, Top: charts.PositionBottom},
		Symbol:      charts.SymbolSquare,
	}
}

func title(text string) charts.TitleOption {
	return charts.TitleOption{
		Text:   text,
		Offset: charts.OffsetCenter,
		FontStyle: charts.FontStyle{
			FontSize: 16,
		},
	}
}

// SaveBar renders the bar chart to the SVG file at path.
func SaveBar(path string, b Bar) error {
	series := make(charts.BarSeriesList, 0, len(b.Names))
	for i, name := range b.Names {
		series = append(series, charts.BarSeries{
			Name:   name,
			Values: []float64{b.Values[i]},
			Label: charts.SeriesLabel{
				Show:           charts.Ptr(true),
				ValueFormatter: b.Format,
			},
		})
	}

	axisMax, labels := niceAxis(b.Values)
	p := newPainter()
	err := p.BarChart(charts.BarChartOption{
		Theme:      theme(b.Names),
		Padding:    charts.NewBox(20, 20, 20, 10),
		Title:      title(b.Title),
		Legend:     legend(b.Names),
		SeriesList: series,
		CategoryAxis: charts.CategoryAxisOption{
			Labels: []string{""},
		},
		ValueAxis: []charts.ValueAxisOption{{
			Title:          b.Unit,
			Min:            charts.Ptr(0.0),
			Max:            charts.Ptr(axisMax),
			LabelCount:     labels,
			ValueFormatter: b.Format,
		}},
	})
	if err != nil {
		return fmt.Errorf("render %s: %w", b.Title, err)
	}
	return save(p, path)
}

// SaveLine renders the line chart to the SVG file at path.
func SaveLine(path string, l Line) error {
	series := make(charts.LineSeriesList, 0, len(l.Names))
	for i, name := range l.Names {
		series = append(series, charts.LineSeries{
			Name:   name,
			Values: l.Values[i],
		})
	}

	p := newPainter()
	err := p.LineChart(charts.LineChartOption{
		Theme:      theme(l.Names),
		Padding:    charts.NewBox(20, 20, 20, 10),
		Title:      title(l.Title),
		Legend:     legend(l.Names),
		SeriesList: series,
		XAxis: charts.XAxisOption{
			Title:  l.XName,
			Labels: l.X,
		},
		YAxis: []charts.YAxisOption{{
			Title:          l.YName,
			Min:            charts.Ptr(0.0),
			ValueFormatter: l.Format,
		}},
	})
	if err != nil {
		return fmt.Errorf("render %s: %w", l.Title, err)
	}
	return save(p, path)
}

// niceAxis returns the maximum of a value axis that starts at zero and the
// number of its labels, so that the steps are round (1, 2, 2.5 or 5 times
// a power of ten) and the highest bar leaves room for its label.
func niceAxis(values []float64) (axisMax float64, labels int) {
	highest := 0.0
	for _, v := range values {
		highest = max(highest, v)
	}
	if highest <= 0 {
		return 1, 2
	}

	const steps = 5
	raw := highest * 1.1 / steps
	magnitude := math.Pow(10, math.Floor(math.Log10(raw)))
	step := 10 * magnitude
	for _, m := range []float64{1, 2, 2.5, 5} {
		if m*magnitude >= raw {
			step = m * magnitude
			break
		}
	}
	n := math.Ceil(highest * 1.1 / step)
	return n * step, int(n) + 1
}

func save(p *charts.Painter, path string) error {
	buf, err := p.Bytes()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}
