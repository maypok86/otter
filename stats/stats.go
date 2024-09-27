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

package stats

import (
	"math"
	"time"
)

// Stats are statistics about the performance of a otter.Cache.
type Stats struct {
	hits           uint64
	misses         uint64
	evictions      uint64
	evictionWeight uint64
	loadSuccesses  uint64
	loadFailures   uint64
	totalLoadTime  time.Duration
}

// Hits returns the number of times otter.Cache lookup methods returned a cached value.
func (s Stats) Hits() uint64 {
	return s.hits
}

// Misses returns the number of times otter.Cache lookup methods did not find a cached value.
func (s Stats) Misses() uint64 {
	return s.misses
}

// Requests returns the number of times otter.Cache lookup methods were looking for a cached value.
//
// NOTE: the values of the metrics are undefined in case of overflow. If you require specific handling, we recommend
// implementing your own otter.StatsRecorder.
func (s Stats) Requests() uint64 {
	return checkedAdd(s.hits, s.misses)
}

// HitRatio returns the ratio of cache requests which were hits.
//
// NOTE: hitRatio + missRatio =~ 1.0.
func (s Stats) HitRatio() float64 {
	requests := s.Requests()
	if requests == 0 {
		return 1.0
	}
	return float64(s.hits) / float64(requests)
}

// MissRatio returns the ratio of cache requests which were misses.
//
// NOTE: hitRatio + missRatio =~ 1.0.
func (s Stats) MissRatio() float64 {
	requests := s.Requests()
	if requests == 0 {
		return 0.0
	}
	return float64(s.misses) / float64(requests)
}

// Evictions returns the number of times an entry has been evicted. This count does not include manual
// otter.Cache deletions.
func (s Stats) Evictions() uint64 {
	return s.evictions
}

// EvictionWeight returns the sum of weights of evicted entries. This total does not include manual
// otter.Cache deletions.
func (s Stats) EvictionWeight() uint64 {
	return s.evictionWeight
}

// LoadSuccesses returns the number of times otter.Cache lookup methods have successfully loaded a new value.
func (s Stats) LoadSuccesses() uint64 {
	return s.loadSuccesses
}

// LoadFailures returns the number of times otter.Cache lookup methods failed to load a new value, either
// because no value was found or an error was returned while loading.
func (s Stats) LoadFailures() uint64 {
	return s.loadFailures
}

// Loads returns the total number of times that otter.Cache lookup methods attempted to load new values.
//
// NOTE: the values of the metrics are undefined in case of overflow. If you require specific handling, we recommend
// implementing your own otter.StatsRecorder.
func (s Stats) Loads() uint64 {
	return checkedAdd(s.loadSuccesses, s.loadFailures)
}

// TotalLoadTime returns the time the cache has spent loading new values.
func (s Stats) TotalLoadTime() time.Duration {
	return s.totalLoadTime
}

// LoadFailureRatio returns the ratio of cache loading attempts which returned errors.
func (s Stats) LoadFailureRatio() float64 {
	loads := s.Loads()
	if loads == 0 {
		return 0.0
	}
	return float64(s.loadFailures) / float64(loads)
}

// AverageLoadPenalty returns the average time spent loading new values.
func (s Stats) AverageLoadPenalty() time.Duration {
	loads := s.Loads()
	if loads == 0 {
		return 0
	}
	if loads > uint64(math.MaxInt64) {
		return s.totalLoadTime / time.Duration(math.MaxInt64)
	}
	return s.totalLoadTime / time.Duration(loads)
}

func checkedAdd(a, b uint64) uint64 {
	s := a + b
	if s < a || s < b {
		return math.MaxUint64
	}
	return s
}
