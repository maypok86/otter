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

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func TestPolicy_SetMaximumSize(t *testing.T) {
	t.Parallel()

	p := &policy[int, int]{}
	p.maximum = 10
	require.NotPanics(t, func() {
		p.setMaximumSize(10)
	})
}

// requireAccounted checks that exactly the given nodes are linked into the policy and that
// the weighted sizes are the sums of their weights.
func requireAccounted(t *testing.T, p *policy[int, int], want ...node.Node[int, int]) {
	t.Helper()

	linked := make([]int, 0, p.window.Len()+p.probation.Len()+p.protected.Len())
	var weighted, window, protected uint64
	for n := range p.window.All() {
		linked = append(linked, n.Key())
		window += uint64(n.Weight())
	}
	for n := range p.probation.All() {
		linked = append(linked, n.Key())
	}
	for n := range p.protected.All() {
		linked = append(linked, n.Key())
		protected += uint64(n.Weight())
	}
	wantKeys := make([]int, 0, len(want))
	for _, n := range want {
		wantKeys = append(wantKeys, n.Key())
		weighted += uint64(n.Weight())
	}
	require.ElementsMatch(t, wantKeys, linked, "linked nodes")
	require.Equal(t, weighted, p.weightedSize, "weightedSize")
	require.Equal(t, window, p.windowWeightedSize, "windowWeightedSize")
	require.Equal(t, protected, p.mainProtectedWeightedSize, "mainProtectedWeightedSize")
}

// The tasks of one key reach the policy in the order they were pushed to the write buffer,
// which can differ from the order of the writes: a writer pushes its task after leaving the
// hash table's lock. Every order must leave the policy consistent.
func TestPolicy_OutOfOrderTasks(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithWeight: true})
	newPolicyWithMaximum := func() *policy[int, int] {
		p := newPolicy[int, int](true)
		p.setMaximumSize(100)
		return p
	}
	// what the cache's evictNode does to the policy
	evictNode := func(p *policy[int, int]) func(n node.Node[int, int], nowNanos int64) {
		return func(n node.Node[int, int], nowNanos int64) {
			p.delete(n)
		}
	}
	moveToProtected := func(p *policy[int, int], n node.Node[int, int]) {
		p.window.Delete(n)
		p.windowWeightedSize -= uint64(n.Weight())
		n.MakeMainProtected()
		p.protected.PushBack(n)
		p.mainProtectedWeightedSize += uint64(n.Weight())
	}

	t.Run("delete of the replacement before its update", func(t *testing.T) {
		t.Parallel()

		for _, inProtected := range []bool{false, true} {
			p := newPolicyWithMaximum()
			n1 := nm.Create(1, 1, 0, 0, 3)
			p.add(n1, evictNode(p))
			if inProtected {
				moveToProtected(p, n1)
			}

			// Set(1) replaces n1 with n2, Invalidate(1) removes n2, and the invalidation's task
			// is replayed first.
			n2 := nm.Create(1, 2, 0, 0, 5)
			n1.Retire()
			n2.Retire()
			p.delete(n2)
			p.update(n2, n1, evictNode(p))

			requireAccounted(t, p)
			require.True(t, n1.IsDead())
			require.True(t, n2.IsDead())
		}
	})
	t.Run("update of the replacement before its own update", func(t *testing.T) {
		t.Parallel()

		p := newPolicyWithMaximum()
		n1 := nm.Create(1, 1, 0, 0, 3)
		p.add(n1, evictNode(p))

		n2 := nm.Create(1, 2, 0, 0, 5)
		n3 := nm.Create(1, 3, 0, 0, 7)
		n1.Retire()
		n2.Retire()
		p.update(n3, n2, evictNode(p))
		p.update(n2, n1, evictNode(p))

		requireAccounted(t, p, n3)
		require.True(t, n1.IsDead())
		require.True(t, n2.IsDead())
	})
	t.Run("insertion replayed after its replacement", func(t *testing.T) {
		t.Parallel()

		p := newPolicyWithMaximum()
		n1 := nm.Create(1, 1, 0, 0, 3)
		n2 := nm.Create(1, 2, 0, 0, 5)
		n1.Retire()
		p.update(n2, n1, evictNode(p))
		p.add(n1, evictNode(p))

		requireAccounted(t, p, n2)
	})
	t.Run("oversized insertion", func(t *testing.T) {
		t.Parallel()

		p := newPolicyWithMaximum()
		n := nm.Create(1, 1, 0, 0, 1000)
		p.add(n, evictNode(p))

		requireAccounted(t, p)
		require.True(t, n.IsDead())
	})
}
