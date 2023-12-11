package s3fifo

import (
	"github.com/gammazero/deque"

	"github.com/maypok86/otter/internal/node"
)

type small[K comparable, V any] struct {
	q       *deque.Deque[*node.Node[K, V]]
	main    *main[K, V]
	ghost   *ghost[K, V]
	cost    uint32
	maxCost uint32
}

func newSmall[K comparable, V any](
	maxCost uint32,
	main *main[K, V],
	ghost *ghost[K, V],
) *small[K, V] {
	return &small[K, V]{
		q:       deque.New[*node.Node[K, V]](),
		main:    main,
		ghost:   ghost,
		maxCost: maxCost,
	}
}

func (s *small[K, V]) insert(n *node.Node[K, V]) {
	s.q.PushBack(n)
	n.Meta = n.Meta.MarkSmall()
	s.cost += n.Cost()
}

func (s *small[K, V]) evict(deleted []*node.Node[K, V]) []*node.Node[K, V] {
	if s.cost == 0 {
		return deleted
	}

	n := s.q.PopFront()
	s.cost -= n.Cost()
	n.Meta = n.Meta.UnmarkSmall()
	if n.Meta.IsDeleted() {
		return deleted
	}
	if n.IsExpired() {
		n.Meta = n.Meta.MarkDeleted()
		// can remove
		return append(deleted, n)
	}

	if n.Meta.GetFrequency() > 1 {
		s.main.insert(n)
		for s.main.isFull() {
			deleted = s.main.evict(deleted)
		}
		n.Meta = n.Meta.ResetFrequency()
		return deleted
	}

	return s.ghost.insert(deleted, n)
}

func (s *small[K, V]) length() int {
	return s.q.Len()
}

func (s *small[K, V]) clear() {
	s.q.Clear()
	s.cost = 0
}
