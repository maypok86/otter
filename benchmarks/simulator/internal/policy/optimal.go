package policy

import (
	"container/heap"
	"math"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type Optimal[K comparable, V any] struct {
	capacity uint64
	next     map[K][]int
	access   []K
}

func (o *Optimal[K, V]) Init(capacity int) {
	o.capacity = uint64(capacity)
	o.next = make(map[K][]int)
	o.access = make([]K, 0)
}

func (o *Optimal[K, V]) Get(key K) (V, bool) {
	var v V
	return v, false
}

func (o *Optimal[K, V]) Set(key K, value V) {
}

func (o *Optimal[K, V]) Record(e event.AccessEvent) {
	key := any(e.Key()).(K)
	o.next[key] = append(o.next[key], len(o.access))
	o.access = append(o.access, key)
}

func (o *Optimal[K, V]) Ratio() float64 {
	hits := uint64(0)
	misses := uint64(0)
	look := make(map[K]*optimalItem[K], o.capacity)
	data := &optimalHeap[K]{}
	heap.Init(data)
	for _, key := range o.access {
		o.next[key] = o.next[key][1:]
		next := math.MaxInt
		if len(o.next[key]) > 0 {
			next = o.next[key][0]
		}

		if item, has := look[key]; has {
			hits++
			item.next = next
			heap.Fix(data, item.index)
			continue
		}

		if uint64(data.Len()) >= o.capacity {
			victim := heap.Pop(data)
			delete(look, victim.(*optimalItem[K]).key)
		}

		misses++
		newItem := &optimalItem[K]{key: key, next: next}
		look[key] = newItem
		heap.Push(data, newItem)
	}

	return 100 * (float64(hits) / float64(hits+misses))
}

func (o *Optimal[K, V]) Name() string {
	return "optimal"
}

func (o *Optimal[K, V]) Close() {
}

type optimalItem[K comparable] struct {
	key   K
	index int
	next  int
}

type optimalHeap[K comparable] []*optimalItem[K]

func (h optimalHeap[K]) Len() int { return len(h) }

func (h optimalHeap[K]) Less(i, j int) bool { return h[i].next > h[j].next }

func (h optimalHeap[K]) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *optimalHeap[K]) Push(x any) {
	item := x.(*optimalItem[K])
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *optimalHeap[K]) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	item.index = -1
	*h = old[:n-1]
	return item
}
