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
	"slices"
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

// newTestPolicy returns a weighted policy with the given maximum that evicts through evict.
func newTestPolicy(maximum uint64, evict func(n node.Node[int, int]) bool) *policy[int, int] {
	p := newPolicy[int, int](true, 0, evict)
	p.setMaximumSize(maximum)
	return p
}

// removeAll is an evict callback for a cache that removes every node the policy evicts.
func removeAll(node.Node[int, int]) bool { return true }

// requireAccounted checks that exactly the given nodes are linked into the policy and that
// the weighted sizes are the sums of their weights.
func requireAccounted(t *testing.T, p *policy[int, int], want ...node.Node[int, int]) {
	t.Helper()

	linked := make([]int, 0, p.window.Len()+p.probation.Len()+p.protected.Len())
	var weighted, window, protected uint64
	for n := range p.window.All() {
		linked = append(linked, n.Key())
		window += uint64(n.PolicyWeight())
	}
	for n := range p.probation.All() {
		linked = append(linked, n.Key())
	}
	for n := range p.protected.All() {
		linked = append(linked, n.Key())
		protected += uint64(n.PolicyWeight())
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
			p := newTestPolicy(100, removeAll)
			n1 := nm.Create(1, 1, 0, 0, 3)
			p.add(n1)
			if inProtected {
				moveToProtected(p, n1)
			}

			// Set(1) replaces n1 with n2, Invalidate(1) removes n2, and the invalidation's task
			// is replayed first.
			n2 := nm.Create(1, 2, 0, 0, 5)
			n1.Retire()
			n2.Retire()
			p.makeDead(n2)
			p.update(n2, n1, false)

			requireAccounted(t, p)
			require.True(t, n1.IsDead())
			require.True(t, n2.IsDead())
		}
	})
	t.Run("update of the replacement before its own update", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n1 := nm.Create(1, 1, 0, 0, 3)
		p.add(n1)

		n2 := nm.Create(1, 2, 0, 0, 5)
		n3 := nm.Create(1, 3, 0, 0, 7)
		n1.Retire()
		n2.Retire()
		p.update(n3, n2, false)
		p.update(n2, n1, false)

		requireAccounted(t, p, n3)
		require.True(t, n1.IsDead())
		require.True(t, n2.IsDead())
	})
	t.Run("insertion replayed after its replacement", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n1 := nm.Create(1, 1, 0, 0, 3)
		n2 := nm.Create(1, 2, 0, 0, 5)
		n1.Retire()
		p.update(n2, n1, false)
		p.add(n1)

		requireAccounted(t, p, n2)
	})
	t.Run("oversized insertion", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n := nm.Create(1, 1, 0, 0, 1000)
		p.add(n)

		requireAccounted(t, p)
		require.True(t, n.IsDead())
	})
}

// reweigh applies a weight changed in place, whatever the order in which it is replayed
// relative to the node's other tasks.
func TestPolicy_Reweigh(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithWeight: true})

	t.Run("in the window", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n := nm.Create(1, 1, 0, 0, 3)
		p.add(n)

		n.SetWeight(7)
		p.reweigh(n)
		requireAccounted(t, p, n)

		// replaying it again changes nothing
		p.reweigh(n)
		requireAccounted(t, p, n)
	})
	t.Run("in the protected queue", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n := nm.Create(1, 1, 0, 0, 3)
		p.add(n)
		p.window.Delete(n)
		p.windowWeightedSize -= uint64(n.PolicyWeight())
		n.MakeMainProtected()
		p.protected.PushBack(n)
		p.mainProtectedWeightedSize += uint64(n.PolicyWeight())

		n.SetWeight(1)
		p.reweigh(n)
		requireAccounted(t, p, n)
	})
	t.Run("before its insertion is replayed", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n := nm.Create(1, 1, 0, 0, 3)
		n.SetWeight(8)
		p.reweigh(n)
		requireAccounted(t, p)

		p.add(n) // takes the current weight
		requireAccounted(t, p, n)
	})
	t.Run("after its removal", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n := nm.Create(1, 1, 0, 0, 3)
		p.add(n)
		n.Retire()
		p.makeDead(n)

		n.SetWeight(8)
		p.reweigh(n)
		requireAccounted(t, p)
	})
	t.Run("above the maximum", func(t *testing.T) {
		t.Parallel()

		p := newTestPolicy(100, removeAll)
		n := nm.Create(1, 1, 0, 0, 3)
		p.add(n)

		n.SetWeight(1000)
		p.reweigh(n)
		requireAccounted(t, p)
		require.True(t, n.IsDead())
	})
}

// A node the cache keeps when the policy evicts it stays linked and accounted, and the policy moves
// on to other victims; a node the cache removes is unlinked by the policy.
func TestPolicy_EvictionKeptByTheCache(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithWeight: true})
	for _, keep := range []bool{false, true} {
		var asked []int
		p := newTestPolicy(2, func(n node.Node[int, int]) bool {
			asked = append(asked, n.Key())
			return !keep
		})
		nodes := []node.Node[int, int]{
			nm.Create(1, 1, 0, 0, 1),
			nm.Create(2, 2, 0, 0, 1),
			nm.Create(3, 3, 0, 0, 1),
		}
		for _, n := range nodes {
			p.add(n)
		}

		p.evictNodes()
		if keep {
			// every node was offered (a window node can be both a candidate and a victim), and
			// the walk ended when the queues ran out
			slices.Sort(asked)
			require.Equal(t, []int{1, 2, 3}, slices.Compact(asked))
			requireAccounted(t, p, nodes...)
			for _, n := range nodes {
				require.False(t, n.IsDead(), "key %d", n.Key())
			}
			continue
		}
		require.Len(t, asked, 1)
		evicted := nodes[asked[0]-1]
		require.True(t, evicted.IsDead())
		var rest []node.Node[int, int]
		for _, n := range nodes {
			if n != evicted {
				rest = append(rest, n)
			}
		}
		requireAccounted(t, p, rest...)
	}
}

