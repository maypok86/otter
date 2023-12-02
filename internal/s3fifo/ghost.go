package s3fifo

import (
	"github.com/gammazero/deque"

	"github.com/maypok86/otter/internal/node"
)

type ghost[K comparable, V any] struct {
	q    *deque.Deque[*node.Node[K, V]]
	main *main[K, V]
}

func newGhost[K comparable, V any](main *main[K, V]) *ghost[K, V] {
	return &ghost[K, V]{
		q:    deque.New[*node.Node[K, V]](main.q.Cap()),
		main: main,
	}
}

func (g *ghost[K, V]) insert(deleted []*node.Node[K, V], n *node.Node[K, V]) []*node.Node[K, V] {
	mainLength := g.main.length()
	if mainLength == 0 {
		return deleted
	}

	for g.q.Len() >= mainLength {
		v := g.q.PopFront()
		v.Meta = v.Meta.UnmarkGhost()
		if v.Meta.IsDeleted() {
			if !v.Meta.IsSmall() && !v.Meta.IsMain() {
				// can remove
				deleted = append(deleted, v)
			}
			continue
		}

		// TODO: add new free buffer
		if !v.Meta.IsSmall() && !v.Meta.IsMain() {
			// can remove
			deleted = append(deleted, v)
			v.Meta = v.Meta.MarkDeleted()
		}
	}

	if !n.Meta.IsGhost() {
		g.q.PushBack(n)
		n.Meta = n.Meta.MarkGhost()
	}

	return deleted
}

func (g *ghost[K, V]) clear() {
	g.q.Clear()
}
