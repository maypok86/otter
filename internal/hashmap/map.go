// Copyright (c) 2024 Alexey Mayshev. All rights reserved.
// Copyright (c) 2021 Andrey Pechkurov
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Copyright notice. This code is a fork of tests for xsync.MapOf from this file with some changes:
// https://github.com/puzpuzpuz/xsync/blob/main/mapof_test.go
//
// Use of this source code is governed by a MIT license that can be found
// at https://github.com/puzpuzpuz/xsync/blob/main/LICENSE

package hashmap

import (
	"fmt"
	"math/bits"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/dolthub/maphash"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

type mapResizeHint int

const (
	mapGrowHint   mapResizeHint = 0
	mapShrinkHint mapResizeHint = 1
	mapClearHint  mapResizeHint = 2
)

const (
	// number of Map nodes per bucket; 5 nodes lead to size of 64B
	// (one cache line) on 64-bit machines.
	nodesPerMapBucket        = 5
	defaultMeta       uint64 = 0x8080808080808080
	metaMask          uint64 = 0xffffffffff
	defaultMetaMasked        = defaultMeta & metaMask
	emptyMetaSlot     uint8  = 0x80

	// threshold fraction of table occupation to start a table shrinking
	// when deleting the last entry in a bucket chain.
	mapShrinkFraction = 128
	// map load factor to trigger a table resize during insertion;
	// a map holds up to mapLoadFactor*nodesPerMapBucket*mapTableLen
	// key-value pairs (this is a soft limit).
	mapLoadFactor = 0.75
	// minimal table size, i.e. number of buckets; thus, minimal map
	// capacity can be calculated as nodesPerMapBucket*defaultMinMapTableLen.
	defaultMinMapTableLen = 32
	// minimum counter stripes to use.
	minMapCounterLen = 8
	// maximum counter stripes to use; stands for around 4KB of memory.
	maxMapCounterLen = 32
)

// Map is like a Go map[K]V but is safe for concurrent
// use by multiple goroutines without additional locking or
// coordination. It follows the interface of sync.Map with
// a number of valuable extensions like Compute or Size.
//
// A Map must not be copied after first use.
//
// Map uses a modified version of Cache-Line Hash Table (CLHT)
// data structure: https://github.com/LPD-EPFL/CLHT
//
// CLHT is built around idea to organize the hash table in
// cache-line-sized buckets, so that on all modern CPUs update
// operations complete with at most one cache-line transfer.
// Also, Get operations involve no write to memory, as well as no
// mutexes or any other sort of locks. Due to this design, in all
// considered scenarios Map outperforms sync.Map.
//
// Map also borrows ideas from Java's j.u.c.ConcurrentHashMap
// (immutable K/V pair structs instead of atomic snapshots)
// and C++'s absl::flat_hash_map (meta memory and SWAR-based
// lookups).
type Map[K comparable, V any] struct {
	totalGrowths int64
	totalShrinks int64
	resizing     int64          // resize in progress flag; updated atomically
	resizeMu     sync.Mutex     // only used along with resizeCond
	resizeCond   sync.Cond      // used to wake up resize waiters (concurrent modifications)
	table        unsafe.Pointer // *mapTable
	nodeManager  *node.Manager[K, V]
	minTableLen  int
}

type counterStripe struct {
	c int64
	//lint:ignore U1000 prevents false sharing
	pad [xruntime.CacheLineSize - 8]byte
}

type mapTable[K comparable, V any] struct {
	buckets []bucketPadded
	// striped counter for number of table nodes;
	// used to determine if a table shrinking is needed
	// occupies min(buckets_memory/1024, 64KB) of memory
	size   []counterStripe
	hasher maphash.Hasher[K]
}

// bucketPadded is a CL-sized map bucket holding up to
// nodesPerMapBucket nodes.
type bucketPadded struct {
	//lint:ignore U1000 ensure each bucket takes two cache lines on both 32 and 64-bit archs
	pad [xruntime.CacheLineSize - unsafe.Sizeof(bucket{})]byte
	bucket
}

type bucket struct {
	meta  uint64
	nodes [nodesPerMapBucket]unsafe.Pointer // *node.Node
	next  unsafe.Pointer                    // *bucketPadded
	mu    sync.Mutex
}

// NewWithSize creates a new Map instance with capacity enough
// to hold size nodes. If size is zero or negative, the value
// is ignored.
func NewWithSize[K comparable, V any](nodeManager *node.Manager[K, V], size int) *Map[K, V] {
	return newMap[K, V](nodeManager, size)
}

// New creates a new Map instance.
func New[K comparable, V any](nodeManager *node.Manager[K, V]) *Map[K, V] {
	return newMap[K, V](nodeManager, defaultMinMapTableLen*nodesPerMapBucket)
}

func newMap[K comparable, V any](nodeManager *node.Manager[K, V], sizeHint int) *Map[K, V] {
	m := &Map[K, V]{
		nodeManager: nodeManager,
	}
	m.resizeCond = *sync.NewCond(&m.resizeMu)
	var table *mapTable[K, V]
	prevHasher := maphash.NewHasher[K]()
	if sizeHint <= defaultMinMapTableLen*nodesPerMapBucket {
		table = newMapTable[K, V](defaultMinMapTableLen, prevHasher)
	} else {
		tableLen := xmath.RoundUpPowerOf2(uint32((float64(sizeHint) / nodesPerMapBucket) / mapLoadFactor))
		table = newMapTable[K, V](int(tableLen), prevHasher)
	}
	m.minTableLen = len(table.buckets)
	atomic.StorePointer(&m.table, unsafe.Pointer(table))
	return m
}

func newMapTable[K comparable, V any](minTableLen int, prevHasher maphash.Hasher[K]) *mapTable[K, V] {
	buckets := make([]bucketPadded, minTableLen)
	for i := range buckets {
		buckets[i].meta = defaultMeta
	}
	counterLen := minTableLen >> 10
	if counterLen < minMapCounterLen {
		counterLen = minMapCounterLen
	} else if counterLen > maxMapCounterLen {
		counterLen = maxMapCounterLen
	}
	counter := make([]counterStripe, counterLen)
	t := &mapTable[K, V]{
		buckets: buckets,
		size:    counter,
		hasher:  maphash.NewSeed[K](prevHasher),
	}
	return t
}

// Get returns the node stored in the map for a key, or nil
// if no value is present.
// The ok result indicates whether value was found in the map.
func (m *Map[K, V]) Get(key K) (got node.Node[K, V], ok bool) {
	table := (*mapTable[K, V])(atomic.LoadPointer(&m.table))
	hash := table.hasher.Hash(key)
	h1 := h1(hash)
	h2w := broadcast(h2(hash))
	//nolint:gosec // there is no overflow
	bidx := uint64(len(table.buckets)-1) & h1
	b := &table.buckets[bidx]
	for {
		metaw := atomic.LoadUint64(&b.meta)
		markedw := markZeroBytes(metaw^h2w) & metaMask
		for markedw != 0 {
			idx := firstMarkedByteIndex(markedw)
			nptr := atomic.LoadPointer(&b.nodes[idx])
			if nptr != nil {
				n := m.nodeManager.FromPointer(nptr)
				if n.Key() == key {
					return n, true
				}
			}
			markedw &= markedw - 1
		}
		bptr := atomic.LoadPointer(&b.next)
		if bptr == nil {
			return nil, false
		}
		b = (*bucketPadded)(bptr)
	}
}

// Compute either sets the computed new value for the key or deletes
// the value for the key.
//
// This call locks a hash table bucket while the compute function
// is executed. It means that modifications on other nodes in
// the bucket will be blocked until the computeFn executes. Consider
// this when the function includes long-running operations.
func (m *Map[K, V]) Compute(
	key K,
	computeFunc func(n node.Node[K, V]) node.Node[K, V],
) node.Node[K, V] {
	for {
	compute_attempt:
		var (
			emptyb   *bucketPadded
			emptyidx int
		)
		table := (*mapTable[K, V])(atomic.LoadPointer(&m.table))
		tableLen := len(table.buckets)
		hash := table.hasher.Hash(key)
		h1 := h1(hash)
		h2 := h2(hash)
		h2w := broadcast(h2)
		//nolint:gosec // there is no overflow
		bidx := uint64(len(table.buckets)-1) & h1
		rootb := &table.buckets[bidx]
		rootb.mu.Lock()
		// The following two checks must go in reverse to what's
		// in the resize method.
		if m.resizeInProgress() {
			// Resize is in progress. Wait, then go for another attempt.
			rootb.mu.Unlock()
			m.waitForResize()
			goto compute_attempt
		}
		if m.newerTableExists(table) {
			// Someone resized the table. Go for another attempt.
			rootb.mu.Unlock()
			goto compute_attempt
		}
		b := rootb
		for {
			metaw := b.meta
			markedw := markZeroBytes(metaw^h2w) & metaMask
			for markedw != 0 {
				idx := firstMarkedByteIndex(markedw)
				nptr := b.nodes[idx]
				if nptr != nil {
					oldNode := m.nodeManager.FromPointer(nptr)
					if oldNode.Key() == key {
						// In-place update/delete.
						newNode := computeFunc(oldNode)
						// oldNode != nil
						if newNode == nil {
							// Deletion.
							// First we update the hash, then the node.
							newmetaw := setByte(metaw, emptyMetaSlot, idx)
							atomic.StoreUint64(&b.meta, newmetaw)
							atomic.StorePointer(&b.nodes[idx], nil)
							rootb.mu.Unlock()
							table.addSize(bidx, -1)
							// Might need to shrink the table if we left bucket empty.
							if newmetaw == defaultMeta {
								m.resize(table, mapShrinkHint)
							}
							return newNode
						}
						if oldNode.AsPointer() != newNode.AsPointer() {
							atomic.StorePointer(&b.nodes[idx], newNode.AsPointer())
						}
						rootb.mu.Unlock()
						return newNode
					}
				}
				markedw &= markedw - 1
			}
			if emptyb == nil {
				// Search for empty nodes (up to 5 per bucket).
				emptyw := metaw & defaultMetaMasked
				if emptyw != 0 {
					idx := firstMarkedByteIndex(emptyw)
					emptyb = b
					emptyidx = idx
				}
			}
			if b.next == nil {
				if emptyb != nil {
					// Insertion into an existing bucket.
					var zeroNode node.Node[K, V]
					// oldNode == nil.
					newNode := computeFunc(zeroNode)
					if newNode == nil {
						// no op.
						rootb.mu.Unlock()
						return newNode
					}
					// First we update meta, then the node.
					atomic.StoreUint64(&emptyb.meta, setByte(emptyb.meta, h2, emptyidx))
					atomic.StorePointer(&emptyb.nodes[emptyidx], newNode.AsPointer())
					rootb.mu.Unlock()
					table.addSize(bidx, 1)
					return newNode
				}
				growThreshold := float64(tableLen) * nodesPerMapBucket * mapLoadFactor
				if table.sumSize() > int64(growThreshold) {
					// Need to grow the table. Then go for another attempt.
					rootb.mu.Unlock()
					m.resize(table, mapGrowHint)
					goto compute_attempt
				}
				// Insertion into a new bucket.
				var zeroNode node.Node[K, V]
				// oldNode == nil
				newNode := computeFunc(zeroNode)
				if newNode == nil {
					rootb.mu.Unlock()
					return newNode
				}
				// Create and append a bucket.
				newb := new(bucketPadded)
				newb.meta = setByte(defaultMeta, h2, 0)
				newb.nodes[0] = newNode.AsPointer()
				atomic.StorePointer(&b.next, unsafe.Pointer(newb))
				rootb.mu.Unlock()
				table.addSize(bidx, 1)
				return newNode
			}
			b = (*bucketPadded)(b.next)
		}
	}
}

func (m *Map[K, V]) newerTableExists(table *mapTable[K, V]) bool {
	curTablePtr := atomic.LoadPointer(&m.table)
	return uintptr(curTablePtr) != uintptr(unsafe.Pointer(table))
}

func (m *Map[K, V]) resizeInProgress() bool {
	return atomic.LoadInt64(&m.resizing) == 1
}

func (m *Map[K, V]) waitForResize() {
	m.resizeMu.Lock()
	for m.resizeInProgress() {
		m.resizeCond.Wait()
	}
	m.resizeMu.Unlock()
}

func (m *Map[K, V]) resize(knownTable *mapTable[K, V], hint mapResizeHint) {
	knownTableLen := len(knownTable.buckets)
	// Fast path for shrink attempts.
	if hint == mapShrinkHint {
		if m.minTableLen == knownTableLen ||
			knownTable.sumSize() > int64((knownTableLen*nodesPerMapBucket)/mapShrinkFraction) {
			return
		}
	}
	// Slow path.
	if !atomic.CompareAndSwapInt64(&m.resizing, 0, 1) {
		// Someone else started resize. Wait for it to finish.
		m.waitForResize()
		return
	}
	var newTable *mapTable[K, V]
	table := (*mapTable[K, V])(atomic.LoadPointer(&m.table))
	tableLen := len(table.buckets)
	switch hint {
	case mapGrowHint:
		// Grow the table with factor of 2.
		atomic.AddInt64(&m.totalGrowths, 1)
		newTable = newMapTable[K, V](tableLen<<1, table.hasher)
	case mapShrinkHint:
		shrinkThreshold := int64((tableLen * nodesPerMapBucket) / mapShrinkFraction)
		if tableLen > m.minTableLen && table.sumSize() <= shrinkThreshold {
			// Shrink the table with factor of 2.
			atomic.AddInt64(&m.totalShrinks, 1)
			newTable = newMapTable[K, V](tableLen>>1, table.hasher)
		} else {
			// No need to shrink. Wake up all waiters and give up.
			m.resizeMu.Lock()
			atomic.StoreInt64(&m.resizing, 0)
			m.resizeCond.Broadcast()
			m.resizeMu.Unlock()
			return
		}
	case mapClearHint:
		newTable = newMapTable[K, V](m.minTableLen, table.hasher)
	default:
		panic(fmt.Sprintf("unexpected resize hint: %d", hint))
	}
	// Copy the data only if we're not clearing the map.
	if hint != mapClearHint {
		for i := 0; i < tableLen; i++ {
			copied := m.copyBucket(&table.buckets[i], newTable)
			//nolint:gosec // there is no overflow
			newTable.addSizePlain(uint64(i), copied)
		}
	}
	// Publish the new table and wake up all waiters.
	atomic.StorePointer(&m.table, unsafe.Pointer(newTable))
	m.resizeMu.Lock()
	atomic.StoreInt64(&m.resizing, 0)
	m.resizeCond.Broadcast()
	m.resizeMu.Unlock()
}

func (m *Map[K, V]) copyBucket(
	b *bucketPadded,
	destTable *mapTable[K, V],
) (copied int) {
	rootb := b
	rootb.mu.Lock()
	//nolint:gocritic // nesting is normal here
	for {
		for i := 0; i < nodesPerMapBucket; i++ {
			if b.nodes[i] != nil {
				n := m.nodeManager.FromPointer(b.nodes[i])
				hash := destTable.hasher.Hash(n.Key())
				//nolint:gosec // there is no overflow
				bidx := uint64(len(destTable.buckets)-1) & h1(hash)
				destb := &destTable.buckets[bidx]
				appendToBucket(h2(hash), b.nodes[i], destb)
				copied++
			}
		}
		if b.next == nil {
			rootb.mu.Unlock()
			return copied
		}
		b = (*bucketPadded)(b.next)
	}
}

// Range calls f sequentially for each key and value present in the
// map. If f returns false, range stops the iteration.
//
// Range does not necessarily correspond to any consistent snapshot
// of the Map's contents: no key will be visited more than once, but
// if the value for any key is stored or deleted concurrently, Range
// may reflect any mapping for that key from any point during the
// Range call.
//
// It is safe to modify the map while iterating it, including entry
// creation, modification and deletion. However, the concurrent
// modification rule apply, i.e. the changes may be not reflected
// in the subsequently iterated nodes.
func (m *Map[K, V]) Range(fn func(n node.Node[K, V]) bool) {
	var zeroPtr unsafe.Pointer
	// Pre-allocate array big enough to fit nodes for most hash tables.
	bnodes := make([]unsafe.Pointer, 0, 16*nodesPerMapBucket)
	tablep := atomic.LoadPointer(&m.table)
	table := *(*mapTable[K, V])(tablep)
	for i := range table.buckets {
		rootb := &table.buckets[i]
		b := rootb
		// Prevent concurrent modifications and copy all nodes into
		// the intermediate slice.
		rootb.mu.Lock()
		for {
			for i := 0; i < nodesPerMapBucket; i++ {
				if b.nodes[i] != nil {
					bnodes = append(bnodes, b.nodes[i])
				}
			}
			if b.next == nil {
				rootb.mu.Unlock()
				break
			}
			b = (*bucketPadded)(b.next)
		}
		// Call the function for all copied nodes.
		for j := range bnodes {
			n := m.nodeManager.FromPointer(bnodes[j])
			if !fn(n) {
				return
			}
			// Remove the reference to avoid preventing the copied
			// nodes from being GCed until this method finishes.
			bnodes[j] = zeroPtr
		}
		bnodes = bnodes[:0]
	}
}

// Clear deletes all keys and values currently stored in the map.
func (m *Map[K, V]) Clear() {
	table := (*mapTable[K, V])(atomic.LoadPointer(&m.table))
	m.resize(table, mapClearHint)
}

// Size returns current size of the map.
func (m *Map[K, V]) Size() int {
	table := (*mapTable[K, V])(atomic.LoadPointer(&m.table))
	return int(table.sumSize())
}

func appendToBucket(h2 uint8, nodePtr unsafe.Pointer, b *bucketPadded) {
	for {
		for i := 0; i < nodesPerMapBucket; i++ {
			if b.nodes[i] == nil {
				b.meta = setByte(b.meta, h2, i)
				b.nodes[i] = nodePtr
				return
			}
		}
		if b.next == nil {
			newb := new(bucketPadded)
			newb.meta = setByte(defaultMeta, h2, 0)
			newb.nodes[0] = nodePtr
			b.next = unsafe.Pointer(newb)
			return
		}
		b = (*bucketPadded)(b.next)
	}
}

func (table *mapTable[K, V]) addSize(bucketIdx uint64, delta int) {
	//nolint:gosec // there is no overflow
	cidx := uint64(len(table.size)-1) & bucketIdx
	atomic.AddInt64(&table.size[cidx].c, int64(delta))
}

func (table *mapTable[K, V]) addSizePlain(bucketIdx uint64, delta int) {
	//nolint:gosec // there is no overflow
	cidx := uint64(len(table.size)-1) & bucketIdx
	table.size[cidx].c += int64(delta)
}

func (table *mapTable[K, V]) sumSize() int64 {
	sum := int64(0)
	for i := range table.size {
		sum += atomic.LoadInt64(&table.size[i].c)
	}
	return sum
}

func h1(h uint64) uint64 {
	return h >> 7
}

func h2(h uint64) uint8 {
	//nolint:gosec // there is no overflow
	return uint8(h & 0x7f)
}

func broadcast(b uint8) uint64 {
	return 0x101010101010101 * uint64(b)
}

func firstMarkedByteIndex(w uint64) int {
	return bits.TrailingZeros64(w) >> 3
}

// SWAR byte search: may produce false positives, e.g. for 0x0100,
// so make sure to double-check bytes found by this function.
func markZeroBytes(w uint64) uint64 {
	return ((w - 0x0101010101010101) & (^w) & 0x8080808080808080)
}

func setByte(w uint64, b uint8, idx int) uint64 {
	shift := idx << 3
	return (w &^ (0xff << shift)) | (uint64(b) << shift)
}