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

package otter

import (
	"errors"
	"time"
)

// Builder is a one-shot builder for creating a cache instance.
type Builder[K comparable, V any] struct {
	maximumSize     *int
	maximumWeight   *uint64
	initialCapacity *int
	statsRecorder   StatsRecorder
	ttl             *time.Duration
	withVariableTTL bool
	weigher         func(key K, value V) uint32
	onDeletion      func(e DeletionEvent[K, V])
	logger          Logger
}

// NewBuilder creates a builder and sets the future cache capacity.
func NewBuilder[K comparable, V any]() *Builder[K, V] {
	return &Builder[K, V]{
		statsRecorder: noopStatsRecorder{},
		logger:        noopLogger{},
	}
}

// MaximumSize specifies the maximum number of entries the cache may contain.
//
// This option cannot be used in conjunction with MaximumWeight.
//
// NOTE: the cache may evict an entry before this limit is exceeded or temporarily exceed the threshold while evicting.
// As the cache size grows close to the maximum, the cache evicts entries that are less likely to be used again.
// For example, the cache may evict an entry because it hasn't been used recently or very often.
func (b *Builder[K, V]) MaximumSize(maximumSize int) *Builder[K, V] {
	b.maximumSize = &maximumSize
	return b
}

// MaximumWeight specifies the maximum weight of entries the cache may contain. Weight is determined using the
// callback specified with Weigher.
// Use of this method requires a corresponding call to Weigher prior to calling Build.
//
// This option cannot be used in conjunction with MaximumSize.
//
// NOTE: the cache may evict an entry before this limit is exceeded or temporarily exceed the threshold while evicting.
// As the cache size grows close to the maximum, the cache evicts entries that are less likely to be used again.
// For example, the cache may evict an entry because it hasn't been used recently or very often.
//
// NOTE: weight is only used to determine whether the cache is over capacity; it has no effect
// on selecting which entry should be evicted next.
func (b *Builder[K, V]) MaximumWeight(maximumWeight uint64) *Builder[K, V] {
	b.maximumWeight = &maximumWeight
	return b
}

// RecordStats enables the accumulation of statistics during the operation of the cache.
//
// NOTE: recording statistics requires bookkeeping to be performed with each operation,
// and thus imposes a performance penalty on cache operations.
func (b *Builder[K, V]) RecordStats(statsRecorder StatsRecorder) *Builder[K, V] {
	b.statsRecorder = statsRecorder
	return b
}

// InitialCapacity sets the minimum total size for the internal data structures. Providing a large enough estimate
// at construction time avoids the need for expensive resizing operations later, but setting this
// value unnecessarily high wastes memory.
func (b *Builder[K, V]) InitialCapacity(initialCapacity int) *Builder[K, V] {
	b.initialCapacity = &initialCapacity
	return b
}

// Weigher specifies the weigher to use in determining the weight of entries. Entry weight is taken into
// consideration by MaximumWeight when determining which entries to evict, and use
// of this method requires a corresponding call to MaximumWeight prior to calling Build.
// Weights are measured and recorded when entries are inserted into or updated in
// the cache, and are thus effectively static during the lifetime of a cache entry.
//
// When the weight of an entry is zero it will not be considered for size-based eviction (though
// it still may be evicted by other means).
func (b *Builder[K, V]) Weigher(weigher func(key K, value V) uint32) *Builder[K, V] {
	b.weigher = weigher
	return b
}

// OnDeletion specifies a handler that caches should notify each time an entry is deleted for any
// DeletionCause. The cache will invoke this handler in the background goroutine
// after the entry's deletion operation has completed.
func (b *Builder[K, V]) OnDeletion(onDeletion func(e DeletionEvent[K, V])) *Builder[K, V] {
	b.onDeletion = onDeletion
	return b
}

// WithTTL specifies that each item should be automatically removed from the cache once a fixed duration
// has elapsed after the item's creation.
func (b *Builder[K, V]) WithTTL(ttl time.Duration) *Builder[K, V] {
	b.ttl = &ttl
	return b
}

// WithVariableTTL specifies that each item should be automatically removed from the cache once a duration has
// elapsed after the item's creation. Items are expired based on the custom ttl specified for each item separately.
//
// You should prefer WithTTL to this option whenever possible.
func (b *Builder[K, V]) WithVariableTTL() *Builder[K, V] {
	b.withVariableTTL = true
	return b
}

// Logger specifies the Logger implementation that will be used for logging warning and errors.
//
// Logging is disabled by default.
func (b *Builder[K, V]) Logger(logger Logger) *Builder[K, V] {
	b.logger = logger
	return b
}

func (b *Builder[K, V]) getMaximum() *uint64 {
	if b.maximumSize != nil {
		//nolint:gosec // there is no overflow
		ms := uint64(*b.maximumSize)
		return &ms
	}
	if b.maximumWeight != nil {
		return b.maximumWeight
	}
	return nil
}

func (b *Builder[K, V]) getWeigher() func(key K, value V) uint32 {
	if b.weigher == nil {
		return func(key K, value V) uint32 {
			return 1
		}
	}
	return b.weigher
}

func (b *Builder[K, V]) validate() error {
	if b.maximumSize != nil && b.maximumWeight != nil {
		return errors.New("otter: both maximumSize and maximumWeight are set")
	}
	if b.maximumSize != nil && b.weigher != nil {
		return errors.New("otter: both maximumSize and weigher are set")
	}
	if b.maximumSize != nil && *b.maximumSize <= 0 {
		return errors.New("otter: maximumSize should be positive")
	}

	if b.maximumWeight != nil && *b.maximumWeight <= 0 {
		return errors.New("otter: maximumWeight should be positive")
	}
	if b.maximumWeight != nil && b.weigher == nil {
		return errors.New("otter: maximumWeight requires weigher")
	}
	if b.weigher != nil && b.maximumWeight == nil {
		return errors.New("otter: weigher requires maximumWeight")
	}

	if b.initialCapacity != nil && *b.initialCapacity <= 0 {
		return errors.New("otter: initial capacity should be positive")
	}
	if b.statsRecorder == nil {
		return errors.New("otter: stats collector should not be nil")
	}
	if b.logger == nil {
		return errors.New("otter: logger should not be nil")
	}
	if b.ttl != nil && *b.ttl <= 0 {
		return errors.New("otter: ttl should be positive")
	}

	return nil
}

// Build creates a configured cache or
// returns an error if invalid parameters were passed to the builder.
func (b *Builder[K, V]) Build() (*Cache[K, V], error) {
	if err := b.validate(); err != nil {
		return nil, err
	}

	return newCache(b), nil
}
