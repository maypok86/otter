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

package refresh

import (
	"time"

	"github.com/maypok86/otter/v2/core"
)

// Calculator calculates when cache entries will be reloaded. A single refresh time is retained so that the lifetime
// of an entry may be extended or reduced by subsequent evaluations.
type Calculator[K comparable, V any] interface {
	// RefreshAfterCreate returns the duration after which the entry is eligible for an automatic refresh after the
	// entry's creation. To indicate no refresh, an entry may be given an excessively long period.
	RefreshAfterCreate(entry core.Entry[K, V]) time.Duration
	// RefreshAfterUpdate returns the duration after which the entry is eligible for an automatic refresh after the
	// replacement of the entry's value due to an explicit update.
	// The entry.RefreshableAfter() may be returned to not modify the refresh time.
	RefreshAfterUpdate(entry core.Entry[K, V], oldValue V) time.Duration
	// RefreshAfterReload returns the duration after which the entry is eligible for an automatic refresh after the
	// replacement of the entry's value due to a reload.
	// The entry.RefreshableAfter() may be returned to not modify the refresh time.
	RefreshAfterReload(entry core.Entry[K, V], oldValue V) time.Duration
	// RefreshAfterReloadFailure returns the duration after which the entry is eligible for an automatic refresh after the
	// value failed to be reloaded.
	// The entry.RefreshableAfter() may be returned to not modify the refresh time.
	RefreshAfterReloadFailure(entry core.Entry[K, V], err error) time.Duration
}

type varCreating[K comparable, V any] struct {
	f func(entry core.Entry[K, V]) time.Duration
}

func (c *varCreating[K, V]) RefreshAfterCreate(entry core.Entry[K, V]) time.Duration {
	return c.f(entry)
}

func (c *varCreating[K, V]) RefreshAfterUpdate(entry core.Entry[K, V], oldValue V) time.Duration {
	return entry.RefreshableAfter()
}

func (c *varCreating[K, V]) RefreshAfterReload(entry core.Entry[K, V], oldValue V) time.Duration {
	return entry.RefreshableAfter()
}

func (c *varCreating[K, V]) RefreshAfterReloadFailure(entry core.Entry[K, V], err error) time.Duration {
	return entry.RefreshableAfter()
}

// Creating returns a Calculator that specifies that the entry should be automatically reloaded
// once the duration has elapsed after the entry's creation.
// The refresh time is not modified when the entry is updated or reloaded.
func Creating[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return CreatingFunc(func(entry core.Entry[K, V]) time.Duration {
		return duration
	})
}

// CreatingFunc returns a Calculator that specifies that the entry should be automatically reloaded
// once the duration has elapsed after the entry's creation.
// The refresh time is not modified when the entry is updated or reloaded.
func CreatingFunc[K comparable, V any](f func(entry core.Entry[K, V]) time.Duration) Calculator[K, V] {
	return &varCreating[K, V]{
		f: f,
	}
}

type varWriting[K comparable, V any] struct {
	f func(entry core.Entry[K, V]) time.Duration
}

func (w *varWriting[K, V]) RefreshAfterCreate(entry core.Entry[K, V]) time.Duration {
	return w.f(entry)
}

func (w *varWriting[K, V]) RefreshAfterUpdate(entry core.Entry[K, V], oldValue V) time.Duration {
	return w.f(entry)
}

func (w *varWriting[K, V]) RefreshAfterReload(entry core.Entry[K, V], oldValue V) time.Duration {
	return w.f(entry)
}

func (w *varWriting[K, V]) RefreshAfterReloadFailure(entry core.Entry[K, V], err error) time.Duration {
	return entry.RefreshableAfter()
}

// Writing returns a Calculator that specifies that the entry should be automatically reloaded
// once the duration has elapsed after the entry's creation or the most recent replacement of its value.
// The refresh time is not modified when the reload fails.
func Writing[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return WritingFunc(func(entry core.Entry[K, V]) time.Duration {
		return duration
	})
}

// WritingFunc returns a Calculator that specifies that the entry should be automatically reloaded
// once the duration has elapsed after the entry's creation or the most recent replacement of its value.
// The refresh time is not modified when the reload fails.
func WritingFunc[K comparable, V any](f func(entry core.Entry[K, V]) time.Duration) Calculator[K, V] {
	return &varWriting[K, V]{
		f: f,
	}
}
