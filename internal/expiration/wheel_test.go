// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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

package expiration

import (
	"slices"
	"testing"
	"time"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func getTestExp(sec int64) int64 {
	return (time.Duration(sec) * time.Second).Nanoseconds()
}

// scheduledAt returns the levels of the wheel whose buckets hold n, once per occurrence.
func scheduledAt[K comparable, V any](w *Wheel[K, V], n node.Node[K, V]) []int {
	var levels []int
	for level, bucket := range w.wheel {
		for _, root := range bucket {
			for f := w.links(root).Next; f != root; f = w.links(f).Next {
				if f == n.AsPointer() {
					levels = append(levels, level)
				}
			}
		}
	}
	return levels
}

func match[K comparable, V any](t *testing.T, nodes []node.Node[K, V], keys []K) {
	t.Helper()

	if len(nodes) != len(keys) {
		t.Fatalf("Not equals lengths of nodes (%d) and keys (%d)", len(nodes), len(keys))
	}

	for i, k := range keys {
		if k != nodes[i].Key() {
			t.Fatalf("Not valid entry found: %+v", nodes[i])
		}
	}
}

func TestWheel_Schedule(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", getTestExp(1), 0, 1),
		nm.Create("k2", "", getTestExp(69), 0, 1),
		nm.Create("k3", "", getTestExp(4399), 0, 1),
	}
	w := NewWheel(nm)

	for _, n := range nodes {
		w.Schedule(n)
	}

	for level, n := range nodes {
		if got := scheduledAt(w, n); !slices.Equal(got, []int{level}) {
			t.Fatalf("%s: scheduled at levels %v, want [%d]", n.Key(), got, level)
		}
	}
}

func TestWheel_Advance(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", now+getTestExp(1), 0, 1),
		nm.Create("k2", "", now+getTestExp(10), 0, 1),
		nm.Create("k3", "", now+getTestExp(30), 0, 1),
		nm.Create("k4", "", now+getTestExp(120), 0, 1),
		nm.Create("k5", "", now+getTestExp(6500), 0, 1),
		nm.Create("k6", "", now+getTestExp(142000), 0, 1),
		nm.Create("k7", "", now+getTestExp(1420000), 0, 1),
	}
	var expired []node.Node[string, string]
	expire := func(n node.Node[string, string], nowNanos int64) bool {
		expired = append(expired, n)
		return true
	}
	w := NewWheel(nm)
	w.time = uint64(now)

	for _, n := range nodes {
		w.Schedule(n)
	}

	keys := make([]string, 0, 7)

	w.Advance(now+getTestExp(2), expire)
	keys = append(keys, "k1")
	match(t, expired, keys)

	w.Advance(now+getTestExp(64), expire)
	keys = append(keys, "k2", "k3")
	match(t, expired, keys)

	w.Advance(now+getTestExp(121), expire)
	keys = append(keys, "k4")
	match(t, expired, keys)

	w.Advance(now+getTestExp(12000), expire)
	keys = append(keys, "k5")
	match(t, expired, keys)

	w.Advance(now+getTestExp(350000), expire)
	keys = append(keys, "k6")
	match(t, expired, keys)

	w.Advance(now+getTestExp(1520000), expire)
	keys = append(keys, "k7")
	match(t, expired, keys)
}

// Schedule of a scheduled node moves it to the bucket of its current expiration time, so that
// replaying an access or a write twice, or before the node's insertion, keeps it scheduled once.
func TestWheel_ScheduleIsIdempotent(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	w := NewWheel(nm)
	w.time = uint64(now)
	n := nm.Create("k1", "", now+getTestExp(1), 0, 1)

	w.Schedule(n)
	w.Schedule(n)
	if got := scheduledAt(w, n); !slices.Equal(got, []int{0}) {
		t.Fatalf("scheduled at levels %v, want [0]", got)
	}

	n.SetExpiresAt(now + getTestExp(4399))
	w.Schedule(n)
	if got := scheduledAt(w, n); !slices.Equal(got, []int{2}) {
		t.Fatalf("after a later deadline, scheduled at levels %v, want [2]", got)
	}
}

// Schedule of a node that is no longer alive unschedules it.
func TestWheel_ScheduleDeadNode(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	w := NewWheel(nm)
	w.time = uint64(now)
	scheduled := nm.Create("k1", "", now+getTestExp(1), 0, 1)
	unscheduled := nm.Create("k2", "", now+getTestExp(1), 0, 1)
	w.Schedule(scheduled)

	for _, n := range []node.Node[string, string]{scheduled, unscheduled} {
		n.Die()
		w.Schedule(n)
		if got := scheduledAt(w, n); len(got) != 0 {
			t.Fatalf("%s: a dead node is scheduled at levels %v", n.Key(), got)
		}
	}
}

