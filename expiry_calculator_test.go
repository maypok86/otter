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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVarExpiryCreating(t *testing.T) {
	t.Parallel()

	oldValue := 3
	e := Entry[int, int]{
		Key:               1,
		Value:             2,
		Weight:            1,
		ExpiresAtNano:     100,
		RefreshableAtNano: 0,
		SnapshotAtNano:    50,
	}
	c := ExpiryCreatingFunc(func(entry Entry[int, int]) time.Duration {
		return time.Duration(5 * int64(entry.Key) * entry.SnapshotAtNano)
	})
	require.Equal(t, time.Duration(5*int64(e.Key)*e.SnapshotAtNano), c.ExpireAfterCreate(e))
	require.Equal(t, e.ExpiresAfter(), c.ExpireAfterUpdate(e, oldValue))
	require.Equal(t, e.ExpiresAfter(), c.ExpireAfterRead(e))
}

func TestVarExpiryWriting(t *testing.T) {
	t.Parallel()

	oldValue := 3
	e := Entry[int, int]{
		Key:               1,
		Value:             2,
		Weight:            1,
		ExpiresAtNano:     100,
		RefreshableAtNano: 0,
		SnapshotAtNano:    50,
	}
	c := ExpiryWritingFunc(func(entry Entry[int, int]) time.Duration {
		return time.Duration(5 * int64(entry.Key) * entry.SnapshotAtNano)
	})
	require.Equal(t, time.Duration(5*int64(e.Key)*e.SnapshotAtNano), c.ExpireAfterCreate(e))
	require.Equal(t, time.Duration(5*int64(e.Key)*e.SnapshotAtNano), c.ExpireAfterUpdate(e, oldValue))
	require.Equal(t, e.ExpiresAfter(), c.ExpireAfterRead(e))
}

func TestVarExpiryAccessing(t *testing.T) {
	t.Parallel()

	oldValue := 3
	e := Entry[int, int]{
		Key:               1,
		Value:             2,
		Weight:            1,
		ExpiresAtNano:     100,
		RefreshableAtNano: 0,
		SnapshotAtNano:    50,
	}
	c := ExpiryAccessingFunc(func(entry Entry[int, int]) time.Duration {
		return time.Duration(5 * int64(entry.Key) * entry.SnapshotAtNano)
	})
	require.Equal(t, time.Duration(5*int64(e.Key)*e.SnapshotAtNano), c.ExpireAfterCreate(e))
	require.Equal(t, time.Duration(5*int64(e.Key)*e.SnapshotAtNano), c.ExpireAfterUpdate(e, oldValue))
	require.Equal(t, time.Duration(5*int64(e.Key)*e.SnapshotAtNano), c.ExpireAfterRead(e))
}
