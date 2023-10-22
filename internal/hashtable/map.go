package hashtable

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/maypok86/otter/internal/node"
	"github.com/maypok86/otter/internal/xmath"
	"github.com/maypok86/otter/internal/xruntime"
)

type resizeHint int

const (
	growHint   resizeHint = 0
	shrinkHint resizeHint = 1
	clearHint  resizeHint = 2
)

const (
	// number of entries per bucket
	// 7 because we need to fit them into 2 cache lines (128 bytes).
	bucketSize       = 7
	maxSpinThreshold = 16
	shrinkFraction   = 128
	minBucketCount   = 32
	minNodeCount     = bucketSize * minBucketCount
	minCounterLength = 8
	maxCounterLength = 32
)

type Map[K comparable, V any] struct {
	table unsafe.Pointer

	resizeMutex sync.Mutex
	resizeCond  sync.Cond
	resizing    atomic.Int64

	hasher func(K) uint64
}

type table struct {
	buckets []paddedBucket
	size    []paddedCounter
	mask    uint64
}

func (t *table) addSize(bucketIdx uint64, delta int) {
	counterIdx := uint64(len(t.size)-1) & bucketIdx
	atomic.AddInt64(&t.size[counterIdx].c, int64(delta))
}

func (t *table) addSizePlain(bucketIdx uint64, delta int) {
	counterIdx := uint64(len(t.size)-1) & bucketIdx
	t.size[counterIdx].c += int64(delta)
}

func (t *table) sumSize() int64 {
	sum := int64(0)
	for i := range t.size {
		sum += atomic.LoadInt64(&t.size[i].c)
	}
	return sum
}

type counter struct {
	c int64
}

type paddedCounter struct {
	padding [xruntime.CacheLineSize - unsafe.Sizeof(counter{})]byte

	counter
}

func New[K comparable, V any](opts ...Option[K]) *Map[K, V] {
	o := defaultOptions[K]()
	for _, opt := range opts {
		opt(o)
	}

	m := &Map[K, V]{
		hasher: o.hasher,
	}
	m.resizeCond = *sync.NewCond(&m.resizeMutex)
	tableLength := xmath.RoundUpPowerOf2(uint32(o.initNodeCount / bucketSize))
	atomic.StorePointer(&m.table, unsafe.Pointer(newTable(int(tableLength))))
	return m
}

func newTable(bucketCount int) *table {
	buckets := make([]paddedBucket, bucketCount)
	counterLength := bucketCount >> 10
	if counterLength < minCounterLength {
		counterLength = minCounterLength
	} else if counterLength > maxCounterLength {
		counterLength = maxCounterLength
	}
	counter := make([]paddedCounter, counterLength)
	mask := uint64(len(buckets) - 1)
	t := &table{
		buckets: buckets,
		size:    counter,
		mask:    mask,
	}
	return t
}

func (m *Map[K, V]) Get(key K) (*node.Node[K, V], bool) {
	t := (*table)(atomic.LoadPointer(&m.table))
	hash := m.calcShiftHash(key)
	bucketIdx := hash & t.mask
	b := &t.buckets[bucketIdx]
	for i := 0; i < bucketSize; i++ {
		spins := 0
		for {
			spins++
			if spins > maxSpinThreshold {
				spins = 0
				runtime.Gosched()
			}

			seq := atomic.LoadUint64(&b.seq)
			if seq&1 == 1 {
				// In progress update/delete
				continue
			}

			h := atomic.LoadUint64(&b.hashes[i])
			if h == uint64(0) || h != hash {
				break
			}

			nodePtr := atomic.LoadPointer(&b.nodes[i])
			if nodePtr == nil {
				// concurrent write in this node
				return nil, false
			}
			n := (*node.Node[K, V])(nodePtr)
			if key != n.Key() {
				return nil, false
			}

			return n, true
		}
	}

	return nil, false
}

func (m *Map[K, V]) Set(n *node.Node[K, V]) *node.Node[K, V] {
	for {
		var (
			emptyBucket *paddedBucket
			emptyIdx    int
		)
		t := (*table)(atomic.LoadPointer(&m.table))
		hash := m.calcShiftHash(n.Key())
		bucketIdx := hash & t.mask
		b := &t.buckets[bucketIdx]
		b.mutex.Lock()
		if m.newerTableExists(t) {
			// someone resized the table, go for another attempt.
			b.mutex.Unlock()
			continue
		}
		if m.resizeInProgress() {
			// resize is in progress. wait, then go for another attempt.
			b.mutex.Unlock()
			m.waitForResize()
			continue
		}
		for i := 0; i < bucketSize; i++ {
			h := b.hashes[i]
			if h == uint64(0) {
				if emptyBucket == nil {
					emptyBucket = b
					emptyIdx = i
				}
				continue
			}
			if h != hash {
				continue
			}

			// Update in progress
			atomic.AddUint64(&b.seq, 1)

			evicted := (*node.Node[K, V])(b.nodes[i])
			atomic.StorePointer(&b.nodes[i], unsafe.Pointer(n))

			// Update done
			atomic.AddUint64(&b.seq, 1)

			b.mutex.Unlock()
			return evicted
		}
		if emptyBucket != nil {
			// Insert in progress
			atomic.AddUint64(&b.seq, 1)

			atomic.StoreUint64(&emptyBucket.hashes[emptyIdx], hash)
			atomic.StorePointer(&emptyBucket.nodes[emptyIdx], unsafe.Pointer(n))

			// Insert done
			atomic.AddUint64(&b.seq, 1)

			b.mutex.Unlock()
			t.addSize(bucketIdx, 1)
			return nil
		}

		// Need to grow the table. Then go for another attempt.
		b.mutex.Unlock()
		m.resize(t, growHint)
	}
}

