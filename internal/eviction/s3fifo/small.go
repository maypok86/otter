package s3fifo

import "github.com/maypok86/otter/internal/node"

type small[K comparable, V any] struct {
	q       *queue[*node.Node[K, V]]
	main    *main[K, V]
	ghost   *ghost[K, V]
	onEvict func(*node.Node[K, V])
}

func newSmall[K comparable, V any](
	capacity int,
	main *main[K, V],
	ghost *ghost[K, V],
	onEvict func(*node.Node[K, V]),
) *small[K, V] {
	return &small[K, V]{
		q:       newQueue[*node.Node[K, V]](capacity),
		main:    main,
		ghost:   ghost,
		onEvict: onEvict,
	}
}

func (s *small[K, V]) add(n *node.Node[K, V]) {
	if n.IsExpired() {
		// if expired we don't need to add
		s.onEvict(n)
		return
	}

	for {
		if s.q.tryInsert(n) {
			return
		}
		s.evict()
	}
}

func (s *small[K, V]) evict() {
	for {
		v, ok := s.q.tryRemove()
		if !ok {
			return
		}
		if v.Touched() {
			s.main.add(v)
		} else {
			if !s.ghost.add(v) {
				s.main.add(v)
			}
			return
		}
	}
}

func (s *small[K, V]) clear() {
	s.q.clear()
}

func (s *small[K, V]) length() int {
	return s.q.length()
}
