// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
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

package expire

import (
	"github.com/dolthub/swiss"

	"github.com/maypok86/otter/internal/node"
	"github.com/maypok86/otter/internal/unixtime"
)

const (
	numberOfBuckets = 128
	mask            = uint32(numberOfBuckets - 1)
	base            = uint32(5)

	// Eliminate probing settings.
	maxProbeCount = 100
	maxFailCount  = 3

	mapSize = 100
)

func bucketTimeToBucketID(bucketTime uint32) int {
	return int(bucketTime & mask)
}

func timestampToBucketTime(timestamp uint32) uint32 {
	return timestamp / base
}

func nextBucketID(bucketID int) int {
	return (bucketID + 1) & int(mask)
}

type bucket[K comparable, V any] struct {
	m        *swiss.Map[*node.Node[K, V], struct{}]
	lastTime uint32
}

func newBucket[K comparable, V any]() bucket[K, V] {
	return bucket[K, V]{
		m:        swiss.NewMap[*node.Node[K, V], struct{}](mapSize),
		lastTime: 0,
	}
}

func (b *bucket[K, V]) add(n *node.Node[K, V], newTime uint32) {
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

// Policy is an expiration policy for arbitrary TTL values,
// using a hybrid algorithm combining a deterministic one using buckets and a randomized one inherited from Redis expiration algorithm.
// https://dl.acm.org/doi/fullHtml/10.1145/3422575.3422797
type Policy[K comparable, V any] struct {
	buckets         [numberOfBuckets]bucket[K, V]
	expires         *swiss.Map[*node.Node[K, V], struct{}]
	currentBucketID int
}

// NewPolicy creates a new Policy with 128 buckets.
func NewPolicy[K comparable, V any]() *Policy[K, V] {
	p := &Policy[K, V]{
		expires: swiss.NewMap[*node.Node[K, V], struct{}](mapSize),
	}

	for i := 0; i < numberOfBuckets; i++ {
		p.buckets[i] = newBucket[K, V]()
	}

	return p
}

// Add adds node.Node to Policy if it has a TTL specified.
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

// Delete removes node.Node from Policy if it has a TTL specified.
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

// RemoveExpired removes the expired node.Node from Policy.
// Buckets are checked first, and then a redis randomized algorithm is applied to lazily find the remaining expired nodes.
func (p *Policy[K, V]) RemoveExpired(expired []*node.Node[K, V]) []*node.Node[K, V] {
	now := unixtime.Now()
	for i := 0; i < numberOfBuckets; i++ {
		expired = p.expireBucket(expired, p.currentBucketID, now)
		p.currentBucketID = nextBucketID(p.currentBucketID)
	}

	return p.probingExpire(expired)
}

func (p *Policy[K, V]) expireBucket(expired []*node.Node[K, V], bucketID int, now uint32) []*node.Node[K, V] {
	b := &p.buckets[bucketID]
	if b.lastTime == 0 {
		return expired
	}

	now = timestampToBucketTime(now)
	if now > b.lastTime {
		b.m.Iter(func(n *node.Node[K, V], _ struct{}) (stop bool) {
			p.expires.Delete(n)
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

// Clear completely clears Policy and returns it to its default state.
func (p *Policy[K, V]) Clear() {
	p.currentBucketID = 0
	p.expires.Clear()
	for i := 0; i < numberOfBuckets; i++ {
		p.buckets[i].clear()
	}
}
