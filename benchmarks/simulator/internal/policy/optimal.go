package policy

import (
	"container/heap"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

// Optimal is Belady's MIN policy: on a miss, it evicts the entry that is
// accessed again furthest in the future. It also skips an entry that comes
// back later than every cached one, as a cache with an admission policy
// can. No cache can get a higher hit ratio on the same trace, so it is an
// upper bound for the others.
//
// It needs the whole trace, so it records the keys and simulates the cache
// when the ratio is asked for.
type Optimal struct {
	capacity int
	keys     []uint64
}

func NewOptimal() *Optimal {
	return &Optimal{}
}

func (o *Optimal) Init(capacity int) {
	o.capacity = capacity
}

func (o *Optimal) Record(e event.AccessEvent) {
	o.keys = append(o.keys, e.Key())
}

func (o *Optimal) Ratio() float64 {
	n := len(o.keys)
	if n == 0 {
		return 0
	}

	// next[i] is the position of the next access to keys[i], or n if there
	// is none.
	next := make([]int, n)
	last := make(map[uint64]int)
	for i := n - 1; i >= 0; i-- {
		key := o.keys[i]
		if j, ok := last[key]; ok {
			next[i] = j
		} else {
			next[i] = n
		}
		last[key] = i
	}

	// cached maps a cached key to its next access. The heap holds the same
	// pairs plus stale ones left by hits; a pair is current only while it
	// matches cached. Positions in next are never 0, so a missing key never
	// matches.
	cached := make(map[uint64]int, o.capacity)
	h := &optimalHeap{}
	top := func() optimalItem {
		for {
			item := (*h)[0]
			if cached[item.key] == item.next {
				return item
			}
			heap.Pop(h)
		}
	}

	hits := 0
	for i, key := range o.keys {
		if _, ok := cached[key]; ok {
			hits++
			cached[key] = next[i]
			heap.Push(h, optimalItem{key: key, next: next[i]})
			continue
		}
		if next[i] == n {
			// Never accessed again, so caching it can't produce a hit.
			continue
		}
		if len(cached) >= o.capacity {
			victim := top()
			if victim.next < next[i] {
				// Every cached entry is needed sooner than this one.
				continue
			}
			heap.Pop(h)
			delete(cached, victim.key)
		}
		cached[key] = next[i]
		heap.Push(h, optimalItem{key: key, next: next[i]})
	}

	return 100 * float64(hits) / float64(n)
}

func (o *Optimal) Name() string {
	return "optimal"
}

func (o *Optimal) Close() {
	o.keys = nil
}

type optimalItem struct {
	key  uint64
	next int
}

// optimalHeap is a max-heap by the next access.
type optimalHeap []optimalItem

func (h optimalHeap) Len() int           { return len(h) }
func (h optimalHeap) Less(i, j int) bool { return h[i].next > h[j].next }
func (h optimalHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *optimalHeap) Push(x any) {
	//nolint:errcheck // the heap only gets optimalItem
	*h = append(*h, x.(optimalItem))
}

func (h *optimalHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
