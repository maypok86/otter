package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/snapshot-chromedp/render"
)

type cacheInfo struct {
	cacheName string
	opsPerSec int
}

func main() {
	path := os.Args[1]
	dir := filepath.Dir(path)

	if err := run(path, dir); err != nil {
		log.Fatal(err)
	}
}

func run(path, dir string) error {
	perfFile, err := os.Open(path)
	if err != nil {
		return err
	}
	defer perfFile.Close()

	scanner := bufio.NewScanner(perfFile)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	workloadToCaches := make(map[string][]cacheInfo)
	for _, line := range lines[4 : len(lines)-2] {
		fields := strings.Fields(line)
		opsPerSec, err := strconv.Atoi(fields[4])
		if err != nil {
			return errors.New("can not parse benchmark output")
		}

		benchInfo := strings.Split(fields[0], "/")[1]
		benchParts := strings.Split(benchInfo, "_")
		cacheName := benchParts[1]
		workload := strings.Split(benchParts[2], "-")[0]

		workloadToCaches[workload] = append(workloadToCaches[workload], cacheInfo{
			cacheName: cacheName,
			opsPerSec: opsPerSec,
		})
	}

	for workload, caches := range workloadToCaches {
		bar := charts.NewBar()
		bar.SetGlobalOptions(
			charts.WithYAxisOpts(opts.YAxis{
				Name: "ops/s",
			}),
			charts.WithTitleOpts(opts.Title{
				Title: workload,
				Right: "40%",
			}),
			charts.WithLegendOpts(opts.Legend{
				Orient: "vertical",
				Right:  "0%",
				Top:    "10%",
			}),
			// for png render
			charts.WithAnimation(false),
		)

		bar = bar.SetXAxis([]string{"cache"})
		for _, cache := range caches {
			bar = bar.AddSeries(cache.cacheName, []opts.BarData{
				{
					Value: cache.opsPerSec,
				},
			})
		}

		outputName := strings.Join(strings.Split(workload, "%"), "")
		imagePath := filepath.Join(dir, fmt.Sprintf("%s.png", outputName))
		if err := render.MakeChartSnapshot(bar.RenderContent(), imagePath); err != nil {
			return fmt.Errorf("create chart: %w", err)
		}
	}

	return nil
}
