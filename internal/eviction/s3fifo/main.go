package s3fifo

import "github.com/maypok86/otter/internal/node"

type main[K comparable, V any] struct {
	q       *queue[*node.Node[K, V]]
	onEvict func(*node.Node[K, V])
}

func newMain[K comparable, V any](capacity int, onEvict func(*node.Node[K, V])) *main[K, V] {
	return &main[K, V]{
		q:       newQueue[*node.Node[K, V]](capacity),
		onEvict: onEvict,
	}
}

// we can add to main any node.
func (m *main[K, V]) add(n *node.Node[K, V]) {
	if n.IsExpired() {
		// if expired we don't need to add.
		m.onEvict(n)
		return
	}

	for {
		if m.q.tryInsert(n) {
			return
		}
		m.evict()
	}
}

func (m *main[K, V]) evict() {
	for {
		v, ok := m.q.tryRemove()
		if !ok {
			return
		}

		if v.Untouch() {
			m.add(v)
		} else {
			// delete from cache.
			m.onEvict(v)
			return
		}
	}
}
