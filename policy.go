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
	"github.com/maypok86/otter/v2/internal/deque"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

const (
	isExp = false

	// The initial percent of the maximum weighted capacity dedicated to the main space.
	percentMain = 0.99
	// percentMainProtected is the percent of the maximum weighted capacity dedicated to the main's protected space.
	percentMainProtected = 0.80
	// The difference in hit rates that restarts the climber.
	hillClimberRestartThreshold = 0.05
	// The percent of the total size to adapt the window by.
	hillClimberStepPercent = 0.0625
	// The rate to decrease the step size to adapt by.
	hillClimberStepDecayRate = 0.98
	// admitHashdosThreshold is the minimum popularity for allowing randomized admission.
	admitHashdosThreshold = 6
	// The maximum number of entries that can be transferred between queues.
	queueTransferThreshold = 1_000
)

type policy[K comparable, V any] struct {
	sketch                    *sketch[K]
	window                    *deque.Linked[K, V]
	probation                 *deque.Linked[K, V]
	protected                 *deque.Linked[K, V]
	maximum                   uint64
	weightedSize              uint64
	windowMaximum             uint64
	windowWeightedSize        uint64
	mainProtectedMaximum      uint64
	mainProtectedWeightedSize uint64
	stepSize                  float64
	adjustment                int64
	hitsInSample              uint64
	missesInSample            uint64
	previousSampleHitRate     float64
	isWeighted                bool
	rand                      func() uint32
}

func newPolicy[K comparable, V any](isWeighted bool) *policy[K, V] {
	return &policy[K, V]{
		sketch:     newSketch[K](),
		window:     deque.NewLinked[K, V](isExp),
		probation:  deque.NewLinked[K, V](isExp),
		protected:  deque.NewLinked[K, V](isExp),
		isWeighted: isWeighted,
		rand:       xruntime.Fastrand,
	}
}

// access updates the eviction policy based on node accesses.
func (p *policy[K, V]) access(n node.Node[K, V]) {
	p.sketch.increment(n.Key())
	switch {
	case n.InWindow():
		reorder(p.window, n)
	case n.InMainProbation():
		p.reorderProbation(n)
	case n.InMainProtected():
		reorder(p.protected, n)
	}
	p.hitsInSample++
}

// add adds node to the eviction policy.
//
// A node's weight is accounted in the weighted sizes if and only if the node is linked into
// one of the queues, so that removing it subtracts exactly what was added.
func (p *policy[K, V]) add(n node.Node[K, V], evictNode func(n node.Node[K, V], nowNanos int64)) {
	// the current weight, which a writer may have changed in place since the insertion
	nodeWeight := uint64(n.Weight())
	// An out-of-order write: the node was replaced or removed before its insertion was
	// replayed. The task that replaced or removed it accounts for it.
	isAlive := n.IsAlive()

	if isAlive {
		// a new node is in the window
		n.SetPolicyWeight(uint32(nodeWeight))
		p.adjustAccounted(n, 0, nodeWeight)
	}
	if p.weightedSize >= p.maximum>>1 {
		// Lazily initialize when close to the maximum
		capacity := p.maximum
		if p.isWeighted {
			//nolint:gosec // there's no overflow
			capacity = uint64(p.window.Len()) + uint64(p.probation.Len()) + uint64(p.protected.Len())
		}
		p.sketch.ensureCapacity(capacity)
	}

	p.sketch.increment(n.Key())
	p.missesInSample++

	if !isAlive {
		return
	}

	if nodeWeight > p.windowMaximum {
		p.window.PushFront(n)
	} else {
		p.window.PushBack(n)
	}
	if nodeWeight > p.maximum {
		// Linked first: the eviction is declined if a writer has set the weight to zero in place
		// since it was read above, and the entry must then stay in the policy.
		evictNode(n, 0)
	}
}

func (p *policy[K, V]) update(n, old node.Node[K, V], evictNode func(n node.Node[K, V], nowNanos int64)) {
	if !n.IsAlive() {
		// n was replaced or removed before this update was replayed. The task that replaced or
		// removed it accounts for n, so only old, which n replaced, leaves the policy. Linking n
		// here would leave a node that is no longer in the map in a queue.
		p.makeDead(old)
		return
	}
	if !p.contains(old) {
		// The replaced node is not in the policy: its insertion was replayed after it had already
		// been replaced (add skips linking non-alive nodes), or it was evicted before this update
		// was replayed. There is nothing to swap n into, so track it as a new entry. Otherwise n
		// would stay in the map and in weightedSize while being unreachable for eviction.
		p.makeDead(old)
		p.add(n, evictNode)
		return
	}

	nodeWeight := uint64(n.Weight())
	p.updateNode(n, old)
	n.SetPolicyWeight(uint32(nodeWeight))
	p.adjustAccounted(n, 0, nodeWeight)
	switch {
	case n.InWindow():
		switch {
		case nodeWeight > p.maximum:
			evictNode(n, 0)
		case nodeWeight <= p.windowMaximum:
			p.access(n)
		case p.window.Contains(n):
			p.window.MoveToFront(n)
		}
	case n.InMainProbation():
		if nodeWeight <= p.maximum {
			p.access(n)
		} else {
			evictNode(n, 0)
		}
	case n.InMainProtected():
		if nodeWeight <= p.maximum {
			p.access(n)
		} else {
			evictNode(n, 0)
		}
	}
}

