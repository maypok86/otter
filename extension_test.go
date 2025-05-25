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
	"sync"
	"testing"
	"time"

	"github.com/maypok86/otter/v2/core/expiry"
	"github.com/maypok86/otter/v2/core/refresh"
	"github.com/maypok86/otter/v2/core/stats"
)

func TestCache_SetExpiresAfter(t *testing.T) {
	t.Parallel()

	size := 100
	statsCounter := stats.NewCounter()
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	done := make(chan struct{})
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: expiry.Writing[int, int](200 * time.Millisecond),
		OnDeletion: func(e DeletionEvent[int, int]) {
			defer func() {
				done <- struct{}{}
			}()

			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	k1 := 1
	v1 := 100

	c.SetExpiresAfter(k1, -2*time.Second)
	_, ok := c.getEntryQuietly(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.SetExpiresAfter(k1, 2*time.Second)
	_, ok = c.getEntry(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.Set(k1, v1)
	e, ok := c.getEntry(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if expiresAfter := e.ExpiresAfter(); expiresAfter > 200*time.Millisecond {
		t.Fatalf("expiresAfter should be equal to %v. expiresAfter: %v", 200*time.Millisecond, expiresAfter)
	}
	c.SetExpiresAfter(k1, time.Second)
	e, ok = c.getEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if expiresAfter := e.ExpiresAfter(); expiresAfter > time.Second || expiresAfter < 500*time.Millisecond {
		t.Fatalf("expiresAfter should be equal to %v. expiresAfter: %v", time.Second, expiresAfter)
	}

	<-done
	mutex.Lock()
	if len(m) != 1 || m[CauseExpiration] != 1 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", 1, m[CauseExpiration])
	}
	mutex.Unlock()
	snapshot := statsCounter.Snapshot()
	if snapshot.Hits() != 1 ||
		snapshot.Misses() != 1 ||
		snapshot.Evictions() != 1 ||
		snapshot.EvictionWeight() != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}

func TestCache_SetRefreshableAfter(t *testing.T) {
	t.Parallel()

	size := 100
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		StatsRecorder:     statsCounter,
		RefreshCalculator: refresh.Creating[int, int](200 * time.Millisecond),
	})

	k1 := 1
	v1 := 100

	c.SetRefreshableAfter(k1, -2*time.Second)
	_, ok := c.getEntryQuietly(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.SetRefreshableAfter(k1, 2*time.Second)
	_, ok = c.getEntry(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.Set(k1, v1)
	e, ok := c.getEntry(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if refreshableAfter := e.RefreshableAfter(); refreshableAfter > 200*time.Millisecond {
		t.Fatalf("refreshableAfter should be equal to %v. refreshableAfter: %v", 200*time.Millisecond, refreshableAfter)
	}
	c.SetRefreshableAfter(k1, time.Second)
	e, ok = c.getEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if refreshableAfter := e.RefreshableAfter(); refreshableAfter > time.Second || refreshableAfter < 500*time.Millisecond {
		t.Fatalf("refreshableAfter should be equal to %v. refreshableAfter: %v", time.Second, refreshableAfter)
	}

	snapshot := statsCounter.Snapshot()
	if snapshot.Hits() != 1 ||
		snapshot.Misses() != 1 {
		t.Fatalf("statistics are not recorded correctly. snapshot: %v", snapshot)
	}
}
