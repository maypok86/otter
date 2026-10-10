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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewWriteKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		oldLive, isRefresh bool
		want               writeKind
	}{
		{oldLive: false, isRefresh: false, want: createWrite},
		{oldLive: false, isRefresh: true, want: createWrite},
		{oldLive: true, isRefresh: false, want: updateWrite},
		{oldLive: true, isRefresh: true, want: reloadWrite},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, newWriteKind(tt.oldLive, tt.isRefresh), "oldLive=%v isRefresh=%v", tt.oldLive, tt.isRefresh)
	}
}

// methodRecorder is an expiry and a refresh calculator that records which method was called
// and returns a duration that tells the methods apart.
type methodRecorder struct {
	method   string
	oldValue int
	err      error
}

func (r *methodRecorder) record(method string, oldValue int, err error, d time.Duration) time.Duration {
	r.method, r.oldValue, r.err = method, oldValue, err
	return d
}

func (r *methodRecorder) ExpireAfterCreate(Entry[int, int]) time.Duration {
	return r.record("ExpireAfterCreate", 0, nil, 1*time.Minute)
}

func (r *methodRecorder) ExpireAfterUpdate(_ Entry[int, int], oldValue int) time.Duration {
	return r.record("ExpireAfterUpdate", oldValue, nil, 2*time.Minute)
}

func (r *methodRecorder) ExpireAfterRead(Entry[int, int]) time.Duration {
	return r.record("ExpireAfterRead", 0, nil, 3*time.Minute)
}

func (r *methodRecorder) RefreshAfterCreate(Entry[int, int]) time.Duration {
	return r.record("RefreshAfterCreate", 0, nil, 1*time.Minute)
}

func (r *methodRecorder) RefreshAfterUpdate(_ Entry[int, int], oldValue int) time.Duration {
	return r.record("RefreshAfterUpdate", oldValue, nil, 2*time.Minute)
}

func (r *methodRecorder) RefreshAfterReload(_ Entry[int, int], oldValue int) time.Duration {
	return r.record("RefreshAfterReload", oldValue, nil, 3*time.Minute)
}

func (r *methodRecorder) RefreshAfterReloadFailure(_ Entry[int, int], err error) time.Duration {
	return r.record("RefreshAfterReloadFailure", 0, err, 4*time.Minute)
}

func TestDeadlinesAfterWrite(t *testing.T) {
	t.Parallel()

	const now = int64(time.Hour)
	errReload := errors.New("reload")
	entry := Entry[int, int]{
		Key:               1,
		Value:             2,
		ExpiresAtNano:     now + int64(time.Hour),
		RefreshableAtNano: now + int64(time.Hour),
		SnapshotAtNano:    now,
	}

	expiry := []struct {
		kind     writeKind
		method   string
		oldValue int
		after    time.Duration
	}{
		{kind: createWrite, method: "ExpireAfterCreate", after: time.Minute},
		{kind: updateWrite, method: "ExpireAfterUpdate", oldValue: 1, after: 2 * time.Minute},
		// a refresh is an update for the expiry calculator
		{kind: reloadWrite, method: "ExpireAfterUpdate", oldValue: 1, after: 2 * time.Minute},
	}
	for _, tt := range expiry {
		r := &methodRecorder{}
		got := expiresAtAfterWrite[int, int](r, tt.kind, entry, 1, now)
		require.Equal(t, tt.method, r.method, "kind %d", tt.kind)
		require.Equal(t, tt.oldValue, r.oldValue, "kind %d", tt.kind)
		require.Equal(t, now+int64(tt.after), got, "kind %d", tt.kind)
	}

	refresh := []struct {
		kind     writeKind
		method   string
		oldValue int
		err      error
		after    time.Duration
	}{
		{kind: createWrite, method: "RefreshAfterCreate", after: time.Minute},
		{kind: updateWrite, method: "RefreshAfterUpdate", oldValue: 1, after: 2 * time.Minute},
		{kind: reloadWrite, method: "RefreshAfterReload", oldValue: 1, after: 3 * time.Minute},
		{kind: reloadFailure, method: "RefreshAfterReloadFailure", err: errReload, after: 4 * time.Minute},
	}
	for _, tt := range refresh {
		r := &methodRecorder{}
		got := refreshableAtAfterWrite[int, int](r, tt.kind, entry, 1, tt.err, now)
		require.Equal(t, tt.method, r.method, "kind %d", tt.kind)
		require.Equal(t, tt.oldValue, r.oldValue, "kind %d", tt.kind)
		require.Equal(t, tt.err, r.err, "kind %d", tt.kind)
		require.Equal(t, now+int64(tt.after), got, "kind %d", tt.kind)
	}
}