// updateNode puts n in the place of old, which must be linked, and stops accounting old.
func (p *policy[K, V]) updateNode(n, old node.Node[K, V]) {
	oldWeight := uint64(old.PolicyWeight())
	n.SetQueueType(old.GetQueueType())

	switch {
	case n.InWindow():
		p.window.UpdateNode(n, old)
	case n.InMainProbation():
		p.probation.UpdateNode(n, old)
	default:
		p.protected.UpdateNode(n, old)
	}
	// n is in old's queue now
	p.adjustAccounted(n, oldWeight, 0)
	old.Die()
}

// reweigh brings the weight accounted for n in line with the weight a writer set in place.
// Replaying it any number of times, in any order relative to n's other tasks, converges to
// n's current weight.
func (p *policy[K, V]) reweigh(n node.Node[K, V], evictNode func(n node.Node[K, V], nowNanos int64)) {
	// Not linked: its insertion has not been replayed yet (add takes the current weight), or it
	// has already left the policy. There is nothing to adjust.
	if !p.contains(n) {
		return
	}

	oldWeight := uint64(n.PolicyWeight())
	newWeight := uint64(n.Weight())
	if oldWeight == newWeight {
		return
	}
	n.SetPolicyWeight(uint32(newWeight))
	p.adjustAccounted(n, oldWeight, newWeight)

	switch {
	case newWeight > p.maximum:
		evictNode(n, 0)
	case n.InWindow() && newWeight > p.windowMaximum:
		p.window.MoveToFront(n)
	}
}

// contains reports whether n is present in the queue its type points to.
func (p *policy[K, V]) contains(n node.Node[K, V]) bool {
	switch {
	case n.InWindow():
		return p.window.Contains(n)
	case n.InMainProbation():
		return p.probation.Contains(n)
	default:
		return p.protected.Contains(n)
	}
}

// delete deletes node from the eviction policy.
func (p *policy[K, V]) delete(n node.Node[K, V]) {
	p.makeDead(n)
}

// makeDead removes n from its queue and from the weighted sizes if it is linked (its add or
// update may not have been replayed yet, in which case it was never accounted) and marks it dead.
func (p *policy[K, V]) makeDead(n node.Node[K, V]) {
	if n.IsDead() {
		return
	}
	if p.contains(n) {
		switch {
		case n.InWindow():
			p.window.Delete(n)
		case n.InMainProbation():
			p.probation.Delete(n)
		default:
			p.protected.Delete(n)
		}
		p.adjustAccounted(n, uint64(n.PolicyWeight()), 0)
	}
	n.Die()
}

// adjustAccounted replaces oldWeight with newWeight in the weighted size of the policy and
// of the queue n's type points to. It is the only place that changes these sizes when a node
// is linked, unlinked or reweighed; moves between queues adjust them where they happen.
func (p *policy[K, V]) adjustAccounted(n node.Node[K, V], oldWeight, newWeight uint64) {
	p.weightedSize = p.weightedSize - oldWeight + newWeight
	switch {
	case n.InWindow():
		p.windowWeightedSize = p.windowWeightedSize - oldWeight + newWeight
	case n.InMainProtected():
		p.mainProtectedWeightedSize = p.mainProtectedWeightedSize - oldWeight + newWeight
	}
}

func (p *policy[K, V]) setMaximumSize(maximum uint64) {
	if maximum == p.maximum {
		return
	}

	window := maximum - uint64(percentMain*float64(maximum))
	mainProtected := uint64(percentMainProtected * float64(maximum-window))

	p.maximum = maximum
	p.windowMaximum = window
	p.mainProtectedMaximum = mainProtected

	p.hitsInSample = 0
	p.missesInSample = 0
	p.stepSize = -hillClimberStepPercent * float64(maximum)

	if p.sketch != nil && !p.isWeighted && p.weightedSize >= (maximum>>1) {
		// Lazily initialize when close to the maximum size
		p.sketch.ensureCapacity(maximum)
	}
}

