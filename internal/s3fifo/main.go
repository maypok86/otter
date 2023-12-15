package s3fifo

import (
	"github.com/maypok86/otter/internal/node"
)

const maxReinsertions = 20

type main[K comparable, V any] struct {
	q       *node.Queue[K, V]
	cost    uint32
	maxCost uint32
}

func newMain[K comparable, V any](maxCost uint32) *main[K, V] {
	return &main[K, V]{
		q:       node.NewQueue[K, V](),
		maxCost: maxCost,
	}
}

func (m *main[K, V]) insert(n *node.Node[K, V]) {
	m.q.Push(n)
	n.MarkMain()
	m.cost += n.Cost()
}

func (m *main[K, V]) evict(deleted []*node.Node[K, V]) []*node.Node[K, V] {
	reinsertions := 0
	for m.cost > 0 {
		n := m.q.Pop()

		if n.IsExpired() || n.Frequency() == 0 {
			n.Unmark()
			m.cost -= n.Cost()
			return append(deleted, n)
		}

		// to avoid the worst case O(n), we remove the 20th reinserted consecutive element.
		reinsertions++
		if reinsertions >= maxReinsertions {
			n.Unmark()
			m.cost -= n.Cost()
			return append(deleted, n)
		}

		m.q.Push(n)
		n.DecrementFrequency()
	}
	return deleted
}

func (m *main[K, V]) remove(n *node.Node[K, V]) {
	m.cost -= n.Cost()
	n.Unmark()
	m.q.Remove(n)
}

func (m *main[K, V]) length() int {
	return m.q.Len()
}

func (m *main[K, V]) clear() {
	m.q.Clear()
	m.cost = 0
}

func (m *main[K, V]) isFull() bool {
	return m.cost >= m.maxCost
}
