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

package tinylfu

import (
	"github.com/maypok86/otter/v2/internal/deque"
	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

const (
	isExp = false

	// The initial percent of the maximum weighted capacity dedicated to the main space.
	percentMain = 0.99
	// PercentMainProtected is the percent of the maximum weighted capacity dedicated to the main's protected space.
	PercentMainProtected = 0.80
	// The difference in hit rates that restarts the climber.
	hillClimberRestartThreshold = 0.05
	// The percent of the total size to adapt the window by.
	hillClimberStepPercent = 0.0625
	// The rate to decrease the step size to adapt by.
	hillClimberStepDecayRate = 0.98
	// AdmitHashdosThreshold is the minimum popularity for allowing randomized admission.
	AdmitHashdosThreshold = 6
	// The maximum number of entries that can be transferred between queues.
	queueTransferThreshold = 1_000
)

type Policy[K comparable, V any] struct {
	Sketch                    *Sketch[K]
	Window                    *deque.Linked[K, V]
	Probation                 *deque.Linked[K, V]
	Protected                 *deque.Linked[K, V]
	Maximum                   uint64
	WeightedSize              uint64
	WindowMaximum             uint64
	WindowWeightedSize        uint64
	MainProtectedMaximum      uint64
	MainProtectedWeightedSize uint64
	StepSize                  float64
	Adjustment                int64
	HitsInSample              uint64
	MissesInSample            uint64
	PreviousSampleHitRate     float64
	IsWeighted                bool
	Rand                      func() uint32
}

func NewPolicy[K comparable, V any](isWeighted bool) *Policy[K, V] {
	return &Policy[K, V]{
		Sketch:     newSketch[K](),
		Window:     deque.NewLinked[K, V](isExp),
		Probation:  deque.NewLinked[K, V](isExp),
		Protected:  deque.NewLinked[K, V](isExp),
		IsWeighted: isWeighted,
		Rand:       xruntime.Fastrand,
	}
}

// Access updates the eviction policy based on node accesses.
func (p *Policy[K, V]) Access(n node.Node[K, V]) {
	p.Sketch.Increment(n.Key())
	switch {
	case n.InWindow():
		reorder(p.Window, n)
	case n.InMainProbation():
		p.reorderProbation(n)
	case n.InMainProtected():
		reorder(p.Protected, n)
	}
	p.HitsInSample++
}

// Add adds node to the eviction policy.
func (p *Policy[K, V]) Add(n node.Node[K, V], evictNode func(n node.Node[K, V], nowNanos int64)) {
	nodeWeight := uint64(n.Weight())

	p.WeightedSize += nodeWeight
	p.WindowWeightedSize += nodeWeight
	if p.WeightedSize >= p.Maximum>>1 {
		// Lazily initialize when close to the maximum
		capacity := p.Maximum
		if p.IsWeighted {
			//nolint:gosec // there's no overflow
			capacity = uint64(p.Window.Len()) + uint64(p.Probation.Len()) + uint64(p.Protected.Len())
		}
		p.Sketch.EnsureCapacity(capacity)
	}

	p.Sketch.Increment(n.Key())
	p.MissesInSample++

	// ignore out-of-order write operations
	if !n.IsAlive() {
		return
	}

	switch {
	case nodeWeight > p.Maximum:
		evictNode(n, 0)
	case nodeWeight > p.WindowMaximum:
		p.Window.PushFront(n)
	default:
		p.Window.PushBack(n)
	}
}

func (p *Policy[K, V]) Update(n, old node.Node[K, V], evictNode func(n node.Node[K, V], nowNanos int64)) {
	nodeWeight := uint64(n.Weight())
	p.updateNode(n, old)
	switch {
	case n.InWindow():
		p.WindowWeightedSize += nodeWeight
		switch {
		case nodeWeight > p.Maximum:
			evictNode(n, 0)
		case nodeWeight <= p.WindowMaximum:
			p.Access(n)
		case p.Window.Contains(n):
			p.Window.MoveToFront(n)
		}
	case n.InMainProbation():
		if nodeWeight <= p.Maximum {
			p.Access(n)
		} else {
			evictNode(n, 0)
		}
	case n.InMainProtected():
		p.MainProtectedWeightedSize += nodeWeight
		if nodeWeight <= p.Maximum {
			p.Access(n)
		} else {
			evictNode(n, 0)
		}
	}

	p.WeightedSize += nodeWeight
}

func (p *Policy[K, V]) updateNode(n, old node.Node[K, V]) {
	n.SetQueueType(old.GetQueueType())

	switch {
	case n.InWindow():
		p.Window.UpdateNode(n, old)
	case n.InMainProbation():
		p.Probation.UpdateNode(n, old)
	default:
		p.Protected.UpdateNode(n, old)
	}
	p.MakeDead(old)
}

// Delete deletes node from the eviction policy.
func (p *Policy[K, V]) Delete(n node.Node[K, V]) {
	// add may not have been processed yet
	switch {
	case n.InWindow():
		p.Window.Delete(n)
	case n.InMainProbation():
		p.Probation.Delete(n)
	default:
		p.Protected.Delete(n)
	}
	p.MakeDead(n)
}

func (p *Policy[K, V]) MakeDead(n node.Node[K, V]) {
	if !n.IsDead() {
		nodeWeight := uint64(n.Weight())
		if n.InWindow() {
			p.WindowWeightedSize -= nodeWeight
		} else if n.InMainProtected() {
			p.MainProtectedWeightedSize -= nodeWeight
		}
		p.WeightedSize -= nodeWeight
		n.Die()
	}
}

func (p *Policy[K, V]) SetMaximumSize(maximum uint64) {
	if maximum == p.Maximum {
		return
	}

	window := maximum - uint64(percentMain*float64(maximum))
	mainProtected := uint64(PercentMainProtected * float64(maximum-window))

	p.Maximum = maximum
	p.WindowMaximum = window
	p.MainProtectedMaximum = mainProtected

	p.HitsInSample = 0
	p.MissesInSample = 0
	p.StepSize = -hillClimberStepPercent * float64(maximum)

	if p.Sketch != nil && !p.IsWeighted && p.WeightedSize >= (maximum>>1) {
		// Lazily initialize when close to the maximum Size
		p.Sketch.EnsureCapacity(maximum)
	}
}

func (p *Policy[K, V]) EnsureCapacity(capacity uint64) {
	p.Sketch.EnsureCapacity(capacity)
}

// Promote the node from probation to protected on access.
func (p *Policy[K, V]) reorderProbation(n node.Node[K, V]) {
	nodeWeight := uint64(n.Weight())

	if p.Probation.NotContains(n) {
		// Ignore stale accesses for an entry that is no longer present
		return
	} else if nodeWeight > p.MainProtectedMaximum {
		reorder(p.Probation, n)
		return
	}

	// If the protected space exceeds its maximum, the LRU items are demoted to the probation space.
	// This is deferred to the adaption phase at the end of the maintenance cycle.
	p.MainProtectedWeightedSize += nodeWeight
	p.Probation.Delete(n)
	p.Protected.PushBack(n)
	n.MakeMainProtected()
}

func (p *Policy[K, V]) EvictNodes(evictNode func(n node.Node[K, V], nowNanos int64)) {
	candidate := p.EvictFromWindow()
	p.evictFromMain(candidate, evictNode)
}

func (p *Policy[K, V]) EvictFromWindow() node.Node[K, V] {
	var first node.Node[K, V]
	n := p.Window.Head()
	for p.WindowWeightedSize > p.WindowMaximum {
		// The pending operations will adjust the Size to reflect the correct weight
		if node.Equals(n, nil) {
			break
		}

		next := n.Next()
		nodeWeight := uint64(n.Weight())
		if nodeWeight != 0 {
			n.MakeMainProbation()
			p.Window.Delete(n)
			p.Probation.PushBack(n)
			if first == nil {
				first = n
			}

			p.WindowWeightedSize -= nodeWeight
		}
		n = next
	}
	return first
}

func (p *Policy[K, V]) evictFromMain(candidate node.Node[K, V], evictNode func(n node.Node[K, V], nowNanos int64)) {
	victimQueue := node.InMainProbationQueue
	candidateQueue := node.InMainProbationQueue
	victim := p.Probation.Head()
	for p.WeightedSize > p.Maximum {
		// Search the admission window for additional candidates
		if node.Equals(candidate, nil) && candidateQueue == node.InMainProbationQueue {
			candidate = p.Window.Head()
			candidateQueue = node.InWindowQueue
		}

		// Try evicting from the protected and window queues
		if node.Equals(candidate, nil) && node.Equals(victim, nil) {
			if victimQueue == node.InMainProbationQueue {
				victim = p.Protected.Head()
				victimQueue = node.InMainProtectedQueue
				continue
			} else if victimQueue == node.InMainProtectedQueue {
				victim = p.Window.Head()
				victimQueue = node.InWindowQueue
				continue
			}

			// The pending operations will adjust the Size to reflect the correct weight
			break
		}

		// Skip over entries with zero weight
		if !node.Equals(victim, nil) && victim.Weight() == 0 {
			victim = victim.Next()
			continue
		} else if !node.Equals(candidate, nil) && candidate.Weight() == 0 {
			candidate = candidate.Next()
			continue
		}

		// Evict immediately if only one of the entries is present
		if node.Equals(victim, nil) {
			previous := candidate.Next()
			evict := candidate
			candidate = previous
			evictNode(evict, 0)
			continue
		} else if node.Equals(candidate, nil) {
			evict := victim
			victim = victim.Next()
			evictNode(evict, 0)
			continue
		}

		// Evict immediately if both selected the same entry
		if node.Equals(candidate, victim) {
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
		if uint64(candidate.Weight()) > p.Maximum {
			evict := candidate
			candidate = candidate.Next()
			evictNode(evict, 0)
			continue
		}

		// Evict the entry with the lowest frequency
		if p.Admit(candidate.Key(), victim.Key()) {
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

func (p *Policy[K, V]) Admit(candidateKey, victimKey K) bool {
	victimFreq := p.Sketch.Frequency(victimKey)
	candidateFreq := p.Sketch.Frequency(candidateKey)
	if candidateFreq > victimFreq {
		return true
	}
	if candidateFreq >= AdmitHashdosThreshold {
		// The maximum frequency is 15 and halved to 7 after a reset to age the history. An attack
		// exploits that a hot candidate is rejected in favor of a hot victim. The threshold of a warm
		// candidate reduces the number of random acceptances to minimize the impact on the hit rate.
		return (p.Rand() & 127) == 0
	}
	return false
}

func (p *Policy[K, V]) Climb() {
	p.determineAdjustment()
	p.demoteFromMainProtected()
	amount := p.Adjustment
	if amount == 0 {
		return
	}
	if amount > 0 {
		p.increaseWindow()
	} else {
		p.decreaseWindow()
	}
}

func (p *Policy[K, V]) determineAdjustment() {
	if p.Sketch.IsNotInitialized() {
		p.PreviousSampleHitRate = 0.0
		p.MissesInSample = 0
		p.HitsInSample = 0
		return
	}

	requestCount := p.HitsInSample + p.MissesInSample
	if requestCount < p.Sketch.SampleSize {
		return
	}

	hitRate := float64(p.HitsInSample) / float64(requestCount)
	hitRateChange := hitRate - p.PreviousSampleHitRate
	amount := p.StepSize
	if hitRateChange < 0 {
		amount = -p.StepSize
	}
	var nextStepSize float64
	if abs(hitRateChange) >= hillClimberRestartThreshold {
		k := float64(-1)
		if amount >= 0 {
			k = float64(1)
		}
		nextStepSize = hillClimberStepPercent * float64(p.Maximum) * k
	} else {
		nextStepSize = hillClimberStepDecayRate * amount
	}
	p.PreviousSampleHitRate = hitRate
	p.Adjustment = int64(amount)
	p.StepSize = nextStepSize
	p.MissesInSample = 0
	p.HitsInSample = 0
}

func (p *Policy[K, V]) demoteFromMainProtected() {
	mainProtectedMaximum := p.MainProtectedMaximum
	mainProtectedWeightedSize := p.MainProtectedWeightedSize
	if mainProtectedWeightedSize <= mainProtectedMaximum {
		return
	}

	for i := 0; i < queueTransferThreshold; i++ {
		if mainProtectedWeightedSize <= mainProtectedMaximum {
			break
		}

		demoted := p.Protected.PopFront()
		if node.Equals(demoted, nil) {
			break
		}
		demoted.MakeMainProbation()
		p.Probation.PushBack(demoted)
		mainProtectedWeightedSize -= uint64(demoted.Weight())
	}

	p.MainProtectedWeightedSize = mainProtectedWeightedSize
}

func (p *Policy[K, V]) increaseWindow() {
	if p.MainProtectedMaximum == 0 {
		return
	}

	quota := p.Adjustment
	if p.MainProtectedMaximum < uint64(p.Adjustment) {
		quota = int64(p.MainProtectedMaximum)
	}
	p.MainProtectedMaximum -= uint64(quota)
	p.WindowMaximum += uint64(quota)
	p.demoteFromMainProtected()

	for i := 0; i < queueTransferThreshold; i++ {
		candidate := p.Probation.Head()
		probation := true
		if node.Equals(candidate, nil) || quota < int64(candidate.Weight()) {
			candidate = p.Protected.Head()
			probation = false
		}
		if node.Equals(candidate, nil) {
			break
		}

		weight := uint64(candidate.Weight())
		if quota < int64(weight) {
			break
		}

		quota -= int64(weight)
		if probation {
			p.Probation.Delete(candidate)
		} else {
			p.MainProtectedWeightedSize -= weight
			p.Protected.Delete(candidate)
		}
		p.WindowWeightedSize += weight
		p.Window.PushBack(candidate)
		candidate.MakeWindow()
	}

	p.MainProtectedMaximum += uint64(quota)
	p.WindowMaximum -= uint64(quota)
	p.Adjustment = quota
}

func (p *Policy[K, V]) decreaseWindow() {
	if p.WindowMaximum <= 1 {
		return
	}

	quota := -p.Adjustment
	windowMaximum := max(0, p.WindowMaximum-1)
	if windowMaximum < uint64(-p.Adjustment) {
		quota = int64(windowMaximum)
	}
	p.MainProtectedMaximum += uint64(quota)
	p.WindowMaximum -= uint64(quota)

	for i := 0; i < queueTransferThreshold; i++ {
		candidate := p.Window.Head()
		if node.Equals(candidate, nil) {
			break
		}

		weight := int64(candidate.Weight())
		if quota < weight {
			break
		}

		quota -= weight
		p.WindowWeightedSize -= uint64(weight)
		p.Window.Delete(candidate)
		p.Probation.PushBack(candidate)
		candidate.MakeMainProbation()
	}

	p.MainProtectedMaximum -= uint64(quota)
	p.WindowMaximum += uint64(quota)
	p.Adjustment = -quota
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
