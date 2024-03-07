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

package expiry

import (
	"testing"

	"github.com/maypok86/otter/internal/generated/node"
	"github.com/maypok86/otter/internal/unixtime"
)

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
	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", 1, 1),
		nm.Create("k2", "", 69, 1),
		nm.Create("k3", "", 4399, 1),
	}
	v := NewVariable[string, string](nm)

	for _, n := range nodes {
		v.Add(n)
	}

	var found bool
	for _, root := range v.wheel[0] {
		if contains(root, nodes[0]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[0])
	}

	found = false
	for _, root := range v.wheel[1] {
		if contains(root, nodes[1]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[1])
	}

	found = false
	for _, root := range v.wheel[2] {
		if contains(root, nodes[2]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[2])
	}
}

func TestVariable_RemoveExpired(t *testing.T) {
	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", 1, 1),
		nm.Create("k2", "", 10, 1),
		nm.Create("k3", "", 30, 1),
		nm.Create("k4", "", 120, 1),
		nm.Create("k5", "", 6500, 1),
		nm.Create("k6", "", 142000, 1),
		nm.Create("k7", "", 1420000, 1),
	}
	v := NewVariable[string, string](nm)

	for _, n := range nodes {
		v.Add(n)
	}

	var expired []node.Node[string, string]
	var keys []string
	unixtime.SetNow(64)
	expired = v.RemoveExpired(expired)
	keys = append(keys, "k1", "k2", "k3")
	match(t, expired, keys)

	unixtime.SetNow(200)
	expired = v.RemoveExpired(expired)
	keys = append(keys, "k4")
	match(t, expired, keys)

	unixtime.SetNow(12000)
	expired = v.RemoveExpired(expired)
	keys = append(keys, "k5")
	match(t, expired, keys)

	unixtime.SetNow(350000)
	expired = v.RemoveExpired(expired)
	keys = append(keys, "k6")
	match(t, expired, keys)

	unixtime.SetNow(1520000)
	expired = v.RemoveExpired(expired)
	keys = append(keys, "k7")
	match(t, expired, keys)
}
