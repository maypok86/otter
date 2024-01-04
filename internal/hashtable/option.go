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

package hashtable

import (
	"github.com/dolthub/maphash"
)

type Option[K comparable] func(*options[K])

type options[K comparable] struct {
	initNodeCount int
	hasher        func(K) uint64
}

func defaultOptions[K comparable]() *options[K] {
	hasher := maphash.NewHasher[K]()
	return &options[K]{
		initNodeCount: minNodeCount,
		hasher:        hasher.Hash,
	}
}

func WithHasher[K comparable](hasher func(K) uint64) Option[K] {
	return func(o *options[K]) {
		o.hasher = hasher
	}
}

func WithNodeCount[K comparable](initNodeCount int) Option[K] {
	return func(o *options[K]) {
		o.initNodeCount = initNodeCount
	}
}
