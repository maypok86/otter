package s3fifo

import "github.com/maypok86/otter/internal/node"

type WriteReason uint8

const (
	Added WriteReason = iota + 1
	Evicted
	Deleted
	Updated
)

type WriteItem[K comparable, V any] struct {
	n           *node.Node[K, V]
	writeReason WriteReason
	costDiff    uint32
}

func NewAddedItem[K comparable, V any](n *node.Node[K, V]) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Added,
	}
}

func NewEvictedItem[K comparable, V any](n *node.Node[K, V]) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Evicted,
	}
}

func NewDeletedItem[K comparable, V any](n *node.Node[K, V]) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Deleted,
	}
}

func NewUpdatedItem[K comparable, V any](n *node.Node[K, V], costDiff uint32) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Updated,
		costDiff:    costDiff,
	}
}
