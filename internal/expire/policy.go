package expire

import (
	"github.com/dolthub/swiss"

	"github.com/maypok86/otter/internal/node"
	"github.com/maypok86/otter/internal/unixtime"
)

const (
	numberOfBuckets = 128
	mask            = uint64(numberOfBuckets - 1)
	base            = uint64(5)

	// eliminate probing.
	maxProbeCount = 100
	maxFailCount  = 3

	mapSize = 100
)

func bucketTimeToBucketID(bucketTime uint64) int {
	return int(bucketTime & mask)
}

func timestampToBucketTime(timestamp uint64) uint64 {
	return timestamp / base
}

func nextBucketID(bucketID int) int {
	return (bucketID + 1) & int(mask)
}

type bucket[K comparable, V any] struct {
	m        *swiss.Map[*node.Node[K, V], struct{}]
	lastTime uint64
}

func newBucket[K comparable, V any]() bucket[K, V] {
	return bucket[K, V]{
		m:        swiss.NewMap[*node.Node[K, V], struct{}](mapSize),
		lastTime: 0,
	}
}

func (b *bucket[K, V]) add(n *node.Node[K, V], newTime uint64) {
	if b.isEmpty() {
		b.m.Put(n, struct{}{})
		b.lastTime = newTime
		return
	}

	if newTime > b.lastTime {
		return
	}

	b.m.Put(n, struct{}{})
}

func (b *bucket[K, V]) delete(n *node.Node[K, V]) {
	b.m.Delete(n)
	if b.m.Count() == 0 {
		b.lastTime = 0
	}
}

func (b *bucket[K, V]) isEmpty() bool {
	return b.lastTime == 0
}

func (b *bucket[K, V]) clear() {
	b.m.Clear()
	b.lastTime = 0
}

type Policy[K comparable, V any] struct {
	removeNode      func(n *node.Node[K, V])
	buckets         [numberOfBuckets]bucket[K, V]
	expires         *swiss.Map[*node.Node[K, V], struct{}]
	currentBucketID int
}

func NewPolicy[K comparable, V any](removeNode func(n *node.Node[K, V])) *Policy[K, V] {
	p := &Policy[K, V]{
		removeNode: removeNode,
		expires:    swiss.NewMap[*node.Node[K, V], struct{}](mapSize),
	}

	for i := 0; i < numberOfBuckets; i++ {
		p.buckets[i] = newBucket[K, V]()
	}

	return p
}

func (p *Policy[K, V]) Add(n *node.Node[K, V]) {
	expiration := n.Expiration()
	if expiration == 0 {
		return
	}

	bucketTime := timestampToBucketTime(expiration)
	bucketID := bucketTimeToBucketID(bucketTime)

	p.expires.Put(n, struct{}{})
	p.buckets[bucketID].add(n, bucketTime)
}

func (p *Policy[K, V]) Delete(n *node.Node[K, V]) {
	expiration := n.Expiration()
	if expiration == 0 {
		return
	}

	bucketTime := timestampToBucketTime(expiration)
	bucketID := bucketTimeToBucketID(bucketTime)

	p.expires.Delete(n)
	p.buckets[bucketID].delete(n)
}

func (p *Policy[K, V]) RemoveExpired(expired []*node.Node[K, V]) []*node.Node[K, V] {
	now := unixtime.Now()
	for i := 0; i < numberOfBuckets; i++ {
		expired = p.expireBucket(expired, p.currentBucketID, now)
		p.currentBucketID = nextBucketID(p.currentBucketID)
	}

	return p.probingExpire(expired)
}

func (p *Policy[K, V]) expireBucket(expired []*node.Node[K, V], bucketID int, now uint64) []*node.Node[K, V] {
	b := &p.buckets[bucketID]
	if b.lastTime == 0 {
		return expired
	}

	now = timestampToBucketTime(now)
	if now > b.lastTime {
		b.m.Iter(func(n *node.Node[K, V], _ struct{}) (stop bool) {
			p.expires.Delete(n)
			p.removeNode(n)
			expired = append(expired, n)
			return false
		})
		b.clear()
	}

	return expired
}

func (p *Policy[K, V]) probingExpire(expired []*node.Node[K, V]) []*node.Node[K, V] {
	failCount := 0
	probeCount := 0
	p.expires.Iter(func(n *node.Node[K, V], _ struct{}) (stop bool) {
		if n.IsExpired() {
			p.expires.Delete(n)
			expired = append(expired, n)
			failCount = 0
			return false
		}

		failCount++
		if failCount > maxFailCount {
			return true
		}

		probeCount++
		return probeCount > maxProbeCount
	})

	return expired
}

func (p *Policy[K, V]) Clear() {
	p.currentBucketID = 0
	p.expires.Clear()
	for i := 0; i < numberOfBuckets; i++ {
		p.buckets[i].clear()
	}
}