// A node that expire keeps, because a writer extended it after the wheel found it expired, is
// scheduled again for its new expiration time.
func TestWheel_AdvanceKeepsDeclinedNode(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	w := NewWheel(nm)
	w.time = uint64(now)
	n := nm.Create("k1", "", now+getTestExp(1), 0, 1)
	w.Schedule(n)

	calls := 0
	w.Advance(now+getTestExp(2), func(n node.Node[string, string], nowNanos int64) bool {
		calls++
		n.SetExpiresAt(nowNanos + getTestExp(30))
		return false
	})
	if calls != 1 {
		t.Fatalf("expire called %d times, want 1", calls)
	}
	if got := scheduledAt(w, n); !slices.Equal(got, []int{0}) {
		t.Fatalf("kept node scheduled at levels %v, want [0]", got)
	}

	var expired []node.Node[string, string]
	w.Advance(now+getTestExp(40), func(n node.Node[string, string], nowNanos int64) bool {
		expired = append(expired, n)
		return true
	})
	match(t, expired, []string{"k1"})
}

func TestWheel_SchedulePassedDeadline(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	var expired []node.Node[string, string]
	expire := func(n node.Node[string, string], nowNanos int64) bool {
		expired = append(expired, n)
		return true
	}
	w := NewWheel(nm)
	w.time = uint64(now)

	// the deadline is behind the wheel's clock when the node is scheduled
	w.Schedule(nm.Create("k1", "", now-getTestExp(10), 0, 1))

	w.Advance(now+getTestExp(2), expire)
	match(t, expired, []string{"k1"})
}

// A node is expired by the sweep that reaches its deadline, as HasExpired reports it, and not
// kept for another bucket.
func TestWheel_AdvanceAtDeadline(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	var expired []node.Node[string, string]
	expire := func(n node.Node[string, string], nowNanos int64) bool {
		expired = append(expired, n)
		return true
	}
	w := NewWheel(nm)
	w.time = uint64(now)

	deadline := now + getTestExp(2)
	w.Schedule(nm.Create("k1", "", deadline, 0, 1))

	w.Advance(deadline, expire)
	match(t, expired, []string{"k1"})
}

func TestWheel_All(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	w := NewWheel(nm)
	w.time = uint64(now)

	want := []string{"k1", "k2", "k3", "k4", "k5"}
	exps := []int64{1, 30, 6500, 142000, 1420000}
	for i, k := range want {
		w.Schedule(nm.Create(k, "", now+getTestExp(exps[i]), 0, 1))
	}

	collect := func() []string {
		var keys []string
		for n := range w.All() {
			keys = append(keys, n.Key())
		}
		slices.Sort(keys)
		return keys
	}
	if got := collect(); !slices.Equal(got, want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}

	// removed entries are not reported
	for n := range w.All() {
		if n.Key() == "k3" {
			w.Unschedule(n)
			break
		}
	}
	if got, want := collect(), []string{"k1", "k2", "k4", "k5"}; !slices.Equal(got, want) {
		t.Fatalf("All() after Unschedule = %v, want %v", got, want)
	}
}

// A panic of expire in the middle of a bucket keeps the bucket's other nodes, and the failing
// node if it is still alive, in the wheel.
func TestWheel_AdvancePanicKeepsNodes(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	w := NewWheel(nm)
	w.time = uint64(now)
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", now+getTestExp(1), 0, 1),
		nm.Create("k2", "", now+getTestExp(1), 0, 1),
		nm.Create("k3", "", now+getTestExp(1), 0, 1),
	}
	for _, n := range nodes {
		w.Schedule(n)
	}

	var expired []node.Node[string, string]
	func() {
		defer func() {
			if r := recover(); r != "expire boom" {
				t.Fatalf("recovered %v, want the expire panic", r)
			}
		}()
		w.Advance(now+getTestExp(2), func(n node.Node[string, string], nowNanos int64) bool {
			if n.Key() == "k2" {
				panic("expire boom")
			}
			expired = append(expired, n)
			return true
		})
	}()
	match(t, expired, []string{"k1"})

	w.Advance(now+getTestExp(70), func(n node.Node[string, string], nowNanos int64) bool {
		expired = append(expired, n)
		return true
	})
	match(t, expired, []string{"k1", "k2", "k3"})
}

// A panic of expire does not advance the wheel: the next Advance sweeps the buckets the
// interrupted one did not reach, instead of skipping them until the wheel wraps around.
func TestWheel_AdvancePanicDoesNotSkipBuckets(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	w := NewWheel(nm)
	w.time = uint64(now)
	// The deadlines are two seconds apart, so the nodes are in different level-0 buckets.
	w.Schedule(nm.Create("k1", "", now+getTestExp(1), 0, 1))
	w.Schedule(nm.Create("k2", "", now+getTestExp(3), 0, 1))

	func() {
		defer func() {
			if r := recover(); r != "expire boom" {
				t.Fatalf("recovered %v, want the expire panic", r)
			}
		}()
		w.Advance(now+getTestExp(4), func(n node.Node[string, string], nowNanos int64) bool {
			panic("expire boom")
		})
	}()

	expired := make(map[string]bool)
	w.Advance(now+getTestExp(5), func(n node.Node[string, string], nowNanos int64) bool {
		expired[n.Key()] = true
		return true
	})
	if !expired["k1"] || !expired["k2"] || len(expired) != 2 {
		t.Fatalf("expired %v, want k1 and k2", expired)
	}
}