// A new maximum starts the hill climber over. The quota that climb could not move yet and the
// hit rate of the last sample were measured for the previous maximum: applied to the new one, a
// quota left over from a large maximum takes the whole protected region of a small one, or
// shrinks its window to a single entry.
func TestPolicy_SetMaximumSizeRestartsClimber(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithSize: true})
	for _, adjustment := range []int64{5250, -5250} {
		p := newPolicy[int, int](false, 100_000, removeAll)
		p.setMaximumSize(100_000)
		for k := range 1000 {
			p.add(nm.Create(k, k, 0, 0, 1))
		}
		p.evictNodes()
		// what climb leaves when increaseWindow or decreaseWindow has not moved all of its quota
		p.adjustment = adjustment
		p.previousSampleHitRate = 0.9

		p.setMaximumSize(1000)
		window, protected := p.windowMaximum, p.mainProtectedMaximum
		p.evictNodes()
		p.climb()

		require.Equal(t, window, p.windowMaximum, "adjustment %d", adjustment)
		require.Equal(t, protected, p.mainProtectedMaximum, "adjustment %d", adjustment)
		require.Zero(t, p.previousSampleHitRate, "adjustment %d", adjustment)
	}
}

func TestPolicy_ClimberWaitsForHalfFull(t *testing.T) {
	t.Parallel()

	// An InitialCapacity hint creates the sketch at construction, which used to release the
	// climber while the cache was far from evicting.
	for _, hint := range []int{0, 1000} {
		c := Must(&Options[int, int]{MaximumSize: 400_000, InitialCapacity: hint})
		ci := c.cache
		ci.evictionMutex.Lock()
		window := ci.evictionPolicy.windowMaximum
		ci.evictionMutex.Unlock()

		for k := range 40_000 {
			c.Set(k, k)
			if k%1000 == 0 {
				c.CleanUp()
			}
		}
		c.CleanUp()

		ci.evictionMutex.Lock()
		require.Equal(t, window, ci.evictionPolicy.windowMaximum, "InitialCapacity %d", hint)
		ci.evictionMutex.Unlock()
	}
}

// An update that makes an entry heavier than the maximum evicts it, whichever queue it is in.
func TestPolicy_UpdateAboveTheMaximum(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithWeight: true})
	for _, queue := range []string{"window", "probation", "protected"} {
		p := newTestPolicy(100, removeAll)
		n1 := nm.Create(1, 1, 0, 0, 3)
		p.add(n1)
		if queue != "window" {
			p.window.Delete(n1)
			p.windowWeightedSize -= uint64(n1.PolicyWeight())
			if queue == "probation" {
				n1.MakeMainProbation()
				p.probation.PushBack(n1)
			} else {
				n1.MakeMainProtected()
				p.protected.PushBack(n1)
				p.mainProtectedWeightedSize += uint64(n1.PolicyWeight())
			}
		}

		n2 := nm.Create(1, 2, 0, 0, 1000)
		n1.Retire()
		p.update(n2, n1, false)

		requireAccounted(t, p)
		require.True(t, n1.IsDead(), queue)
		require.True(t, n2.IsDead(), queue)
	}
}

// Entries of zero weight are never victims: they free nothing. A probation queue that holds only
// such entries, which decreaseWindow can move there or a writer can reweigh in place, leaves the
// candidates without a victim, and the candidates are evicted instead.
func TestPolicy_EvictsCandidatesWithoutVictims(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithWeight: true})
	p := newTestPolicy(10, removeAll)
	zero := nm.Create(0, 0, 0, 0, 0)
	p.add(zero)
	p.window.Delete(zero)
	zero.MakeMainProbation()
	p.probation.PushBack(zero)
	nodes := []node.Node[int, int]{
		nm.Create(1, 1, 0, 0, 5),
		nm.Create(2, 2, 0, 0, 5),
		nm.Create(3, 3, 0, 0, 5),
	}
	for _, n := range nodes {
		p.add(n)
	}
	candidate := p.window.Head()

	p.evictFromMain(nil)

	require.True(t, candidate.IsDead())
	rest := []node.Node[int, int]{zero}
	for _, n := range nodes {
		if n != candidate {
			rest = append(rest, n)
		}
	}
	requireAccounted(t, p, rest...)
}

// The climber grows the window by no more than the protected region's maximum, and once that
// region has nothing left to give, a further increase changes nothing.
func TestPolicy_IncreaseWindowUpToTheProtectedMaximum(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithSize: true})
	p := newPolicy[int, int](false, 0, removeAll)
	p.setMaximumSize(100)
	for k := range 80 {
		p.add(nm.Create(k, k, 0, 0, 1))
	}
	p.evictNodes()
	require.Equal(t, uint64(1), p.windowMaximum)
	require.Equal(t, uint64(79), p.mainProtectedMaximum)

	p.adjustment = 1000
	p.increaseWindow()
	require.Equal(t, uint64(80), p.windowMaximum)
	require.Zero(t, p.mainProtectedMaximum)
	require.Equal(t, 80, p.window.Len())
	require.Equal(t, uint64(80), p.windowWeightedSize)

	p.adjustment = 10
	p.increaseWindow()
	require.Equal(t, uint64(80), p.windowMaximum)
	require.Zero(t, p.mainProtectedMaximum)
	require.Equal(t, 80, p.window.Len())
}
