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
	capacity         *int
	initialCapacity  *int
	statsEnabled     bool
	ttl              *time.Duration
	withVariableTTL  bool
	weigher          func(key K, value V) uint32
	withWeight       bool
	deletionListener func(key K, value V, cause DeletionCause)
}

// NewBuilder creates a builder and sets the future cache capacity.
func NewBuilder[K comparable, V any](capacity int) *Builder[K, V] {
	return &Builder[K, V]{
		capacity: &capacity,
		weigher: func(key K, value V) uint32 {
			return 1
		},
	}
}

// CollectStats determines whether statistics should be calculated when the cache is running.
//
// By default, statistics calculating is disabled.
func (b *Builder[K, V]) CollectStats() *Builder[K, V] {
	b.statsEnabled = true
	return b
}

// InitialCapacity sets the minimum total size for the internal data structures. Providing a large enough estimate
// at construction time avoids the need for expensive resizing operations later, but setting this
// value unnecessarily high wastes memory.
func (b *Builder[K, V]) InitialCapacity(initialCapacity int) *Builder[K, V] {
	b.initialCapacity = &initialCapacity
	return b
}

// Weigher sets a function to dynamically calculate the weight of an item.
//
// By default, this function always returns 1.
func (b *Builder[K, V]) Weigher(weigher func(key K, value V) uint32) *Builder[K, V] {
	b.weigher = weigher
	b.withWeight = true
	return b
}

// DeletionListener specifies a listener instance that caches should notify each time an entry is deleted for any
// DeletionCause cause. The cache will invoke this listener in the background goroutine
// after the entry's deletion operation has completed.
func (b *Builder[K, V]) DeletionListener(deletionListener func(key K, value V, cause DeletionCause)) *Builder[K, V] {
	b.deletionListener = deletionListener
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

func (b *Builder[K, V]) validate() error {
	if b.capacity == nil || *b.capacity <= 0 {
		return errors.New("otter: not valid capacity")
	}
	if b.initialCapacity != nil && *b.initialCapacity <= 0 {
		return errors.New("otter: initial capacity should be positive")
	}
	if b.weigher == nil {
		return errors.New("otter: weigher should not be nil")
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
