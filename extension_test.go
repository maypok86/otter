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

	"github.com/maypok86/otter/v2/stats"
)

func TestCache_SetExpiresAfter(t *testing.T) {
	size := 100
	statsCounter := stats.NewCounter()
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	done := make(chan struct{})
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](time.Second),
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
	_, ok := c.GetEntryQuietly(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.SetExpiresAfter(k1, 2*time.Second)
	_, ok = c.GetEntry(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.Set(k1, v1)
	e, ok := c.GetEntry(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if expiresAfter := e.ExpiresAfter(); expiresAfter < 800*time.Millisecond || expiresAfter > time.Second {
		t.Fatalf("expiresAfter should be equal to %v. expiresAfter: %v", 200*time.Millisecond, expiresAfter)
	}
	c.SetExpiresAfter(k1, 2*time.Second)
	e, ok = c.GetEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key = %v", k1)
	}
	if e.Value != v1 {
		t.Fatalf("value should be equal to v1. key: %v, value: %v", k1, e.Value)
	}
	if expiresAfter := e.ExpiresAfter(); expiresAfter > 2*time.Second || expiresAfter < time.Second+800*time.Millisecond {
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
		RefreshCalculator: RefreshCreating[int, int](200 * time.Millisecond),
	})

	k1 := 1
	v1 := 100

	c.SetRefreshableAfter(k1, -2*time.Second)
	_, ok := c.GetEntryQuietly(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.SetRefreshableAfter(k1, 2*time.Second)
	_, ok = c.GetEntry(k1)
	if ok {
		t.Fatalf("found key = %v", k1)
	}
	c.Set(k1, v1)
	e, ok := c.GetEntry(k1)
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
	e, ok = c.GetEntryQuietly(k1)
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

func TestCache_Extension(t *testing.T) {
	size := getRandomSize(t)

	duration := time.Hour
	c := Must(&Options[int, int]{
		MaximumSize:       size,
		ExpiryCalculator:  ExpiryWriting[int, int](duration),
		RefreshCalculator: RefreshWriting[int, int](duration),
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	k1 := 4
	v1 := k1
	e1, ok := c.GetEntryQuietly(k1)
	if !ok {
		t.Fatalf("not found key %d", k1)
	}

	e2, ok := c.GetEntry(k1)
	if !ok {
		t.Fatalf("not found key %d", k1)
	}

	time.Sleep(time.Second)

	isEqualEntries := func(a, b Entry[int, int]) bool {
		return a.Key == b.Key &&
			a.Value == b.Value &&
			a.Weight == b.Weight &&
			a.ExpiresAtNano == b.ExpiresAtNano &&
			a.RefreshableAtNano == b.RefreshableAtNano
	}

	isValidEntries := e1.Key == k1 &&
		e1.Value == v1 &&
		e1.Weight == 1 &&
		isEqualEntries(e1, e2) &&
		e1.ExpiresAfter() < duration &&
		!e1.HasExpired()

	if !isValidEntries {
		t.Fatalf("found not valid entries. e1: %+v, e2: %+v, v1:%d", e1, e2, v1)
	}

	if _, ok := c.GetEntryQuietly(size); ok {
		t.Fatalf("found not valid key: %d", size)
	}
	if _, ok := c.GetEntry(size); ok {
		t.Fatalf("found not valid key: %d", size)
	}
}
