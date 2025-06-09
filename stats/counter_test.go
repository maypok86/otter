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
	"sync"
	"testing"
)

func TestCounter_Basic(t *testing.T) {
	t.Parallel()

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()

		c := NewCounter()
		c.RecordHits(1)
		c.RecordMisses(1)
		c.RecordEviction(10)
		c.RecordLoadSuccess(1)
		c.RecordLoadFailure(1)

		expected := Stats{
			Hits:           1,
			Misses:         1,
			Evictions:      1,
			EvictionWeight: 10,
			LoadSuccesses:  1,
			LoadFailures:   1,
			TotalLoadTime:  2,
		}
		if got := c.Snapshot(); got != expected {
			t.Fatalf("got = %+v, expected = %+v", got, expected)
		}
	})

	t.Run("overflow", func(t *testing.T) {
		t.Parallel()

		c := NewCounter()
		c.totalLoadTime.Add(math.MaxUint64)

		expected := Stats{
			TotalLoadTime: math.MaxInt64,
		}

		if got := c.Snapshot(); got != expected {
			t.Fatalf("got = %+v, expected = %+v", got, expected)
		}
	})
}

func TestCounter_Concurrent(t *testing.T) {
	t.Parallel()

	c := NewCounter()

	goroutines := 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			c.RecordHits(1)
			c.RecordMisses(1)
			c.RecordEviction(10)
			c.RecordLoadSuccess(1)
			c.RecordLoadFailure(1)
		}()
	}

	wg.Wait()

	expected := Stats{
		Hits:           50,
		Misses:         50,
		Evictions:      50,
		EvictionWeight: 500,
		LoadSuccesses:  50,
		LoadFailures:   50,
		TotalLoadTime:  100,
	}

	if got := c.Snapshot(); got != expected {
		t.Fatalf("got = %+v, expected = %+v", got, expected)
	}
}
