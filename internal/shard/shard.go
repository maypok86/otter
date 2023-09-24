package shard

import (
	"fmt"
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
	// 3 because we need to fit them into 1 cache line (64 bytes).
	bucketSize = 3
	// percentage at which the map will be expanded.
	loadFactor = 0.75
	// threshold fraction of table occupation to start a table shrinking
	// when deleting the last entry in a bucket chain.
	shrinkFraction   = 128
	minBucketCount   = 32
	minNodeCount     = bucketSize * minBucketCount
	minCounterLength = 8
	maxCounterLength = 32
)

func zeroValue[V any]() V {
	var zero V
	return zero
}

type Shard[K comparable, V any] struct {
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

func New[K comparable, V any](opts ...Option[K]) *Shard[K, V] {
	o := defaultOptions[K]()
	for _, opt := range opts {
		opt(o)
	}

	s := &Shard[K, V]{
		hasher: o.hasher,
	}
	s.resizeCond = *sync.NewCond(&s.resizeMutex)
	tableLength := xmath.RoundUpPowerOf2(uint32(o.initNodeCount / bucketSize))
	atomic.StorePointer(&s.table, unsafe.Pointer(newTable(int(tableLength))))
	return s
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

func (s *Shard[K, V]) Get(key K) (V, *node.Node[K, V], bool) {
	t := (*table)(atomic.LoadPointer(&s.table))
	hash := s.calcShiftHash(key)
	bucketIdx := hash & t.mask
	b := &t.buckets[bucketIdx]
	for {
		for i := 0; i < bucketSize; i++ {
			// we treat the hash code only as a hint, so there is no
			// need to get an atomic snapshot.
			h := atomic.LoadUint64(&b.hashes[i])
			if h == uint64(0) || h != hash {
				continue
			}
			// we found a matching hash code
			nodePtr := atomic.LoadPointer(&b.nodes[i])
			if nodePtr == nil {
				// concurrent write in this node
				return zeroValue[V](), nil, false
			}
			n := (*node.Node[K, V])(nodePtr)
			if key != n.Key {
				return zeroValue[V](), nil, false
			}
			if n.IsExpired() {
				return zeroValue[V](), n, false
			}

			return n.Value, nil, true
		}
		bucketPtr := atomic.LoadPointer(&b.next)
		if bucketPtr == nil {
			return zeroValue[V](), nil, false
		}
		b = (*paddedBucket)(bucketPtr)
	}
}

func (s *Shard[K, V]) Set(key K, value V) (inserted, evicted *node.Node[K, V]) {
	return s.SetWithExpiration(key, value, 0)
}

func (s *Shard[K, V]) SetWithExpiration(key K, value V, expiration uint64) (inserted, evicted *node.Node[K, V]) {
	for {
	RETRY:
		var (
			emptyBucket *paddedBucket
			emptyIdx    int
		)
		t := (*table)(atomic.LoadPointer(&s.table))
		tableLen := len(t.buckets)
		hash := s.calcShiftHash(key)
		bucketIdx := hash & t.mask
		rootBucket := &t.buckets[bucketIdx]
		rootBucket.mutex.Lock()
		if s.newerTableExists(t) {
			// someone resized the table, go for another attempt.
			rootBucket.mutex.Unlock()
			goto RETRY
		}
		if s.resizeInProgress() {
			// resize is in progress. wait, then go for another attempt.
			rootBucket.mutex.Unlock()
			s.waitForResize()
			goto RETRY
		}
		b := rootBucket
		for {
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
				n := (*node.Node[K, V])(b.nodes[i])
				newNode := node.New[K, V](key, value, expiration)
				atomic.StorePointer(&b.nodes[i], unsafe.Pointer(newNode))
				rootBucket.mutex.Unlock()
				return newNode, n
			}
			if b.next == nil {
				if emptyBucket != nil {
					// insertion into an existing bucket.
					newNode := node.New[K, V](key, value, expiration)
					// first we update the hash, then the entry.
					atomic.StoreUint64(&emptyBucket.hashes[emptyIdx], hash)
					atomic.StorePointer(&emptyBucket.nodes[emptyIdx], unsafe.Pointer(newNode))
					rootBucket.mutex.Unlock()
					t.addSize(bucketIdx, 1)
					return newNode, nil
				}
				growThreshold := float64(tableLen) * bucketSize * loadFactor
				if t.sumSize() > int64(growThreshold) {
					// need to grow the table then go for another attempt.
					rootBucket.mutex.Unlock()
					s.resize(t, growHint)
					goto RETRY
				}
				// insertion into a new bucket.
				// create and append the bucket.
				newNode := node.New[K, V](key, value, expiration)
				newBucket := &paddedBucket{}
				newBucket.hashes[0] = hash
				newBucket.nodes[0] = unsafe.Pointer(newNode)
				atomic.StorePointer(&b.next, unsafe.Pointer(newBucket))
				rootBucket.mutex.Unlock()
				t.addSize(bucketIdx, 1)
				return newNode, nil
			}
			b = (*paddedBucket)(b.next)
		}
	}
}

func (s *Shard[K, V]) Delete(key K) *node.Node[K, V] {
	return s.delete(key, func(n *node.Node[K, V]) bool {
		return key == n.Key
	})
}

func (s *Shard[K, V]) EvictNode(n *node.Node[K, V]) *node.Node[K, V] {
	return s.delete(n.Key, func(current *node.Node[K, V]) bool {
		return n == current
	})
}

func (s *Shard[K, V]) delete(key K, cmp func(*node.Node[K, V]) bool) *node.Node[K, V] {
	for {
	RETRY:
		hintNonEmpty := 0
		t := (*table)(atomic.LoadPointer(&s.table))
		hash := s.calcShiftHash(key)
		bucketIdx := hash & t.mask
		rootBucket := &t.buckets[bucketIdx]
		rootBucket.mutex.Lock()
		if s.newerTableExists(t) {
			// someone resized the table. Go for another attempt.
			rootBucket.mutex.Unlock()
			goto RETRY
		}
		if s.resizeInProgress() {
			// resize is in progress. Wait, then go for another attempt.
			rootBucket.mutex.Unlock()
			s.waitForResize()
			goto RETRY
		}
		b := rootBucket
		for {
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
					return nil
				}
				atomic.StoreUint64(&b.hashes[i], uint64(0))
				atomic.StorePointer(&b.nodes[i], nil)
				leftEmpty := false
				if hintNonEmpty == 0 {
					leftEmpty = b.isEmpty()
				}
				rootBucket.mutex.Unlock()
				t.addSize(bucketIdx, -1)
				// Might need to shrink the table.
				if leftEmpty {
					s.resize(t, shrinkHint)
				}
				return current
			}
			if b.next == nil {
				// not found
				rootBucket.mutex.Unlock()
				return nil
			}
			b = (*paddedBucket)(b.next)
		}
	}
}