func TestExpiresAtAfterRead(t *testing.T) {
	t.Parallel()

	const now = int64(time.Hour)
	r := &methodRecorder{}
	entry := Entry[int, int]{ExpiresAtNano: now + int64(time.Minute), SnapshotAtNano: now}
	got, ok := expiresAtAfterRead[int, int](r, entry, now)
	require.Equal(t, "ExpireAfterRead", r.method)
	require.True(t, ok)
	require.Equal(t, now+int64(3*time.Minute), got)
}

func TestNextDeadline(t *testing.T) {
	t.Parallel()

	const (
		now   = int64(time.Hour)
		never = time.Duration(math.MaxInt64)
	)
	tests := []struct {
		name    string
		now     int64
		current int64
		d       time.Duration
		want    int64
	}{
		{name: "a new duration", now: now, current: now + int64(time.Minute), d: time.Hour, want: now + int64(time.Hour)},
		{name: "the time left keeps the deadline", now: now, current: now + int64(time.Minute), d: time.Minute, want: now + int64(time.Minute)},
		{name: "zero keeps the deadline", now: now, current: now + int64(time.Minute), d: 0, want: now + int64(time.Minute)},
		{name: "a negative duration keeps the deadline", now: now, current: now + int64(time.Minute), d: -time.Minute, want: now + int64(time.Minute)},
		{name: "never saturates", now: now, current: now + int64(time.Minute), d: never, want: math.MaxInt64},
		{name: "never from the Unix epoch saturates", now: time.Now().UnixNano(), current: 0, d: never, want: math.MaxInt64},
		{name: "a negative clock does not overflow", now: -now, current: 0, d: never, want: -now + int64(never)},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, nextDeadline(tt.current, tt.now, tt.d), tt.name)
	}
}

func TestMoveDeadline(t *testing.T) {
	t.Parallel()

	const now = int64(time.Hour)
	tests := []struct {
		name      string
		current   int64
		d         time.Duration
		tolerance time.Duration
		want      int64
		moved     bool
	}{
		{
			name:    "a read within the tolerance keeps the deadline",
			current: now + int64(time.Minute-expireTolerance/2), d: time.Minute, tolerance: expireTolerance,
			want: now + int64(time.Minute-expireTolerance/2),
		},
		{
			name:    "a read past the tolerance moves it",
			current: now + int64(time.Minute-expireTolerance*3/2), d: time.Minute, tolerance: expireTolerance,
			want: now + int64(time.Minute), moved: true,
		},
		{
			name:    "a shorter lifetime within the tolerance keeps it",
			current: now + int64(time.Minute), d: time.Minute - expireTolerance/2, tolerance: expireTolerance,
			want: now + int64(time.Minute),
		},
		{
			// a lifetime only a little longer than the tolerance is not cut short
			name:    "a deadline almost due moves",
			current: now + int64(expireTolerance*6/10), d: expireTolerance * 3 / 2, tolerance: expireTolerance,
			want: now + int64(expireTolerance*3/2), moved: true,
		},
		{
			name:    "a lifetime within the tolerance moves on every read",
			current: now + int64(expireTolerance/4), d: expireTolerance / 2, tolerance: expireTolerance,
			want: now + int64(expireTolerance/2), moved: true,
		},
		{
			name:    "no tolerance moves it exactly",
			current: now + int64(time.Minute), d: time.Minute + expireTolerance/2,
			want: now + int64(time.Minute+expireTolerance/2), moved: true,
		},
		{
			name:    "the same deadline is not stored again",
			current: math.MaxInt64, d: time.Duration(math.MaxInt64), tolerance: expireTolerance,
			want: math.MaxInt64,
		},
		{
			name:    "a non-positive duration keeps it",
			current: now + int64(time.Minute), d: 0,
			want: now + int64(time.Minute),
		},
	}
	for _, tt := range tests {
		got, moved := moveDeadline(tt.current, now, tt.d, tt.tolerance)
		require.Equal(t, tt.want, got, tt.name)
		require.Equal(t, tt.moved, moved, tt.name)
	}
}
