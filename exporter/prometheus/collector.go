// Copyright (c) 2024 Alexey Mayshev. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package prometheus

import (
	"github.com/maypok86/otter"
	"github.com/prometheus/client_golang/prometheus"
)

// StatsProvider provides cache statistics.
type StatsProvider interface {
	Stats() otter.Stats
}

// Collector collects statistics from a cache and exposes them to Prometheus.
type Collector struct {
	provider         StatsProvider
	hitsDesc         *prometheus.Desc
	missesDesc       *prometheus.Desc
	rejectedSetsDesc *prometheus.Desc
	evictedCountDesc *prometheus.Desc
	evictedCostDesc  *prometheus.Desc
}

var _ prometheus.Collector = (*Collector)(nil)

// NewCollector creates a new collector for the given cache statistics provider.
// Metric names are prefixed with the given namespace and subsystem,
// i.e "{namespace}_{subsystem}_{metric}".
// Supported metrics:
// - hits
// - misses
// - rejected_sets
// - evicted_count
// - evicted_cost
func NewCollector(namespace, subsystem string, provider StatsProvider) *Collector {
	return &Collector{
		provider: provider,
		hitsDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "hits"),
			"Number of cache hits.",
			nil, nil,
		),
		missesDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "misses"),
			"Number of cache misses.",
			nil, nil,
		),
		rejectedSetsDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "rejected_sets"),
			"Number of rejected sets.",
			nil, nil,
		),
		evictedCountDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "evicted_count"),
			"Number of evicted entries.",
			nil, nil,
		),
		evictedCostDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "evicted_cost"),
			"Sum of costs of evicted entries.",
			nil, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(descs chan<- *prometheus.Desc) {
	descs <- c.hitsDesc
	descs <- c.missesDesc
	descs <- c.rejectedSetsDesc
	descs <- c.evictedCountDesc
	descs <- c.evictedCostDesc
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(metrics chan<- prometheus.Metric) {
	stats := c.provider.Stats()
	metrics <- prometheus.MustNewConstMetric(
		c.hitsDesc, prometheus.CounterValue, float64(stats.Hits()),
	)
	metrics <- prometheus.MustNewConstMetric(
		c.missesDesc, prometheus.CounterValue, float64(stats.Misses()),
	)
	metrics <- prometheus.MustNewConstMetric(
		c.rejectedSetsDesc, prometheus.CounterValue, float64(stats.RejectedSets()),
	)
	metrics <- prometheus.MustNewConstMetric(
		c.evictedCountDesc, prometheus.CounterValue, float64(stats.EvictedCount()),
	)
	metrics <- prometheus.MustNewConstMetric(
		c.evictedCostDesc, prometheus.CounterValue, float64(stats.EvictedCost()),
	)
}
