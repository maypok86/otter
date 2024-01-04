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

// Stats is a thread-safe statistics collector.
type Stats struct {
	hits   *counter
	misses *counter
}

// New creates a new Stats collector.
func New() *Stats {
	return &Stats{
		hits:   newCounter(),
		misses: newCounter(),
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

// Ratio returns the cache hit ratio.
func (s *Stats) Ratio() float64 {
	if s == nil {
		return 0.0
	}

	hits := s.hits.value()
	misses := s.misses.value()
	if hits == 0 && misses == 0 {
		return 0.0
	}
	return float64(hits) / float64(hits+misses)
}

func (s *Stats) Clear() {
	if s == nil {
		return
	}

	s.hits.reset()
	s.misses.reset()
}
