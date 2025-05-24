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

import "context"

// Reloader computes or retrieves a replacement value corresponding to an already-cached key, for use in populating a Cache.
type Reloader[K comparable, V any] interface {
	// Reload computes or retrieves a replacement value corresponding to an already-cached key.
	// If the replacement value is not found, then the mapping will be removed if ErrNotFound is returned.
	// This method is called when an existing cache entry is refreshed by Cache.Get, or through a call to Cache.Refresh.
	//
	// WARNING: loading must not attempt to update any mappings of this cache directly
	// or block waiting for other cache operations to complete.
	//
	// NOTE: all errors returned during refresh will be logged (using Logger) and then swallowed.
	Reload(ctx context.Context, key K) (V, error)
}

// ReloaderFunc is an adapter to allow the use of ordinary functions as reloaders.
// If f is a function with the appropriate signature, ReloaderFunc(f) is a Reloader that calls f.
type ReloaderFunc[K comparable, V any] func(ctx context.Context, key K) (V, error)

// Reload calls f(ctx, key).
func (rf ReloaderFunc[K, V]) Reload(ctx context.Context, key K) (V, error) {
	return rf(ctx, key)
}

// BulkReloader computes or retrieves replacement values corresponding to already-cached keys, for use in populating a Cache.
type BulkReloader[K comparable, V any] interface {
	// BulkReload computes or retrieves replacement values corresponding to already-cached keys.
	// If the replacement value is not found, then the mapping will be removed.
	// This method is called when an existing cache entry is refreshed by Cache.BulkGet, or through a call to Cache.BulkRefresh.
	//
	// WARNING: loading must not attempt to update any mappings of this cache directly
	// or block waiting for other cache operations to complete.
	//
	// NOTE: all errors returned during refresh will be logged (using Logger) and then swallowed.
	BulkReload(ctx context.Context, keys []K) (map[K]V, error)
}

// BulkReloaderFunc is an adapter to allow the use of ordinary functions as reloaders.
// If f is a function with the appropriate signature, BulkReloaderFunc(f) is a BulkReloader that calls f.
type BulkReloaderFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

// BulkReload calls f(ctx, keys).
func (brf BulkReloaderFunc[K, V]) BulkReload(ctx context.Context, keys []K) (map[K]V, error) {
	return brf(ctx, keys)
}
