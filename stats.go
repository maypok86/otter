// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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

// StatsRecorder accumulates statistics during the operation of a Cache.
//
// If you also want to record eviction statistics,
// then your recorder should implement an EvictionStatsRecorder.
//
// If you also want to record load statistics,
// then your recorder should implement an LoadStatsRecorder.
type StatsRecorder interface {
	// RecordHits records cache hits. This should be called when a cache request returns a cached value.
	RecordHits(count int)
	// RecordMisses records cache misses. This should be called when a cache request returns a value that was not
	// found in the cache.
	RecordMisses(count int)
}

// EvictionStatsRecorder is a recorder that records statistics on the eviction of entries from the cache.
type EvictionStatsRecorder interface {
	// RecordEviction records the eviction of an entry from the cache. This should only been called when an entry is
	// evicted due to the cache's eviction strategy, and not as a result of manual deletions.
	RecordEviction(weight uint32)
}

// LoadStatsRecorder is a recorder that records statistics on the loads of new entries.
type LoadStatsRecorder interface {
	// RecordLoadSuccess records the successful load of a new entry. This method should be called when a cache request
	// causes an entry to be loaded and the loading completes successfully.
	RecordLoadSuccess(loadTime time.Duration)
	// RecordLoadFailure records the failed load of a new entry. This method should be called when a cache request
	// causes an entry to be loaded, but the loading function returns an error.
	RecordLoadFailure(loadTime time.Duration)
}

type noopStatsRecorder struct{}

func (np noopStatsRecorder) RecordHits(count int)                     {}
func (np noopStatsRecorder) RecordMisses(count int)                   {}
func (np noopStatsRecorder) RecordEviction(weight uint32)             {}
func (np noopStatsRecorder) RecordLoadFailure(loadTime time.Duration) {}
func (np noopStatsRecorder) RecordLoadSuccess(loadTime time.Duration) {}

type statsRecorder interface {
	StatsRecorder
	EvictionStatsRecorder
	LoadStatsRecorder
}

type statsRecorderHub struct {
	StatsRecorder
	EvictionStatsRecorder
	LoadStatsRecorder
}

func newStatsRecorder(recorder StatsRecorder) statsRecorder {
	// optimization
	if noop, ok := recorder.(noopStatsRecorder); ok {
		return noop
	}
	if c, ok := recorder.(*stats.Counter); ok {
		return c
	}

	sc := &statsRecorderHub{
		StatsRecorder:         recorder,
		EvictionStatsRecorder: noopStatsRecorder{},
		LoadStatsRecorder:     noopStatsRecorder{},
	}

	if ec, ok := recorder.(EvictionStatsRecorder); ok {
		sc.EvictionStatsRecorder = ec
	}
	if lc, ok := recorder.(LoadStatsRecorder); ok {
		sc.LoadStatsRecorder = lc
	}

	return sc
}
