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

package otter

import (
	"time"

	"github.com/maypok86/otter/v2/stats"
)

// StatsCollector accumulates statistics during the operation of a Cache.
//
// If you also want to collect eviction statistics,
// then your collector should implement an EvictionStatsCollector.
//
// If you also want to collect statistics on set rejections,
// then your collector should implement an RejectedSetsStatsCollector.
//
// If you also want to collect load statistics,
// then your collector should implement an LoadingStatsCollector.
type StatsCollector interface {
	// CollectHits collects cache hits. This should be called when a cache request returns a cached value.
	CollectHits(count int)
	// CollectMisses collects cache misses. This should be called when a cache request returns a value that was not
	// found in the cache.
	CollectMisses(count int)
}

// EvictionStatsCollector is a collector that collects statistics on the eviction of entries from the cache.
type EvictionStatsCollector interface {
	// CollectEviction collects the eviction of an entry from the cache. This should only been called when an entry is
	// evicted due to the cache's eviction strategy, and not as a result of manual deletions.
	CollectEviction(weight uint32)
}

// RejectedSetsStatsCollector is a collector that collects statistics on the rejection of sets.
type RejectedSetsStatsCollector interface {
	// CollectRejectedSets collects rejected sets due to too much weight of entries in them.
	CollectRejectedSets(count int)
}

// LoadingStatsCollector is a collector that collects statistics on the loads of new entries.
type LoadingStatsCollector interface {
	// CollectLoadSuccess collects the successful load of a new entry. This method should be called when a cache request
	// causes an entry to be loaded and the loading completes successfully.
	CollectLoadSuccess(loadTime time.Duration)
	// CollectLoadFailure collects the failed load of a new entry. This method should be called when a cache request
	// causes an entry to be loaded, but the loading function returns an error.
	CollectLoadFailure(loadTime time.Duration)
}

type noopStatsCollector struct{}

func (np noopStatsCollector) CollectHits(count int)                     {}
func (np noopStatsCollector) CollectMisses(count int)                   {}
func (np noopStatsCollector) CollectEviction(weight uint32)             {}
func (np noopStatsCollector) CollectRejectedSets(count int)             {}
func (np noopStatsCollector) CollectLoadFailure(loadTime time.Duration) {}
func (np noopStatsCollector) CollectLoadSuccess(loadTime time.Duration) {}

type statsCollector interface {
	StatsCollector
	EvictionStatsCollector
	RejectedSetsStatsCollector
	LoadingStatsCollector
}

type statsCollectorComposition struct {
	StatsCollector
	EvictionStatsCollector
	RejectedSetsStatsCollector
	LoadingStatsCollector
}

func newStatsCollector(collector StatsCollector) statsCollector {
	// optimization
	if noop, ok := collector.(noopStatsCollector); ok {
		return noop
	}
	if c, ok := collector.(*stats.Counter); ok {
		return c
	}

	sc := &statsCollectorComposition{
		StatsCollector:             collector,
		EvictionStatsCollector:     noopStatsCollector{},
		RejectedSetsStatsCollector: noopStatsCollector{},
		LoadingStatsCollector:      noopStatsCollector{},
	}

	if ec, ok := collector.(EvictionStatsCollector); ok {
		sc.EvictionStatsCollector = ec
	}
	if rsc, ok := collector.(RejectedSetsStatsCollector); ok {
		sc.RejectedSetsStatsCollector = rsc
	}
	if lc, ok := collector.(LoadingStatsCollector); ok {
		sc.LoadingStatsCollector = lc
	}

	return sc
}
