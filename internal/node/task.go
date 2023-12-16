package node

type reason uint8

const (
	addReason reason = iota + 1
	deleteReason
	updateReason
	clearReason
	closeReason
)

type WriteTask[K comparable, V any] struct {
	n           *Node[K, V]
	writeReason reason
	costDiff    uint32
}

func NewAddTask[K comparable, V any](n *Node[K, V], cost uint32) WriteTask[K, V] {
	return WriteTask[K, V]{
		n:           n,
		writeReason: addReason,
		costDiff:    cost,
	}
}

func NewDeleteTask[K comparable, V any](n *Node[K, V]) WriteTask[K, V] {
	return WriteTask[K, V]{
		n:           n,
		writeReason: deleteReason,
	}
}

func NewUpdateTask[K comparable, V any](n *Node[K, V], costDiff uint32) WriteTask[K, V] {
	return WriteTask[K, V]{
		n:           n,
		writeReason: updateReason,
		costDiff:    costDiff,
	}
}

func NewClearTask[K comparable, V any]() WriteTask[K, V] {
	return WriteTask[K, V]{
		writeReason: clearReason,
	}
}

func NewCloseTask[K comparable, V any]() WriteTask[K, V] {
	return WriteTask[K, V]{
		writeReason: closeReason,
	}
}

func (t *WriteTask[K, V]) Node() *Node[K, V] {
	return t.n
}

func (t *WriteTask[K, V]) CostDiff() uint32 {
	return t.costDiff
}

func (t *WriteTask[K, V]) IsAdd() bool {
	return t.writeReason == addReason
}

func (t *WriteTask[K, V]) IsDelete() bool {
	return t.writeReason == deleteReason
}

func (t *WriteTask[K, V]) IsUpdate() bool {
	return t.writeReason == updateReason
}

func (t *WriteTask[K, V]) IsClear() bool {
	return t.writeReason == clearReason
}

func (t *WriteTask[K, V]) IsClose() bool {
	return t.writeReason == closeReason
}
