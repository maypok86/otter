package s3fifo

import "github.com/maypok86/otter/internal/node"

type Policy[K comparable, V any] struct {
	main     *main[K, V]
	small    *small[K, V]
	ghost    *ghost[K, V]
	capacity int
}

func NewPolicy[K comparable, V any](capacity int, onEvict func(*node.Node[K, V])) *Policy[K, V] {
	smallCapacity := capacity / 10
	mainCapacity := capacity - smallCapacity
	ghostCapacity := mainCapacity

	main := newMain[K, V](mainCapacity, onEvict)
	ghost := newGhost[K, V](ghostCapacity, onEvict)

	return &Policy[K, V]{
		main:     main,
		small:    newSmall[K, V](smallCapacity, main, ghost, onEvict),
		ghost:    ghost,
		capacity: capacity,
	}
}

func (p *Policy[K, V]) Get(n *node.Node[K, V]) {
	if !n.Touch() {
		// add to policy, but we already know that n is not ghost
		// then we can just add to small
		p.small.add(n)
	}
}

func (p *Policy[K, V]) Add(n *node.Node[K, V]) {
	if n.Revive() {
		p.main.add(n)
	} else {
		p.small.add(n)
	}
}

func (p *Policy[K, V]) Delete(n *node.Node[K, V]) {
	n.MustGhost()
}

func (p *Policy[K, V]) Clear() {
	p.ghost.clear()
	p.main.clear()
	p.small.clear()
}

func (p *Policy[K, V]) Size() int {
	return p.small.length() + p.main.length()
}

func (p *Policy[K, V]) Capacity() int {
	return p.capacity
}
