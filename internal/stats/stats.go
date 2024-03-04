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
	"sync/atomic"

	"github.com/maypok86/otter/internal/xruntime"
)

// Stats is a thread-safe statistics collector.
type Stats struct {
	hits                   *counter
	misses                 *counter
	rejectedSets           *counter
	evictedCountersPadding [xruntime.CacheLineSize - 16]byte
	evictedCount           int64
	evictedCost            int64
}

// New creates a new Stats collector.
func New() *Stats {
	return &Stats{
		hits:         newCounter(),
		misses:       newCounter(),
		rejectedSets: newCounter(),
	}
}

// IncHits increments the hits counter.
func (s *Stats) IncHits() {
	if s == nil {
		return
	}

	s.hits.increment()
}

// Hits returns the number of cache hits.
func (s *Stats) Hits() int64 {
	if s == nil {
		return 0
	}

	return s.hits.value()
}

// IncMisses increments the misses counter.
func (s *Stats) IncMisses() {
	if s == nil {
		return
	}

	s.misses.increment()
}

// Misses returns the number of cache misses.
func (s *Stats) Misses() int64 {
	if s == nil {
		return 0
	}

	return s.misses.value()
}

// IncRejectedSets increments the rejectedSets counter.
func (s *Stats) IncRejectedSets() {
	if s == nil {
		return
	}

	s.rejectedSets.increment()
}

// RejectedSets returns the number of rejected sets.
func (s *Stats) RejectedSets() int64 {
	if s == nil {
		return 0
	}

	return s.rejectedSets.value()
}

// IncEvictedCount increments the evictedCount counter.
func (s *Stats) IncEvictedCount() {
	if s == nil {
		return
	}

	atomic.AddInt64(&s.evictedCount, 1)
}

// EvictedCount returns the number of evicted entries.
func (s *Stats) EvictedCount() int64 {
	if s == nil {
		return 0
	}

	return atomic.LoadInt64(&s.evictedCount)
}

// AddEvictedCost adds cost to the evictedCost counter.
func (s *Stats) AddEvictedCost(cost uint32) {
	if s == nil {
		return
	}

	atomic.AddInt64(&s.evictedCost, int64(cost))
}

// EvictedCost returns the sum of costs of evicted entries.
func (s *Stats) EvictedCost() int64 {
	if s == nil {
		return 0
	}

	return atomic.LoadInt64(&s.evictedCost)
}

func (s *Stats) Clear() {
	if s == nil {
		return
	}

	s.hits.reset()
	s.misses.reset()
	s.rejectedSets.reset()
	atomic.StoreInt64(&s.evictedCount, 0)
	atomic.StoreInt64(&s.evictedCost, 0)
}
