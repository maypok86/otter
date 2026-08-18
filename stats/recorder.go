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

import "time"

// Recorder accumulates statistics during the operation of a otter.Cache.
type Recorder interface {
	// RecordHits records cache hits. This should be called when a cache request returns a cached value.
	RecordHits(count int)
	// RecordMisses records cache misses. This should be called when a cache request returns a value that was not
	// found in the cache.
	RecordMisses(count int)
	// RecordEviction records the eviction of an entry from the cache. This should only been called when an entry is
	// evicted due to the cache's eviction strategy, and not as a result of manual deletions.
	RecordEviction(weight uint32)
	// RecordLoadSuccess records the successful load of a new entry. This method should be called when a cache request
	// causes an entry to be loaded and the loading completes successfully (either no error or otter.ErrNotFound).
	RecordLoadSuccess(loadTime time.Duration)
	// RecordLoadFailure records the failed load of a new entry. This method should be called when a cache request
	// causes an entry to be loaded, but the loading function returns an error that is not otter.ErrNotFound.
	RecordLoadFailure(loadTime time.Duration)
}

// EvictionCauseRecorder is an optional interface that a [Recorder] may also implement to
// record evictions broken down by their cause.
//
// When the recorder supplied to a cache implements EvictionCauseRecorder, the cache reports
// each eviction through the matching cause-specific method below instead of calling
// [Recorder.RecordEviction]. This lets a recorder distinguish evictions caused by the cache
// exceeding its size or weight bound from those caused by entry expiration, without having to
// attach a deletion listener. Recorders that don't need the breakdown can simply not implement
// this interface and keep using RecordEviction.
//
// As with RecordEviction, these methods are only called for evictions performed automatically by
// the cache's eviction strategy, and never as a result of manual deletions.
type EvictionCauseRecorder interface {
	// RecordOverflow records the eviction of an entry that was removed because the cache exceeded
	// its maximum size or weight.
	RecordOverflow(weight uint32)
	// RecordExpiration records the eviction of an entry that was removed because its expiration
	// time had passed.
	RecordExpiration(weight uint32)
}

// Snapshoter allows getting a stats snapshot from a recorder that implements it.
type Snapshoter interface {
	// Snapshot returns a snapshot of this recorder's values.
	Snapshot() Stats
}

// SnapshotRecorder is the interface that groups the [Snapshoter] and [Recorder] interfaces.
type SnapshotRecorder interface {
	Snapshoter
	Recorder
}

// NoopRecorder is a noop stats recorder. It can be useful if recording statistics is not necessary.
type NoopRecorder struct{}

func (np *NoopRecorder) RecordHits(count int)                     {}
func (np *NoopRecorder) RecordMisses(count int)                   {}
func (np *NoopRecorder) RecordEviction(weight uint32)             {}
func (np *NoopRecorder) RecordLoadFailure(loadTime time.Duration) {}
func (np *NoopRecorder) RecordLoadSuccess(loadTime time.Duration) {}
func (np *NoopRecorder) Snapshot() Stats {
	return Stats{}
}
