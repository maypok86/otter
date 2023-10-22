package s3fifo

import (
	"sync"

	"github.com/maypok86/otter/internal/node"
)

const (
	maxSmallFrequency = int32(6)
	deletedFrequency  = int32(-2)
	ghostFrequency    = int32(-1)
	defaultFrequency  = int32(0)
)

type Policy[K comparable, V any] struct {
	mutex                sync.Mutex
	small                *small[K, V]
	main                 *main[K, V]
	ghost                *ghost[K, V]
	maxCost              uint32
	maxAvailableNodeCost uint32
}

func NewPolicy[K comparable, V any](maxCost uint32) *Policy[K, V] {
	smallMaxCost := maxCost / 10
	mainMaxCost := maxCost - smallMaxCost

	main := newMain[K, V](mainMaxCost)
	ghost := newGhost(main)
	small := newSmall(smallMaxCost, main, ghost)

	return &Policy[K, V]{
		small:                small,
		main:                 main,
		ghost:                ghost,
		maxCost:              maxCost,
		maxAvailableNodeCost: smallMaxCost,
	}
}

func (p *Policy[K, V]) Read(deleted, nodes []*node.Node[K, V]) []*node.Node[K, V] {
	p.mutex.Lock()
	for _, n := range nodes {
		deleted = p.read(deleted, n)
	}
	p.mutex.Unlock()
	return deleted
}

func (p *Policy[K, V]) read(deleted []*node.Node[K, V], n *node.Node[K, V]) []*node.Node[K, V] {
	if n.Meta.IsDeleted() {
		return deleted
	}

	if n.Meta.IsSmall() || n.Meta.IsMain() {
		n.Meta = n.Meta.IncrementFrequency()
	} else if n.Meta.IsGhost() {
		deleted = p.insert(deleted, n)
		n.Meta = n.Meta.ResetFrequency()
	}
	return deleted
}

func (p *Policy[K, V]) insert(deleted []*node.Node[K, V], n *node.Node[K, V]) []*node.Node[K, V] {
	for p.isFull() {
		deleted = p.evict(deleted)
	}

	if n.Meta.IsDeleted() {
		return deleted
	}

	if n.Meta.IsGhost() {
		if !n.Meta.IsMain() {
			p.main.insert(n)
		}
		return deleted
	}

	if !n.Meta.IsSmall() {
		p.small.insert(n)
	}

	return deleted
}

func (p *Policy[K, V]) update(deleted []*node.Node[K, V], item WriteItem[K, V]) []*node.Node[K, V] {
	for p.isFull() {
		deleted = p.evict(deleted)
	}

	if item.n.Meta.IsDeleted() {
		return deleted
	}

	if item.n.Meta.IsSmall() {
		p.small.cost += item.costDiff
	}
	if item.n.Meta.IsMain() {
		p.main.cost += item.costDiff
	}

	return deleted
}

func (p *Policy[K, V]) evict(deleted []*node.Node[K, V]) []*node.Node[K, V] {
	if p.small.cost >= p.maxCost/10 {
		return p.small.evict(deleted)
	}

	return p.main.evict(deleted)
}

func (p *Policy[K, V]) isFull() bool {
	return p.small.cost+p.main.cost >= p.maxCost
}

func (p *Policy[K, V]) Write(deleted []*node.Node[K, V], items []WriteItem[K, V]) []*node.Node[K, V] {
	p.mutex.Lock()
	for _, item := range items {
		// already deleted in map
		if item.writeReason == Evicted || item.writeReason == Deleted {
			item.n.Meta = item.n.Meta.MarkDeleted()
			continue
		}

		if item.writeReason == Updated {
			deleted = p.update(deleted, item)
			continue
		}

		// add
		deleted = p.insert(deleted, item.n)
	}
	p.mutex.Unlock()
	return deleted
}

func (p *Policy[K, V]) MaxAvailableCost() uint32 {
	return p.maxAvailableNodeCost
}

func (p *Policy[K, V]) Clear() {
	p.ghost.clear()
	p.main.clear()
	p.small.clear()
}
