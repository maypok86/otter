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
	"time"

	"github.com/maypok86/otter/v2/internal/xsync"
)

// Counter is a goroutine-safe otter.StatsCollector implementation for use by otter.Cache.
type Counter struct {
	hits           *xsync.Adder
	misses         *xsync.Adder
	evictions      *xsync.Adder
	evictionWeight *xsync.Adder
	rejectedSets   *xsync.Adder
	loadSuccesses  *xsync.Adder
	loadFailures   *xsync.Adder
	totalLoadTime  *xsync.Adder
}

// NewCounter constructs a Counter instance with all counts initialized to zero.
func NewCounter() *Counter {
	return &Counter{
		hits:           xsync.NewAdder(),
		misses:         xsync.NewAdder(),
		evictions:      xsync.NewAdder(),
		evictionWeight: xsync.NewAdder(),
		rejectedSets:   xsync.NewAdder(),
		loadSuccesses:  xsync.NewAdder(),
		loadFailures:   xsync.NewAdder(),
		totalLoadTime:  xsync.NewAdder(),
	}
}

// Snapshot returns a snapshot of this collector's values. Note that this may be an inconsistent view, as it
// may be interleaved with update operations.
//
// NOTE: the values of the metrics are undefined in case of overflow. If you require specific handling, we recommend
// implementing your own otter.StatsCollector.
func (c *Counter) Snapshot() Stats {
	totalLoadTime := c.totalLoadTime.Value()
	if totalLoadTime > uint64(math.MaxInt64) {
		totalLoadTime = uint64(math.MaxInt64)
	}
	return Stats{
		hits:           c.hits.Value(),
		misses:         c.misses.Value(),
		evictions:      c.evictions.Value(),
		evictionWeight: c.evictionWeight.Value(),
		rejectedSets:   c.rejectedSets.Value(),
		loadSuccesses:  c.loadSuccesses.Value(),
		loadFailures:   c.loadFailures.Value(),
		totalLoadTime:  time.Duration(totalLoadTime),
	}
}

// CollectHits collects cache hits. This should be called when a cache request returns a cached value.
func (c *Counter) CollectHits(count int) {
	c.hits.Add(uint64(count))
}

// CollectMisses collects cache misses. This should be called when a cache request returns a value that was not
// found in the cache.
func (c *Counter) CollectMisses(count int) {
	c.misses.Add(uint64(count))
}

// CollectEviction collects the eviction of an entry from the cache. This should only been called when an entry is
// evicted due to the cache's eviction strategy, and not as a result of manual deletions.
func (c *Counter) CollectEviction(weight uint32) {
	c.evictions.Add(1)
	c.evictionWeight.Add(uint64(weight))
}

// CollectRejectedSets collects rejected sets due to too much weight of entries in them.
func (c *Counter) CollectRejectedSets(count int) {
	c.rejectedSets.Add(uint64(count))
}

// CollectLoadSuccess collects the successful load of a new entry. This method should be called when a cache request
// causes an entry to be loaded and the loading completes successfully.
func (c *Counter) CollectLoadSuccess(loadTime time.Duration) {
	c.loadSuccesses.Add(1)
	c.totalLoadTime.Add(uint64(loadTime))
}

// CollectLoadFailure collects the failed load of a new entry. This method should be called when a cache request
// causes an entry to be loaded, but the loading function returns an error.
func (c *Counter) CollectLoadFailure(loadTime time.Duration) {
	c.loadFailures.Add(1)
	c.totalLoadTime.Add(uint64(loadTime))
}
