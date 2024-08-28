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
	rejectedSets uint64,
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

	if s.Hits() != hits {
		t.Fatalf("hits should be %d, but got %d", hits, s.Hits())
	}
	if s.Misses() != misses {
		t.Fatalf("misses should be %d, but got %d", misses, s.Misses())
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
	if s.RejectedSets() != rejectedSets {
		t.Fatalf("rejectedSets should be %d, but got %d", rejectedSets, s.RejectedSets())
	}
	if s.Evictions() != evictions {
		t.Fatalf("evictions should be %d, but got %d", evictions, s.Evictions())
	}
	if s.EvictionWeight() != evictionWeight {
		t.Fatalf("evictionWeight should be %d, but got %d", evictionWeight, s.EvictionWeight())
	}
	if s.LoadSuccesses() != loadSuccesses {
		t.Fatalf("loadSuccesses should be %d, but got %d", loadSuccesses, s.LoadSuccesses())
	}
	if s.LoadFailures() != loadFailures {
		t.Fatalf("loadFailures should be %d, but got %d", loadFailures, s.LoadFailures())
	}
	if s.TotalLoadTime() != totalLoadTime {
		t.Fatalf("totalLoadTime should be %d, but got %d", totalLoadTime, s.TotalLoadTime())
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
	t.Run("empty", func(t *testing.T) {
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
			0,
			0.0,
			0,
		)
	})

	t.Run("populated", func(t *testing.T) {
		testStats(t, Stats{
			hits:           11,
			misses:         13,
			evictions:      27,
			evictionWeight: 54,
			rejectedSets:   1,
			loadSuccesses:  17,
			loadFailures:   19,
			totalLoadTime:  23,
		},
			11,
			13,
			24,
			11.0/24,
			13.0/24,
			1,
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
		testStats(t, Stats{
			hits:           math.MaxUint64,
			misses:         math.MaxUint64,
			evictions:      27,
			evictionWeight: 54,
			rejectedSets:   1,
			loadSuccesses:  math.MaxUint64,
			loadFailures:   math.MaxUint64,
			totalLoadTime:  math.MaxInt64,
		},
			math.MaxUint64,
			math.MaxUint64,
			math.MaxUint64,
			1.0,
			1.0,
			1,
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
}
