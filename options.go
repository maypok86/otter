// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
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
	"errors"

	"github.com/maypok86/otter/v2/core/expiry"
	"github.com/maypok86/otter/v2/core/refresh"
	"github.com/maypok86/otter/v2/core/stats"
)

const (
	defaultInitialCapacity = 16
)

// Options should be passed to New to construct a Cache.
type Options[K comparable, V any] struct {
	// MaximumSize specifies the maximum number of entries the cache may contain.
	//
	// This option cannot be used in conjunction with MaximumWeight.
	//
	// NOTE: the cache may evict an entry before this limit is exceeded or temporarily exceed the threshold while evicting.
	// As the cache size grows close to the maximum, the cache evicts entries that are less likely to be used again.
	// For example, the cache may evict an entry because it hasn't been used recently or very often.
	MaximumSize int
	// MaximumWeight specifies the maximum weight of entries the cache may contain. Weight is determined using the
	// callback specified with Weigher.
	// Use of this method requires specifying an option Weigher prior to calling New.
	//
	// This option cannot be used in conjunction with MaximumSize.
	//
	// NOTE: the cache may evict an entry before this limit is exceeded or temporarily exceed the threshold while evicting.
	// As the cache size grows close to the maximum, the cache evicts entries that are less likely to be used again.
	// For example, the cache may evict an entry because it hasn't been used recently or very often.
	//
	// NOTE: weight is only used to determine whether the cache is over capacity; it has no effect
	// on selecting which entry should be evicted next.
	MaximumWeight uint64
	// StatsRecorder accumulates statistics during the operation of a Cache.
	StatsRecorder stats.Recorder
	// InitialCapacity specifies the minimum total size for the internal data structures. Providing a large enough estimate
	// at construction time avoids the need for expensive resizing operations later, but setting this
	// value unnecessarily high wastes memory.
	InitialCapacity int
	// Weigher specifies the weigher to use in determining the weight of entries. Entry weight is taken into
	// consideration by MaximumWeight when determining which entries to evict, and use
	// of this method requires specifying an option MaximumWeight prior to calling New.
	// Weights are measured and recorded when entries are inserted into or updated in
	// the cache, and are thus effectively static during the lifetime of a cache entry.
	//
	// When the weight of an entry is zero it will not be considered for size-based eviction (though
	// it still may be evicted by other means).
	Weigher func(key K, value V) uint32
	// ExpiryCalculator specifies that each entry should be automatically removed from the cache once a duration has
	// elapsed after the entry's creation, the most recent replacement of its value, or its last read.
	// The expiration time is reset by all cache read and write operations.
	ExpiryCalculator expiry.Calculator[K, V]
	// OnDeletion specifies a handler that caches should notify each time an entry is deleted for any
	// DeletionCause. The cache will invoke this handler in the background goroutine
	// after the entry's deletion operation has completed.
	OnDeletion func(e DeletionEvent[K, V])
	// RefreshCalculator specifies that active entries are eligible for automatic refresh once a duration has
	// elapsed after the entry's creation, the most recent replacement of its value, or the most recent entry's reload.
	// The semantics of refreshes are specified in Cache.Refresh,
	// and are performed by calling Reloader.Reload in a separate background goroutine.
	//
	// Automatic refreshes are performed when the first stale request for an entry occurs. The request
	// triggering the refresh will make an asynchronous call to Reloader.Reload to get a new value.
	// Until refresh is completed, requests will continue to return the old value.
	//
	// NOTE: all errors returned during refresh will be logged (using Logger) and then swallowed.
	RefreshCalculator refresh.Calculator[K, V]
	// Logger specifies the Logger implementation that will be used for logging warning and errors.
	//
	// Logging is disabled by default.
	Logger Logger
}

func (o *Options[K, V]) getMaximum() uint64 {
	if o.MaximumSize > 0 {
		return uint64(o.MaximumSize)
	}
	if o.MaximumWeight > 0 {
		return o.MaximumWeight
	}
	return 0
}

func (o *Options[K, V]) hasInitialCapacity() bool {
	return o.InitialCapacity > 0
}

func (o *Options[K, V]) getInitialCapacity() int {
	if o.hasInitialCapacity() {
		return o.InitialCapacity
	}
	return defaultInitialCapacity
}

func (o *Options[K, V]) validate() error {
	if o.MaximumSize > 0 && o.MaximumWeight > 0 {
		return errors.New("otter: both maximumSize and maximumWeight are set")
	}
	if o.MaximumSize > 0 && o.Weigher != nil {
		return errors.New("otter: both maximumSize and weigher are set")
	}

	if o.MaximumWeight > 0 && o.Weigher == nil {
		return errors.New("otter: maximumWeight requires weigher")
	}
	if o.Weigher != nil && o.MaximumWeight <= 0 {
		return errors.New("otter: weigher requires maximumWeight")
	}

	if o.MaximumSize < 0 {
		return errors.New("otter: maximumSize should be positive")
	}
	if o.InitialCapacity < 0 {
		return errors.New("otter: initial capacity should be positive")
	}

	return nil
}

func (o *Options[K, V]) setDefaults() {
	if o.Weigher == nil {
		o.Weigher = func(key K, value V) uint32 {
			return 1
		}
	}
	if o.Logger == nil {
		o.Logger = noopLogger{}
	}
}
