package s3fifo

import (
	"github.com/maypok86/otter/internal/node"
)

type Policy[K comparable, V any] struct {
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

func (p *Policy[K, V]) Read(nodes []*node.Node[K, V]) {
	for _, n := range nodes {
		n.IncrementFrequency()
	}
}

func (p *Policy[K, V]) insert(deleted []*node.Node[K, V], n *node.Node[K, V]) []*node.Node[K, V] {
	if p.ghost.isGhost(n) {
		p.main.insert(n)
		n.ResetFrequency()
	} else {
		p.small.insert(n)
	}

	for p.isFull() {
		deleted = p.evict(deleted)
	}

	return deleted
}

func (p *Policy[K, V]) update(deleted []*node.Node[K, V], n *node.Node[K, V], costDiff uint32) []*node.Node[K, V] {
	if n.IsSmall() {
		p.small.cost += costDiff
	} else if n.IsMain() {
		p.main.cost += costDiff
	}

	for p.isFull() {
		deleted = p.evict(deleted)
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
	tasks []node.WriteTask[K, V],
) []*node.Node[K, V] {
	for _, task := range tasks {
		n := task.GetNode()

		// already deleted in map
		if task.IsDelete() {
			p.delete(task.GetNode())
			continue
		}

		if task.IsUpdate() {
			deleted = p.update(deleted, n, task.GetCostDiff())
			continue
		}

		// add
		deleted = p.insert(deleted, n)
	}
	return deleted
}

func (p *Policy[K, V]) Delete(buffer []*node.Node[K, V]) {
	for _, n := range buffer {
		p.delete(n)
	}
}

func (p *Policy[K, V]) delete(n *node.Node[K, V]) {
	if n.IsSmall() {
		p.small.remove(n)
		return
	}

	if n.IsMain() {
		p.main.remove(n)
	}
}

func (p *Policy[K, V]) MaxAvailableCost() uint32 {
	return p.maxAvailableNodeCost
}

func (p *Policy[K, V]) Clear() {
	p.ghost.clear()
	p.main.clear()
	p.small.clear()
}
