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

// Stats are statistics about the performance of an otter.Cache.
type Stats struct {
	// Hits is the number of times otter.Cache lookup methods returned a cached value.
	Hits uint64
	// Misses is the number of times otter.Cache lookup methods did not find a cached value.
	Misses uint64
	// Evictions is the number of times an entry has been evicted. This count does not include manual
	// otter.Cache deletions.
	Evictions uint64
	// EvictionWeight is the sum of weights of evicted entries. This total does not include manual
	// otter.Cache deletions.
	EvictionWeight uint64
	// LoadSuccesses is the number of times otter.Cache lookup methods have successfully loaded a new value.
	LoadSuccesses uint64
	// LoadFailures is the number of times otter.Cache lookup methods failed to load a new value, either
	// because no value was found or an error was returned while loading.
	LoadFailures uint64
	// TotalLoadTime returns the time the cache has spent loading new values.
	TotalLoadTime time.Duration
}

// Requests returns the number of times otter.Cache lookup methods were looking for a cached value.
//
// NOTE: the values of the metrics are undefined in case of overflow. If you require specific handling, we recommend
// implementing your own [Recorder].
func (s Stats) Requests() uint64 {
	return saturatedAdd(s.Hits, s.Misses)
}

// HitRatio returns the ratio of cache requests which were hits.
//
// NOTE: hitRatio + missRatio =~ 1.0.
func (s Stats) HitRatio() float64 {
	requests := s.Requests()
	if requests == 0 {
		return 1.0
	}
	return float64(s.Hits) / float64(requests)
}

// MissRatio returns the ratio of cache requests which were misses.
//
// NOTE: hitRatio + missRatio =~ 1.0.
func (s Stats) MissRatio() float64 {
	requests := s.Requests()
	if requests == 0 {
		return 0.0
	}
	return float64(s.Misses) / float64(requests)
}

// Loads returns the total number of times that otter.Cache lookup methods attempted to load new values.
//
// NOTE: the values of the metrics are undefined in case of overflow. If you require specific handling, we recommend
// implementing your own [Recorder].
func (s Stats) Loads() uint64 {
	return saturatedAdd(s.LoadSuccesses, s.LoadFailures)
}

// LoadFailureRatio returns the ratio of cache loading attempts which returned errors.
func (s Stats) LoadFailureRatio() float64 {
	loads := s.Loads()
	if loads == 0 {
		return 0.0
	}
	return float64(s.LoadFailures) / float64(loads)
}

// AverageLoadPenalty returns the average time spent loading new values.
func (s Stats) AverageLoadPenalty() time.Duration {
	loads := s.Loads()
	if loads == 0 {
		return 0
	}
	if loads > uint64(math.MaxInt64) {
		return s.TotalLoadTime / time.Duration(math.MaxInt64)
	}
	return s.TotalLoadTime / time.Duration(loads)
}

func saturatedAdd(a, b uint64) uint64 {
	s := a + b
	if s < a || s < b {
		return math.MaxUint64
	}
	return s
}
