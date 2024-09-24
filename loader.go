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

package otter

import "context"

// Loader computes or retrieves values, based on a key, for use in populating a Cache.
type Loader[K comparable, V any] interface {
	// Load computes or retrieves the value corresponding to key.
	//
	// WARNING: loading must not attempt to update any mappings of this cache directly.
	Load(ctx context.Context, key K) (V, error)
}

// LoaderFunc is an adapter to allow the use of ordinary functions as loaders.
// If f is a function with the appropriate signature, LoaderFunc(f) is a Loader that calls f.
type LoaderFunc[K comparable, V any] func(ctx context.Context, key K) (V, error)

// Load calls f(ctx, key).
func (lf LoaderFunc[K, V]) Load(ctx context.Context, key K) (V, error) {
	return lf(ctx, key)
}

// BulkLoader computes or retrieves values, based on the keys, for use in populating a Cache.
type BulkLoader[K comparable, V any] interface {
	// BulkLoad computes or retrieves the values corresponding to keys.
	//
	// WARNING: loading must not attempt to update any mappings of this cache directly.
	BulkLoad(ctx context.Context, keys []K) (map[K]V, error)
}

// BulkLoaderFunc is an adapter to allow the use of ordinary functions as loaders.
// If f is a function with the appropriate signature, BulkLoaderFunc(f) is a BulkLoader that calls f.
type BulkLoaderFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

// BulkLoad calls f(ctx, keys).
func (blf BulkLoaderFunc[K, V]) BulkLoad(ctx context.Context, keys []K) (map[K]V, error) {
	return blf(ctx, keys)
}