// Promote the node from probation to protected on access.
func (p *policy[K, V]) reorderProbation(n node.Node[K, V]) {
	nodeWeight := uint64(n.PolicyWeight())

	if p.probation.NotContains(n) {
		// Ignore stale accesses for an entry that is no longer present
		return
	} else if nodeWeight > p.mainProtectedMaximum {
		reorder(p.probation, n)
		return
	}

	// If the protected space exceeds its maximum, the LRU items are demoted to the probation space.
	// This is deferred to the adaption phase at the end of the maintenance cycle.
	p.mainProtectedWeightedSize += nodeWeight
	p.probation.Delete(n)
	p.protected.PushBack(n)
	n.MakeMainProtected()
}

func (p *policy[K, V]) evictNodes(evictNode func(n node.Node[K, V], nowNanos int64)) {
	candidate := p.evictFromWindow()
	p.evictFromMain(candidate, evictNode)
}

func (p *policy[K, V]) evictFromWindow() node.Node[K, V] {
	var first node.Node[K, V]
	n := p.window.Head()
	for p.windowWeightedSize > p.windowMaximum {
		// The pending operations will adjust the size to reflect the correct weight
		if n == nil {
			break
		}

		next := n.Next()
		nodeWeight := uint64(n.PolicyWeight())
		if nodeWeight != 0 {
			n.MakeMainProbation()
			p.window.Delete(n)
			p.probation.PushBack(n)
			if first == nil {
				first = n
			}

			p.windowWeightedSize -= nodeWeight
		}
		n = next
	}
	return first
}

func (p *policy[K, V]) evictFromMain(candidate node.Node[K, V], evictNode func(n node.Node[K, V], nowNanos int64)) {
	victimQueue := node.InMainProbationQueue
	candidateQueue := node.InMainProbationQueue
	victim := p.probation.Head()
	for p.weightedSize > p.maximum {
		// Search the admission window for additional candidates
		if candidate == nil && candidateQueue == node.InMainProbationQueue {
			candidate = p.window.Head()
			candidateQueue = node.InWindowQueue
		}

		// Try evicting from the protected and window queues
		if candidate == nil && victim == nil {
			if victimQueue == node.InMainProbationQueue {
				victim = p.protected.Head()
				victimQueue = node.InMainProtectedQueue
				continue
			} else if victimQueue == node.InMainProtectedQueue {
				victim = p.window.Head()
				victimQueue = node.InWindowQueue
				continue
			}

			// The pending operations will adjust the size to reflect the correct weight
			break
		}

		// Skip over entries with zero weight
		if victim != nil && victim.PolicyWeight() == 0 {
			victim = victim.Next()
			continue
		} else if candidate != nil && candidate.PolicyWeight() == 0 {
			candidate = candidate.Next()
			continue
		}

		// Evict immediately if only one of the entries is present
		if victim == nil {
			previous := candidate.Next()
			evict := candidate
			candidate = previous
			evictNode(evict, 0)
			continue
		} else if candidate == nil {
			evict := victim
			victim = victim.Next()
			evictNode(evict, 0)
			continue
		}

		// Evict immediately if both selected the same entry
		if candidate == victim {
			victim = victim.Next()
			evictNode(candidate, 0)
			candidate = nil
			continue
		}

		// Evict immediately if an entry was deleted
		if !victim.IsAlive() {
			evict := victim
			victim = victim.Next()
			evictNode(evict, 0)
			continue
		} else if !candidate.IsAlive() {
			evict := candidate
			candidate = candidate.Next()
			evictNode(evict, 0)
			continue
		}

		// Evict immediately if the candidate's weight exceeds the maximum
		if uint64(candidate.PolicyWeight()) > p.maximum {
			evict := candidate
			candidate = candidate.Next()
			evictNode(evict, 0)
			continue
		}

		// Evict the entry with the lowest frequency
		if p.admit(candidate.Key(), victim.Key()) {
			evict := victim
			victim = victim.Next()
			evictNode(evict, 0)
			candidate = candidate.Next()
		} else {
			evict := candidate
			candidate = candidate.Next()
			evictNode(evict, 0)
		}
	}
}

func (p *policy[K, V]) admit(candidateKey, victimKey K) bool {
	victimFreq := p.sketch.frequency(victimKey)
	candidateFreq := p.sketch.frequency(candidateKey)
	if candidateFreq > victimFreq {
		return true
	}
	if candidateFreq >= admitHashdosThreshold {
		// The maximum frequency is 15 and halved to 7 after a reset to age the history. An attack
		// exploits that a hot candidate is rejected in favor of a hot victim. The threshold of a warm
		// candidate reduces the number of random acceptances to minimize the impact on the hit rate.
		return (p.rand() & 127) == 0
	}
	return false
}

