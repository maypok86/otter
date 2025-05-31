package policy

import (
	"container/heap"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type Optimal struct {
	capacity uint64
	hits     map[uint64]uint64
	access   []uint64
}

func NewOptimal(capacity int) *Optimal {
	return &Optimal{
		capacity: uint64(capacity),
		hits:     make(map[uint64]uint64),
		access:   make([]uint64, 0),
	}
}

func (o *Optimal) Record(e event.AccessEvent) {
	o.hits[e.Key()]++
	o.access = append(o.access, e.Key())
}

func (o *Optimal) Ratio() float64 {
	hits := uint64(0)
	misses := uint64(0)
	look := make(map[uint64]struct{}, o.capacity)
	data := &optimalHeap{}
	heap.Init(data)
	for _, key := range o.access {
		if _, has := look[key]; has {
			hits++
			continue
		}
		if uint64(data.Len()) >= o.capacity {
			victim := heap.Pop(data)
			delete(look, victim.(*optimalItem).key)
		}
		misses++
		look[key] = struct{}{}
		heap.Push(data, &optimalItem{key, o.hits[key]})
	}

	return 100 * (float64(hits) / float64(hits+misses))
}

func (o *Optimal) Name() string {
	return "optimal"
}

func (o *Optimal) Close() {
}

type optimalItem struct {
	key  uint64
	hits uint64
}

type optimalHeap []*optimalItem

func (h optimalHeap) Len() int           { return len(h) }
func (h optimalHeap) Less(i, j int) bool { return h[i].hits < h[j].hits }
func (h optimalHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *optimalHeap) Push(x any) {
	*h = append(*h, x.(*optimalItem))
}

func (h *optimalHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
