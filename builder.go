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

import "errors"

const (
	defaultStatsEnabled = false
)

// ErrIllegalCapacity means that a non-positive capacity has been passed to the NewBuilder.
var ErrIllegalCapacity = errors.New("capacity should be positive")

type options[K comparable, V any] struct {
	capacity     int
	statsEnabled bool
	costFunc     func(key K, value V) uint32
}

func (o *options[K, V]) validate() error {
	return nil
}

func (o *options[K, V]) toConfig() Config[K, V] {
	return Config[K, V]{
		Capacity:     o.capacity,
		StatsEnabled: o.statsEnabled,
		CostFunc:     o.costFunc,
	}
}

// Builder is a one-shot builder for creating a cache instance.
type Builder[K comparable, V any] struct {
	options[K, V]
}

// MustBuilder creates a builder and sets the future cache capacity.
//
// Panics if capacity <= 0.
func MustBuilder[K comparable, V any](capacity int) *Builder[K, V] {
	b, err := NewBuilder[K, V](capacity)
	if err != nil {
		panic(err)
	}
	return b
}

// NewBuilder creates a builder and sets the future cache capacity.
//
// Returns an error if capacity <= 0.
func NewBuilder[K comparable, V any](capacity int) (*Builder[K, V], error) {
	if capacity <= 0 {
		return nil, ErrIllegalCapacity
	}

	return &Builder[K, V]{
		options: options[K, V]{
			capacity:     capacity,
			statsEnabled: defaultStatsEnabled,
			costFunc: func(key K, value V) uint32 {
				return 1
			},
		},
	}, nil
}

// StatsEnabled determines whether statistics should be calculated when the cache is running.
//
// By default, statistics calculating is disabled.
func (b *Builder[K, V]) StatsEnabled(statsEnabled bool) *Builder[K, V] {
	b.statsEnabled = statsEnabled
	return b
}

// Cost sets a function to dynamically calculate the cost of a key-value pair.
//
// By default, this function always returns 1.
func (b *Builder[K, V]) Cost(costFunc func(key K, value V) uint32) *Builder[K, V] {
	b.costFunc = costFunc
	return b
}

// Build creates a configured cache or
// returns an error if invalid parameters were passed to the builder.
func (b *Builder[K, V]) Build() (*Cache[K, V], error) {
	if err := b.validate(); err != nil {
		return nil, err
	}

	return NewCache(b.toConfig()), nil
}