func (m *Map[K, V]) Delete(key K) *node.Node[K, V] {
	return m.delete(key, func(n *node.Node[K, V]) bool {
		return key == n.Key()
	})
}

func (m *Map[K, V]) EvictNode(n *node.Node[K, V]) *node.Node[K, V] {
	return m.delete(n.Key(), func(current *node.Node[K, V]) bool {
		return n == current
	})
}

func (m *Map[K, V]) delete(key K, cmp func(*node.Node[K, V]) bool) *node.Node[K, V] {
	for {
		hintNonEmpty := 0
		t := (*table)(atomic.LoadPointer(&m.table))
		hash := m.calcShiftHash(key)
		bucketIdx := hash & t.mask
		b := &t.buckets[bucketIdx]
		b.mutex.Lock()
		if m.newerTableExists(t) {
			// someone resized the table. Go for another attempt.
			b.mutex.Unlock()
			continue
		}
		if m.resizeInProgress() {
			// resize is in progress. Wait, then go for another attempt.
			b.mutex.Unlock()
			m.waitForResize()
			continue
		}

		for i := 0; i < bucketSize; i++ {
			h := b.hashes[i]
			if h == uint64(0) {
				continue
			}
			if h != hash {
				hintNonEmpty++
				continue
			}

			current := (*node.Node[K, V])(b.nodes[i])
			if !cmp(current) {
				b.mutex.Unlock()
				return nil
			}

			// delete in progress
			atomic.AddUint64(&b.seq, 1)

			atomic.StoreUint64(&b.hashes[i], uint64(0))
			atomic.StorePointer(&b.nodes[i], nil)

			// delete done
			atomic.AddUint64(&b.seq, 1)

			leftEmpty := false
			if hintNonEmpty == 0 {
				leftEmpty = b.isEmpty()
			}
			b.mutex.Unlock()
			t.addSize(bucketIdx, -1)
			// might need to shrink the table.
			if leftEmpty {
				m.resize(t, shrinkHint)
			}
			return current
		}

		// not found
		b.mutex.Unlock()
		return nil
	}
}

func (m *Map[K, V]) resize(t *table, hint resizeHint) {
	var shrinkThreshold int64
	tableLen := len(t.buckets)
	// fast path for shrink attempts.
	if hint == shrinkHint {
		shrinkThreshold = int64((tableLen * bucketSize) / shrinkFraction)
		if tableLen == minBucketCount || t.sumSize() > shrinkThreshold {
			return
		}
	}
	// slow path.
	if !m.resizing.CompareAndSwap(0, 1) {
		// someone else started resize. Wait for it to finish.
		m.waitForResize()
		return
	}
	var nt *table
	switch hint {
	case growHint:
		// grow the table with factor of 2.
		nt = newTable(tableLen << 1)
	case shrinkHint:
		if t.sumSize() <= shrinkThreshold {
			// shrink the table with factor of 2.
			nt = newTable(tableLen >> 1)
		} else {
			// no need to shrink
			// wake up all waiters and give up.
			m.resizeMutex.Lock()
			m.resizing.Store(0)
			m.resizeCond.Broadcast()
			m.resizeMutex.Unlock()
			return
		}
	case clearHint:
		nt = newTable(minBucketCount)
	default:
		panic(fmt.Sprintf("unexpected resize hint: %d", hint))
	}
	// copy the data only if we're not clearing the hashtable.
	if hint != clearHint {
		for i := 0; i < tableLen; i++ {
			copied := m.copyBuckets(&t.buckets[i], nt)
			nt.addSizePlain(uint64(i), copied)
		}
	}
	// publish the new table and wake up all waiters.
	atomic.StorePointer(&m.table, unsafe.Pointer(nt))
	m.resizeMutex.Lock()
	m.resizing.Store(0)
	m.resizeCond.Broadcast()
	m.resizeMutex.Unlock()
}

func (m *Map[K, V]) copyBuckets(b *paddedBucket, dest *table) (copied int) {
	b.mutex.Lock()
	for i := 0; i < bucketSize; i++ {
		if b.nodes[i] == nil {
			continue
		}
		n := (*node.Node[K, V])(b.nodes[i])
		hash := m.calcShiftHash(n.Key())
		bucketIdx := hash & dest.mask
		dest.buckets[bucketIdx].add(hash, b.nodes[i])
		copied++
	}
	b.mutex.Unlock()
	return copied
}

func (m *Map[K, V]) newerTableExists(table *table) bool {
	currentTable := atomic.LoadPointer(&m.table)
	return uintptr(currentTable) != uintptr(unsafe.Pointer(table))
}

func (m *Map[K, V]) resizeInProgress() bool {
	return m.resizing.Load() == 1
}

func (m *Map[K, V]) waitForResize() {
	m.resizeMutex.Lock()
	for m.resizeInProgress() {
		m.resizeCond.Wait()
	}
	m.resizeMutex.Unlock()
}

func (m *Map[K, V]) Clear() {
	table := (*table)(atomic.LoadPointer(&m.table))
	m.resize(table, clearHint)
}

func (m *Map[K, V]) Size() int {
	table := (*table)(atomic.LoadPointer(&m.table))
	return int(table.sumSize())
}

func (m *Map[K, V]) calcShiftHash(key K) uint64 {
	h := m.hasher(key)
	if h == uint64(0) {
		return 1
	}

	return h
}
