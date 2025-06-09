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
			hits:           1,
			misses:         1,
			evictions:      1,
			evictionWeight: 10,
			loadSuccesses:  1,
			loadFailures:   1,
			totalLoadTime:  2,
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
			totalLoadTime: math.MaxInt64,
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
		hits:           50,
		misses:         50,
		evictions:      50,
		evictionWeight: 500,
		loadSuccesses:  50,
		loadFailures:   50,
		totalLoadTime:  100,
	}

	if got := c.Snapshot(); got != expected {
		t.Fatalf("got = %+v, expected = %+v", got, expected)
	}
}
