package s3fifo

import (
	"github.com/gammazero/deque"

	"github.com/maypok86/otter/internal/node"
)

const maxReinsertions = 20

type main[K comparable, V any] struct {
	q       *deque.Deque[*node.Node[K, V]]
	cost    uint32
	maxCost uint32
}

func newMain[K comparable, V any](maxCost uint32) *main[K, V] {
	return &main[K, V]{
		q:       deque.New[*node.Node[K, V]](),
		maxCost: maxCost,
	}
}

func (m *main[K, V]) insert(n *node.Node[K, V]) {
	m.q.PushBack(n)
	n.Meta = n.Meta.MarkMain()
	m.cost += n.Cost()
}

func (m *main[K, V]) evict(deleted []*node.Node[K, V]) []*node.Node[K, V] {
	reinsertions := 0
	for m.cost > 0 {
		n := m.q.PopFront()
		if n.Meta.IsDeleted() {
			n.Meta = n.Meta.UnmarkMain()
			m.cost -= n.Cost()
			return deleted
		}

		if n.IsExpired() || n.Meta.GetFrequency() == 0 {
			n.Meta = n.Meta.UnmarkMain().MarkDeleted()
			m.cost -= n.Cost()
			// can remove
			return append(deleted, n)
		}

		// to avoid the worst case O(n), we remove the 20th reinserted consecutive element.
		reinsertions++
		if reinsertions >= maxReinsertions {
			n.Meta = n.Meta.UnmarkMain().MarkDeleted()
			m.cost -= n.Cost()
			// can remove
			return append(deleted, n)
		}

		m.q.PushBack(n)
		n.Meta = n.Meta.DecrementFrequency()
	}
	return deleted
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
