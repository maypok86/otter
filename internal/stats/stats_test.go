// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
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
	"math/rand"
	"testing"
	"time"
)

const (
	maxCount = 10_000
)

func generateCount(t *testing.T) int64 {
	t.Helper()

	r := rand.NewSource(time.Now().UnixNano())
	count := r.Int63() % maxCount
	if count == 0 {
		return 1
	}

	return count
}

func TestStats_Nil(t *testing.T) {
	var s *Stats
	expected := int64(0)
	for _, inc := range []func(){
		s.IncHits,
		s.IncMisses,
		s.IncRejectedSets,
		s.IncEvictedCount,
		func() {
			s.AddEvictedWeight(1)
		},
		s.IncHits,
		s.IncMisses,
	} {
		inc()
	}
	for _, f := range []func() int64{
		s.Hits,
		s.Misses,
		s.RejectedSets,
		s.EvictedCount,
		s.EvictedWeight,
	} {
		if expected != f() {
			t.Fatalf("hits and misses for nil stats should always be %d", expected)
		}
	}
	s.Clear()
}

func TestStats_Hits(t *testing.T) {
	expected := generateCount(t)

	s := New()
	for i := int64(0); i < expected; i++ {
		s.IncHits()
	}

	hits := s.Hits()
	if expected != hits {
		t.Fatalf("number of hits should be %d, but got %d", expected, hits)
	}
}

func TestStats_Misses(t *testing.T) {
	expected := generateCount(t)

	s := New()
	for i := int64(0); i < expected; i++ {
		s.IncMisses()
	}

	misses := s.Misses()
	if expected != misses {
		t.Fatalf("number of misses should be %d, but got %d", expected, misses)
	}
}

func TestStats_RejectedSets(t *testing.T) {
	expected := generateCount(t)

	s := New()
	for i := int64(0); i < expected; i++ {
		s.IncRejectedSets()
	}

	rejectedSets := s.RejectedSets()
	if expected != rejectedSets {
		t.Fatalf("number of rejected sets should be %d, but got %d", expected, rejectedSets)
	}
}

func TestStats_EvictedCount(t *testing.T) {
	expected := generateCount(t)

	s := New()
	for i := int64(0); i < expected; i++ {
		s.IncEvictedCount()
	}

	evictedCount := s.EvictedCount()
	if expected != evictedCount {
		t.Fatalf("number of evicted entries should be %d, but got %d", expected, evictedCount)
	}
}

func TestStats_EvictedWeight(t *testing.T) {
	expected := generateCount(t)

	s := New()
	k := int64(0)
	for k < expected {
		add := 2
		if expected-k < 2 {
			add = 1
		}
		k += int64(add)
		s.AddEvictedWeight(uint32(add))
	}

	evictedWeight := s.EvictedWeight()
	if expected != evictedWeight {
		t.Fatalf("sum of weights of evicted entries should be %d, but got %d", expected, evictedWeight)
	}
}

func TestStats_Clear(t *testing.T) {
	s := New()

	count := generateCount(t)
	for i := int64(0); i < count; i++ {
		s.IncHits()
	}
	for i := int64(0); i < count; i++ {
		s.IncMisses()
	}

	misses := s.Misses()
	if count != misses {
		t.Fatalf("number of misses should be %d, but got %d", count, misses)
	}
	hits := s.Hits()
	if count != hits {
		t.Fatalf("number of hits should be %d, but got %d", count, hits)
	}

	s.Clear()

	hits = s.Hits()
	misses = s.Misses()
	if hits != 0 || misses != 0 {
		t.Fatalf("hits and misses after clear should be 0, but got hits: %d and misses: %d", hits, misses)
	}
}