func (s *Shard[K, V]) resize(t *table, hint resizeHint) {
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
	if !s.resizing.CompareAndSwap(0, 1) {
		// someone else started resize. Wait for it to finish.
		s.waitForResize()
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
			// no need to shrink, wake up all waiters and give up.
			s.resizeMutex.Lock()
			s.resizing.Store(0)
			s.resizeCond.Broadcast()
			s.resizeMutex.Unlock()
			return
		}
	case clearHint:
		nt = newTable(minBucketCount)
	default:
		panic(fmt.Sprintf("unexpected resize hint: %d", hint))
	}
	// copy the data only if we're not clearing the shard.
	if hint != clearHint {
		for i := 0; i < tableLen; i++ {
			copied := s.copyBuckets(&t.buckets[i], nt)
			nt.addSizePlain(uint64(i), copied)
		}
	}
	// publish the new table and wake up all waiters.
	atomic.StorePointer(&s.table, unsafe.Pointer(nt))
	s.resizeMutex.Lock()
	s.resizing.Store(0)
	s.resizeCond.Broadcast()
	s.resizeMutex.Unlock()
}

func (s *Shard[K, V]) copyBuckets(b *paddedBucket, dest *table) (copied int) {
	rootBucket := b
	rootBucket.mutex.Lock()
	for {
		for i := 0; i < bucketSize; i++ {
			if b.nodes[i] == nil {
				continue
			}
			n := (*node.Node[K, V])(b.nodes[i])
			if n.IsExpired() {
				continue
			}
			hash := s.calcShiftHash(n.Key)
			bucketIdx := hash & dest.mask
			dest.buckets[bucketIdx].add(hash, b.nodes[i])
			copied++
		}
		if b.next == nil {
			rootBucket.mutex.Unlock()
			return copied
		}
		b = (*paddedBucket)(b.next)
	}
}

func (s *Shard[K, V]) newerTableExists(table *table) bool {
	currentTable := atomic.LoadPointer(&s.table)
	return uintptr(currentTable) != uintptr(unsafe.Pointer(table))
}

func (s *Shard[K, V]) resizeInProgress() bool {
	return s.resizing.Load() == 1
}

func (s *Shard[K, V]) waitForResize() {
	s.resizeMutex.Lock()
	for s.resizeInProgress() {
		s.resizeCond.Wait()
	}
	s.resizeMutex.Unlock()
}

func (s *Shard[K, V]) Clear() {
	table := (*table)(atomic.LoadPointer(&s.table))
	s.resize(table, clearHint)
}

func (s *Shard[K, V]) Size() int {
	table := (*table)(atomic.LoadPointer(&s.table))
	return int(table.sumSize())
}

func (s *Shard[K, V]) calcShiftHash(key K) uint64 {
	h := s.hasher(key)
	if h == uint64(0) {
		return 1
	}

	return h
}
