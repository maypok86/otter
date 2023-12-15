package node

type Queue[K comparable, V any] struct {
	head *Node[K, V]
	tail *Node[K, V]
	len  int
}

func NewQueue[K comparable, V any]() *Queue[K, V] {
	return &Queue[K, V]{}
}

func (q *Queue[K, V]) Len() int {
	return q.len
}

func (q *Queue[K, V]) IsEmpty() bool {
	return q.Len() == 0
}

func (q *Queue[K, V]) Push(n *Node[K, V]) {
	if q.IsEmpty() {
		q.head = n
		q.tail = n
	} else {
		n.prev = q.tail
		q.tail.next = n
		q.tail = n
	}

	q.len++
}

func (q *Queue[K, V]) Pop() *Node[K, V] {
	if q.IsEmpty() {
		return nil
	}

	result := q.head
	q.Remove(result)
	return result
}

func (q *Queue[K, V]) Remove(n *Node[K, V]) {
	next := n.next
	prev := n.prev

	if prev == nil {
		if next == nil && q.head != n {
			return
		}

		q.head = next
	} else {
		prev.next = next
		n.prev = nil
	}

	if next == nil {
		q.tail = prev
	} else {
		next.prev = prev
		n.next = nil
	}

	q.len--
}

func (q *Queue[K, V]) Clear() {
	for !q.IsEmpty() {
		q.Pop()
	}
}
