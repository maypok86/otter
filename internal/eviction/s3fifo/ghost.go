package s3fifo

import "github.com/maypok86/otter/internal/node"

type ghost[K comparable, V any] struct {
	q       *queue[*node.Node[K, V]]
	onEvict func(*node.Node[K, V])
}

func newGhost[K comparable, V any](capacity int, onEvict func(*node.Node[K, V])) *ghost[K, V] {
	return &ghost[K, V]{
		q:       newQueue[*node.Node[K, V]](capacity),
		onEvict: onEvict,
	}
}

func (g *ghost[K, V]) add(n *node.Node[K, V]) bool {
	if n.IsExpired() {
		// if expired we don't need to add
		g.onEvict(n)
		return true
	}

	if !n.BecomeGhost() {
		return false
	}

	for {
		if g.q.tryInsert(n) {
			return true
		}
		g.evict()
	}
}

func (g *ghost[K, V]) evict() {
	v, ok := g.q.tryRemove()
	if ok {
		if v.IsExpired() || v.IsGhost() {
			g.onEvict(v)
		}
	}
}