func (p *policy[K, V]) climb() {
	p.determineAdjustment()
	p.demoteFromMainProtected()
	amount := p.adjustment
	if amount == 0 {
		return
	}
	if amount > 0 {
		p.increaseWindow()
	} else {
		p.decreaseWindow()
	}
}

func (p *policy[K, V]) determineAdjustment() {
	if p.sketch.isNotInitialized() {
		p.previousSampleHitRate = 0.0
		p.missesInSample = 0
		p.hitsInSample = 0
		return
	}

	requestCount := p.hitsInSample + p.missesInSample
	if requestCount < p.sketch.sampleSize {
		return
	}

	hitRate := float64(p.hitsInSample) / float64(requestCount)
	hitRateChange := hitRate - p.previousSampleHitRate
	amount := p.stepSize
	if hitRateChange < 0 {
		amount = -p.stepSize
	}
	var nextStepSize float64
	if abs(hitRateChange) >= hillClimberRestartThreshold {
		k := float64(-1)
		if amount >= 0 {
			k = float64(1)
		}
		nextStepSize = hillClimberStepPercent * float64(p.maximum) * k
	} else {
		nextStepSize = hillClimberStepDecayRate * amount
	}
	p.previousSampleHitRate = hitRate
	p.adjustment = int64(amount)
	p.stepSize = nextStepSize
	p.missesInSample = 0
	p.hitsInSample = 0
}

func (p *policy[K, V]) demoteFromMainProtected() {
	mainProtectedMaximum := p.mainProtectedMaximum
	mainProtectedWeightedSize := p.mainProtectedWeightedSize
	if mainProtectedWeightedSize <= mainProtectedMaximum {
		return
	}

	for i := 0; i < queueTransferThreshold; i++ {
		if mainProtectedWeightedSize <= mainProtectedMaximum {
			break
		}

		demoted := p.protected.PopFront()
		if demoted == nil {
			break
		}
		demoted.MakeMainProbation()
		p.probation.PushBack(demoted)
		mainProtectedWeightedSize -= uint64(demoted.PolicyWeight())
	}

	p.mainProtectedWeightedSize = mainProtectedWeightedSize
}

func (p *policy[K, V]) increaseWindow() {
	if p.mainProtectedMaximum == 0 {
		return
	}

	quota := p.adjustment
	if p.mainProtectedMaximum < uint64(p.adjustment) {
		quota = int64(p.mainProtectedMaximum)
	}
	p.mainProtectedMaximum -= uint64(quota)
	p.windowMaximum += uint64(quota)
	p.demoteFromMainProtected()

	for i := 0; i < queueTransferThreshold; i++ {
		candidate := p.probation.Head()
		probation := true
		if candidate == nil || quota < int64(candidate.PolicyWeight()) {
			candidate = p.protected.Head()
			probation = false
		}
		if candidate == nil {
			break
		}

		weight := uint64(candidate.PolicyWeight())
		if quota < int64(weight) {
			break
		}

		quota -= int64(weight)
		if probation {
			p.probation.Delete(candidate)
		} else {
			p.mainProtectedWeightedSize -= weight
			p.protected.Delete(candidate)
		}
		p.windowWeightedSize += weight
		p.window.PushBack(candidate)
		candidate.MakeWindow()
	}

	p.mainProtectedMaximum += uint64(quota)
	p.windowMaximum -= uint64(quota)
	p.adjustment = quota
}

func (p *policy[K, V]) decreaseWindow() {
	if p.windowMaximum <= 1 {
		return
	}

	quota := -p.adjustment
	windowMaximum := max(0, p.windowMaximum-1)
	if windowMaximum < uint64(-p.adjustment) {
		quota = int64(windowMaximum)
	}
	p.mainProtectedMaximum += uint64(quota)
	p.windowMaximum -= uint64(quota)

	for i := 0; i < queueTransferThreshold; i++ {
		candidate := p.window.Head()
		if candidate == nil {
			break
		}

		weight := int64(candidate.PolicyWeight())
		if quota < weight {
			break
		}

		quota -= weight
		p.windowWeightedSize -= uint64(weight)
		p.window.Delete(candidate)
		p.probation.PushBack(candidate)
		candidate.MakeMainProbation()
	}

	p.mainProtectedMaximum -= uint64(quota)
	p.windowMaximum += uint64(quota)
	p.adjustment = -quota
}

func abs(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}

func reorder[K comparable, V any](d *deque.Linked[K, V], n node.Node[K, V]) {
	if d.Contains(n) {
		d.MoveToBack(n)
	}
}
