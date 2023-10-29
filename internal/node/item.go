package node

type WriteReason uint8

const (
	Added WriteReason = iota + 1
	Evicted
	Deleted
	Updated
)

type WriteItem[K comparable, V any] struct {
	n           *Node[K, V]
	writeReason WriteReason
	costDiff    uint32
}

func NewAddedItem[K comparable, V any](n *Node[K, V]) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Added,
	}
}

func NewEvictedItem[K comparable, V any](n *Node[K, V]) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Evicted,
	}
}

func NewDeletedItem[K comparable, V any](n *Node[K, V]) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Deleted,
	}
}

func NewUpdatedItem[K comparable, V any](n *Node[K, V], costDiff uint32) WriteItem[K, V] {
	return WriteItem[K, V]{
		n:           n,
		writeReason: Updated,
		costDiff:    costDiff,
	}
}

func (i *WriteItem[K, V]) GetNode() *Node[K, V] {
	return i.n
}

func (i *WriteItem[K, V]) GetCostDiff() uint32 {
	return i.costDiff
}

func (i *WriteItem[K, V]) IsAdded() bool {
	return i.writeReason == Added
}

func (i *WriteItem[K, V]) IsEvicted() bool {
	return i.writeReason == Evicted
}

func (i *WriteItem[K, V]) IsDeleted() bool {
	return i.writeReason == Deleted
}

func (i *WriteItem[K, V]) IsUpdated() bool {
	return i.writeReason == Updated
}
