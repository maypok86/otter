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

func contains[K comparable, V any](root, f node.Node[K, V]) bool {
	n := root.NextExp()
	for !node.Equals(n, root) {
		if node.Equals(n, f) {
			return true
		}

		n = n.NextExp()
	}
	return false
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

func TestVariable_Add(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", getTestExp(1), 0, 1),
		nm.Create("k2", "", getTestExp(69), 0, 1),
		nm.Create("k3", "", getTestExp(4399), 0, 1),
	}
	v := NewVariable(nm)

	for _, n := range nodes {
		v.Add(n)
	}

	var found bool
	for _, root := range v.wheel[0] {
		if contains(root, nodes[0]) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[0])
	}

	found = false
	for _, root := range v.wheel[1] {
		if contains(root, nodes[1]) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[1])
	}

	found = false
	for _, root := range v.wheel[2] {
		if contains(root, nodes[2]) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[2])
	}
}

func TestVariable_DeleteExpired(t *testing.T) {
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
	expireNode := func(n node.Node[string, string], nowNanos int64) {
		expired = append(expired, n)
	}
	v := NewVariable(nm)
	v.time = uint64(now)

	for _, n := range nodes {
		v.Add(n)
	}

	keys := make([]string, 0, 7)

	v.DeleteExpired(now+getTestExp(2), expireNode)
	keys = append(keys, "k1")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(64), expireNode)
	keys = append(keys, "k2", "k3")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(121), expireNode)
	keys = append(keys, "k4")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(12000), expireNode)
	keys = append(keys, "k5")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(350000), expireNode)
	keys = append(keys, "k6")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(1520000), expireNode)
	keys = append(keys, "k7")
	match(t, expired, keys)
}

func TestVariable_All(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	v := NewVariable(nm)
	v.time = uint64(now)

	want := []string{"k1", "k2", "k3", "k4", "k5"}
	exps := []int64{1, 30, 6500, 142000, 1420000}
	for i, k := range want {
		v.Add(nm.Create(k, "", now+getTestExp(exps[i]), 0, 1))
	}

	collect := func() []string {
		var keys []string
		for n := range v.All() {
			keys = append(keys, n.Key())
		}
		slices.Sort(keys)
		return keys
	}
	if got := collect(); !slices.Equal(got, want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}

	// removed entries are not reported
	for n := range v.All() {
		if n.Key() == "k3" {
			v.Delete(n)
			break
		}
	}
	if got, want := collect(), []string{"k1", "k2", "k4", "k5"}; !slices.Equal(got, want) {
		t.Fatalf("All() after Delete = %v, want %v", got, want)
	}
}

// A panic of expireNode in the middle of a bucket keeps the bucket's other nodes, and the failing
// node if it is still alive, in the wheel.
func TestVariable_DeleteExpiredPanicKeepsNodes(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	v := NewVariable(nm)
	v.time = uint64(now)
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", now+getTestExp(1), 0, 1),
		nm.Create("k2", "", now+getTestExp(1), 0, 1),
		nm.Create("k3", "", now+getTestExp(1), 0, 1),
	}
	for _, n := range nodes {
		v.Add(n)
	}

	var expired []node.Node[string, string]
	func() {
		defer func() {
			if r := recover(); r != "expire boom" {
				t.Fatalf("recovered %v, want the expireNode panic", r)
			}
		}()
		v.DeleteExpired(now+getTestExp(2), func(n node.Node[string, string], nowNanos int64) {
			if n.Key() == "k2" {
				panic("expire boom")
			}
			expired = append(expired, n)
		})
	}()
	match(t, expired, []string{"k1"})

	v.DeleteExpired(now+getTestExp(70), func(n node.Node[string, string], nowNanos int64) {
		expired = append(expired, n)
	})
	match(t, expired, []string{"k1", "k2", "k3"})
}

// A panic of expireNode does not advance the wheel: the next DeleteExpired sweeps the buckets the
// interrupted one did not reach, instead of skipping them until the wheel wraps around.
func TestVariable_DeleteExpiredPanicDoesNotSkipBuckets(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	v := NewVariable(nm)
	v.time = uint64(now)
	// The deadlines are two seconds apart, so the nodes are in different level-0 buckets.
	v.Add(nm.Create("k1", "", now+getTestExp(1), 0, 1))
	v.Add(nm.Create("k2", "", now+getTestExp(3), 0, 1))

	func() {
		defer func() {
			if r := recover(); r != "expire boom" {
				t.Fatalf("recovered %v, want the expireNode panic", r)
			}
		}()
		v.DeleteExpired(now+getTestExp(4), func(n node.Node[string, string], nowNanos int64) {
			panic("expire boom")
		})
	}()

	expired := make(map[string]bool)
	v.DeleteExpired(now+getTestExp(5), func(n node.Node[string, string], nowNanos int64) {
		expired[n.Key()] = true
	})
	if !expired["k1"] || !expired["k2"] || len(expired) != 2 {
		t.Fatalf("expired %v, want k1 and k2", expired)
	}
}
