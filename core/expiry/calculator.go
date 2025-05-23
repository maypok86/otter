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

package expiry

import (
	"time"

	"github.com/maypok86/otter/v2/core"
)

// Calculator calculates when cache entries expire. A single expiration time is retained so that the lifetime
// of an entry may be extended or reduced by subsequent evaluations.
type Calculator[K comparable, V any] interface {
	// ExpireAfterCreate specifies that the entry should be automatically removed from the cache once the duration has
	// elapsed after the entry's creation. To indicate no expiration, an entry may be given an
	// excessively long period.
	//
	// NOTE: ExpiresAtNano and RefreshableAtNano are not initialized at this stage.
	ExpireAfterCreate(entry core.Entry[K, V]) time.Duration
	// ExpireAfterUpdate specifies that the entry should be automatically removed from the cache once the duration has
	// elapsed after the replacement of its value. To indicate no expiration, an entry may be given an
	// excessively long period. The entry.ExpiresAfter() may be returned to not modify the expiration time.
	ExpireAfterUpdate(entry core.Entry[K, V], oldValue V) time.Duration
	// ExpireAfterRead specifies that the entry should be automatically removed from the cache once the duration has
	// elapsed after its last read. To indicate no expiration, an entry may be given an excessively
	// long period. The entry.ExpiresAfter() may be returned to not modify the expiration time.
	ExpireAfterRead(entry core.Entry[K, V]) time.Duration
}

type varCreating[K comparable, V any] struct {
	f func(entry core.Entry[K, V]) time.Duration
}

func (c *varCreating[K, V]) ExpireAfterCreate(entry core.Entry[K, V]) time.Duration {
	return c.f(entry)
}

func (c *varCreating[K, V]) ExpireAfterUpdate(entry core.Entry[K, V], oldValue V) time.Duration {
	return entry.ExpiresAfter()
}

func (c *varCreating[K, V]) ExpireAfterRead(entry core.Entry[K, V]) time.Duration {
	return entry.ExpiresAfter()
}

// Creating returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation. The expiration time is
// not modified when the entry is updated or read.
func Creating[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return CreatingFunc(func(entry core.Entry[K, V]) time.Duration {
		return duration
	})
}

// CreatingFunc returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation. The expiration time is
// not modified when the entry is updated or read.
func CreatingFunc[K comparable, V any](f func(entry core.Entry[K, V]) time.Duration) Calculator[K, V] {
	return &varCreating[K, V]{
		f: f,
	}
}

type varWriting[K comparable, V any] struct {
	f func(entry core.Entry[K, V]) time.Duration
}

func (w *varWriting[K, V]) ExpireAfterCreate(entry core.Entry[K, V]) time.Duration {
	return w.f(entry)
}

func (w *varWriting[K, V]) ExpireAfterUpdate(entry core.Entry[K, V], oldValue V) time.Duration {
	return w.f(entry)
}

func (w *varWriting[K, V]) ExpireAfterRead(entry core.Entry[K, V]) time.Duration {
	return entry.ExpiresAfter()
}

// Writing returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation or replacement of its value.
// The expiration time is not modified when the entry is read.
func Writing[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return WritingFunc(func(entry core.Entry[K, V]) time.Duration {
		return duration
	})
}

// WritingFunc returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation or replacement of its value.
// The expiration time is not modified when the entry is read.
func WritingFunc[K comparable, V any](f func(entry core.Entry[K, V]) time.Duration) Calculator[K, V] {
	return &varWriting[K, V]{
		f: f,
	}
}

type varAccessing[K comparable, V any] struct {
	f func(entry core.Entry[K, V]) time.Duration
}

func (a *varAccessing[K, V]) ExpireAfterCreate(entry core.Entry[K, V]) time.Duration {
	return a.f(entry)
}

func (a *varAccessing[K, V]) ExpireAfterUpdate(entry core.Entry[K, V], oldValue V) time.Duration {
	return a.f(entry)
}

func (a *varAccessing[K, V]) ExpireAfterRead(entry core.Entry[K, V]) time.Duration {
	return a.f(entry)
}

// Accessing returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation, replacement of its value,
// or after it was last read.
func Accessing[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return AccessingFunc(func(entry core.Entry[K, V]) time.Duration {
		return duration
	})
}

// AccessingFunc returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation, replacement of its value,
// or after it was last read.
func AccessingFunc[K comparable, V any](f func(entry core.Entry[K, V]) time.Duration) Calculator[K, V] {
	return &varAccessing[K, V]{
		f: f,
	}
}
