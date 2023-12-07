package s3fifo

import (
	"sync"

	"github.com/maypok86/otter/internal/node"
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
	ghost.small = small

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
	} else if p.ghost.isGhost(n) {
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

	if p.ghost.isGhost(n) {
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

func (p *Policy[K, V]) update(deleted []*node.Node[K, V], n *node.Node[K, V], costDiff uint32) []*node.Node[K, V] {
	for p.isFull() {
		deleted = p.evict(deleted)
	}

	if n.Meta.IsDeleted() {
		return deleted
	}

	if n.Meta.IsSmall() {
		p.small.cost += costDiff
	}
	if n.Meta.IsMain() {
		p.main.cost += costDiff
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

func (p *Policy[K, V]) Write(
	deleted []*node.Node[K, V],
	expired []*node.Node[K, V],
	tasks []node.WriteTask[K, V],
) []*node.Node[K, V] {
	p.mutex.Lock()

	for _, n := range expired {
		n.Meta = n.Meta.MarkDeleted()
	}

	for _, task := range tasks {
		n := task.GetNode()

		// already deleted in map
		if task.IsEvict() || task.IsDelete() {
			n.Meta = n.Meta.MarkDeleted()
			continue
		}

		if task.IsUpdate() {
			deleted = p.update(deleted, n, task.GetCostDiff())
			continue
		}

		// add
		deleted = p.insert(deleted, n)
	}
	p.mutex.Unlock()
	return deleted
}

func (p *Policy[K, V]) MaxAvailableCost() uint32 {
	return p.maxAvailableNodeCost
}

func (p *Policy[K, V]) Clear() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.ghost.clear()
	p.main.clear()
	p.small.clear()
}
