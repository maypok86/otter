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
	"testing"
	"time"
)

func testStats(
	t *testing.T,
	s Stats,
	hits uint64,
	misses uint64,
	requests uint64,
	hitRatio float64,
	missRatio float64,
	evictions uint64,
	evictionWeight uint64,
	loadSuccesses uint64,
	loadFailures uint64,
	totalLoadTime time.Duration,
	loads uint64,
	loadFailureRatio float64,
	averageLoadPenalty time.Duration,
) {
	t.Helper()

	if s.Hits != hits {
		t.Fatalf("hits should be %d, but got %d", hits, s.Hits)
	}
	if s.Misses != misses {
		t.Fatalf("misses should be %d, but got %d", misses, s.Misses)
	}
	if s.Requests() != requests {
		t.Fatalf("requests should be %d, but got %d", requests, s.Requests())
	}
	if s.HitRatio() != hitRatio {
		t.Fatalf("hitRatio should be %.2f, but got %.2f", hitRatio, s.HitRatio())
	}
	if s.MissRatio() != missRatio {
		t.Fatalf("missRatio should be %.2f, but got %.2f", missRatio, s.MissRatio())
	}
	if s.Evictions != evictions {
		t.Fatalf("evictions should be %d, but got %d", evictions, s.Evictions)
	}
	if s.EvictionWeight != evictionWeight {
		t.Fatalf("evictionWeight should be %d, but got %d", evictionWeight, s.EvictionWeight)
	}
	if s.LoadSuccesses != loadSuccesses {
		t.Fatalf("loadSuccesses should be %d, but got %d", loadSuccesses, s.LoadSuccesses)
	}
	if s.LoadFailures != loadFailures {
		t.Fatalf("loadFailures should be %d, but got %d", loadFailures, s.LoadFailures)
	}
	if s.TotalLoadTime != totalLoadTime {
		t.Fatalf("totalLoadTime should be %d, but got %d", totalLoadTime, s.TotalLoadTime)
	}
	if s.Loads() != loads {
		t.Fatalf("loads should be %d, but got %d", loads, s.Loads())
	}
	if s.LoadFailureRatio() != loadFailureRatio {
		t.Fatalf("loadFailureRatio should be %.2f, but got %.2f", loadFailureRatio, s.LoadFailureRatio())
	}
	if s.AverageLoadPenalty() != averageLoadPenalty {
		t.Fatalf("averageLoadPenalty should be %d, but got %d", averageLoadPenalty, s.AverageLoadPenalty())
	}
}

func TestStats(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		testStats(t, Stats{},
			0,
			0,
			0,
			1.0,
			0.0,
			0,
			0,
			0,
			0,
			0,
			0,
			0.0,
			0,
		)
	})

	t.Run("populated", func(t *testing.T) {
		t.Parallel()

		testStats(t, Stats{
			Hits:           11,
			Misses:         13,
			Evictions:      27,
			EvictionWeight: 54,
			LoadSuccesses:  17,
			LoadFailures:   19,
			TotalLoadTime:  23,
		},
			11,
			13,
			24,
			11.0/24,
			13.0/24,
			27,
			54,
			17,
			19,
			23,
			36,
			19.0/36,
			0,
		)
	})

	t.Run("overflow", func(t *testing.T) {
		t.Parallel()

		testStats(t, Stats{
			Hits:           math.MaxUint64,
			Misses:         math.MaxUint64,
			Evictions:      27,
			EvictionWeight: 54,
			LoadSuccesses:  math.MaxUint64,
			LoadFailures:   math.MaxUint64,
			TotalLoadTime:  math.MaxInt64,
		},
			math.MaxUint64,
			math.MaxUint64,
			math.MaxUint64,
			1.0,
			1.0,
			27,
			54,
			math.MaxUint64,
			math.MaxUint64,
			math.MaxInt64,
			math.MaxUint64,
			1.0,
			1,
		)
	})

	t.Run("minus", func(t *testing.T) {
		t.Parallel()

		testStats(t, Stats{
			Hits:           11,
			Misses:         13,
			Evictions:      27,
			EvictionWeight: 54,
			LoadSuccesses:  17,
			LoadFailures:   19,
			TotalLoadTime:  23,
		}.Minus(Stats{
			Hits:           1,
			Misses:         14,
			Evictions:      27,
			EvictionWeight: 10,
			LoadSuccesses:  0,
			LoadFailures:   5,
			TotalLoadTime:  4,
		}),
			10,
			0,
			10,
			10.0/10,
			0.0/10,
			0,
			44,
			17,
			14,
			19,
			31,
			14.0/31,
			0,
		)
	})

	t.Run("plus", func(t *testing.T) {
		t.Parallel()

		testStats(t, Stats{
			Hits:           11,
			Misses:         13,
			Evictions:      27,
			EvictionWeight: 54,
			LoadSuccesses:  17,
			LoadFailures:   19,
			TotalLoadTime:  23,
		}.Plus(Stats{
			Hits:           1,
			Misses:         math.MaxUint64 - 10,
			Evictions:      27,
			EvictionWeight: 10,
			LoadSuccesses:  0,
			LoadFailures:   5,
			TotalLoadTime:  4,
		}),
			12,
			math.MaxUint64,
			math.MaxUint64,
			12.0/math.MaxUint64,
			float64(math.MaxUint64)/math.MaxUint64,
			54,
			64,
			17,
			24,
			27,
			41,
			24.0/41,
			0,
		)
	})
}
