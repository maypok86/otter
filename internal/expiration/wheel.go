// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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

package expiration

import (
	"iter"
	"math/bits"
	"time"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xmath"
)

var (
	buckets = []uint64{64, 64, 32, 4, 1}
	spans   = []uint64{
		xmath.RoundUpPowerOf264(uint64((1 * time.Second).Nanoseconds())),             // 1.07s
		xmath.RoundUpPowerOf264(uint64((1 * time.Minute).Nanoseconds())),             // 1.14m
		xmath.RoundUpPowerOf264(uint64((1 * time.Hour).Nanoseconds())),               // 1.22h
		xmath.RoundUpPowerOf264(uint64((24 * time.Hour).Nanoseconds())),              // 1.63d
		buckets[3] * xmath.RoundUpPowerOf264(uint64((24 * time.Hour).Nanoseconds())), // 6.5d
		buckets[3] * xmath.RoundUpPowerOf264(uint64((24 * time.Hour).Nanoseconds())), // 6.5d
	}
	shift = []uint64{
		uint64(bits.TrailingZeros64(spans[0])),
		uint64(bits.TrailingZeros64(spans[1])),
		uint64(bits.TrailingZeros64(spans[2])),
		uint64(bits.TrailingZeros64(spans[3])),
		uint64(bits.TrailingZeros64(spans[4])),
	}
)

// Wheel is a hierarchical timer wheel: it keeps nodes in buckets by their expiration time and
// expires them as its time advances.
type Wheel struct {
	kind  *node.Kind
	wheel [][]*node.Sentinel
	// time is in wheel units, see toWheel.
	time uint64
}

// toWheel converts a time of the cache's clock to the wheel's units. The wheel counts in uint64,
// so that the bucket of a time is a shift and a mask, but a clock may count from an arbitrary
// point, so its readings may be negative and cross zero. Flipping the sign bit maps int64 onto
// uint64 in the same order, while a plain conversion would make a negative deadline that has
// passed look far in the future. The bucket of a time does not change, since its index is taken
// from the low bits.
func toWheel(t int64) uint64 {
	//nolint:gosec // the conversion is the point
	return uint64(t) ^ (1 << 63)
}

// fromWheel is the inverse of toWheel.
func fromWheel(t uint64) int64 {
	//nolint:gosec // the conversion is the point
	return int64(t ^ (1 << 63))
}

func NewWheel(kind *node.Kind) *Wheel {
	wheel := make([][]*node.Sentinel, len(buckets))
	for i := 0; i < len(wheel); i++ {
		wheel[i] = make([]*node.Sentinel, buckets[i])
		for j := 0; j < len(wheel[i]); j++ {
			fn := kind.NewSentinel()
			rh := fn.Header()
			sh := kind.SentinelHeader(fn)
			sh.SetPrevExp(rh)
			sh.SetNextExp(rh)
			wheel[i][j] = fn
		}
	}
	return &Wheel{
		kind:  kind,
		wheel: wheel,
		time:  toWheel(0),
	}
}

// findBucket determines the bucket that the timer event should be added to.
func (w *Wheel) findBucket(expiration uint64) *node.Sentinel {
	var duration uint64
	if expiration > w.time {
		duration = expiration - w.time
	} else {
		// A deadline that the wheel has already passed (e.g. moved earlier while its task was
		// pending) goes to the current bucket, which the next advance sweeps. Without this the
		// unsigned difference wraps and the node lands in the last, ~6.5 day, bucket.
		expiration = w.time
	}
	length := len(w.wheel) - 1
	for i := 0; i < length; i++ {
		if duration < spans[i+1] {
			ticks := expiration >> shift[i]
			index := ticks & (buckets[i] - 1)
			return w.wheel[i][index]
		}
	}
	return w.wheel[length][0]
}

// Schedule puts n in the bucket of its current expiration time, moving it if it is already
// scheduled. A node that is not alive was replaced or removed, so it is unscheduled instead.
// Writes and accesses reach the wheel out of order (maintenance replays the read buffer before the
// write buffer), and Schedule is idempotent, so callers do not need to know whether n is
// scheduled yet.
func (w *Wheel) Schedule(n *node.Header) {
	w.Unschedule(n)
	nh := w.kind.Header(n)
	if !nh.IsAlive() {
		return
	}
	root := w.findBucket(toWheel(nh.ExpiresAt()))
	w.link(root, n)
}

// All returns an iterator over the entries scheduled in the timer wheel, in no particular order.
func (w *Wheel) All() iter.Seq[*node.Header] {
	return func(yield func(*node.Header) bool) {
		for _, bucket := range w.wheel {
			for _, root := range bucket {
				rh := root.Header()
				for n := w.kind.SentinelHeader(root).NextExp(); n != rh; n = w.kind.Header(n).NextExp() {
					if !yield(n) {
						return
					}
				}
			}
		}
	}
}

// Unschedule removes n from the wheel if it is scheduled.
func (w *Wheel) Unschedule(n *node.Header) {
	w.unlink(n)
	nh := w.kind.Header(n)
	nh.SetNextExp(nil)
	nh.SetPrevExp(nil)
}

// Advance moves the wheel's time to nowNanos and calls expire for each node whose expiration time
// is reached. The wheel reads that time before expire runs, and a writer can extend it in
// between: expire reports whether it removed the node, and a node it keeps is scheduled again.
func (w *Wheel) Advance(nowNanos int64, expire func(n *node.Header, nowNanos int64) bool) {
	currentTime := toWheel(nowNanos)
	prevTime := w.time
	w.time = currentTime

	// If expire panics, the wheel goes back to its previous time, so that the next call sweeps
	// the buckets this one did not reach. Otherwise they would be skipped until the wheel wraps
	// around.
	completed := false
	defer func() {
		if !completed {
			w.time = prevTime
		}
	}()

	for i := 0; i < len(shift); i++ {
		previousTicks := prevTime >> shift[i]
		currentTicks := currentTime >> shift[i]
		delta := currentTicks - previousTicks
		if delta == 0 {
			break
		}

		w.advanceBuckets(i, previousTicks, delta, expire)
	}
	completed = true
}

func (w *Wheel) advanceBuckets(
	index int,
	prevTicks, delta uint64,
	expire func(n *node.Header, nowNanos int64) bool,
) {
	mask := buckets[index] - 1
	steps := min(delta+1, buckets[index])
	start := prevTicks & mask
	end := start + steps
	timerWheel := w.wheel[index]
	for i := start; i < end; i++ {
		w.sweepBucket(timerWheel[i&mask], expire)
	}
}

// sweepBucket detaches the bucket's chain and expires or reschedules each of its nodes. If expire
// panics, the nodes not yet visited, and the failing node if it is still alive, are scheduled
// again, so that the wheel keeps every live node it held; one that has expired goes to the level-0
// bucket of the current tick, which the next Advance sweeps first.
func (w *Wheel) sweepBucket(root *node.Sentinel, expire func(n *node.Header, nowNanos int64) bool) {
	rh := root.Header()
	sh := w.kind.SentinelHeader(root)
	n := sh.NextExp()
	sh.SetPrevExp(rh)
	sh.SetNextExp(rh)

	var next *node.Header
	completed := false
	defer func() {
		if completed {
			return
		}
		w.Schedule(n)
		for next != rh {
			rest := next
			rhdr := w.kind.Header(rest)
			next = rhdr.NextExp()
			// still linked into the detached chain, which Schedule must not unlink it from
			rhdr.SetPrevExp(nil)
			rhdr.SetNextExp(nil)
			w.Schedule(rest)
		}
	}()

	for n != rh {
		nh := w.kind.Header(n)
		next = nh.NextExp()
		nh.SetPrevExp(nil)
		nh.SetNextExp(nil)

		// expired at its deadline, as HasExpired reports it
		expired := toWheel(nh.ExpiresAt()) <= w.time
		if !expired || !expire(n, fromWheel(w.time)) {
			w.Schedule(n)
		}

		n = next
	}
	completed = true
}

// link adds the entry at the tail of the bucket's list.
func (w *Wheel) link(root *node.Sentinel, n *node.Header) {
	rh := root.Header()
	sh := w.kind.SentinelHeader(root)
	nh := w.kind.Header(n)
	prev := sh.PrevExp()

	nh.SetPrevExp(prev)
	nh.SetNextExp(rh)

	w.kind.Header(prev).SetNextExp(n)
	sh.SetPrevExp(n)
}

// unlink removes the entry from its bucket, if scheduled.
func (w *Wheel) unlink(n *node.Header) {
	nh := w.kind.Header(n)
	next := nh.NextExp()
	if next != nil {
		prev := nh.PrevExp()
		w.kind.Header(next).SetPrevExp(prev)
		w.kind.Header(prev).SetNextExp(next)
	}
}

// TypedWheel is a strictly typed *node.Entry[K, V] facade over the non-generic Wheel.
type TypedWheel[K comparable, V any] struct {
	raw *Wheel
	nm  *node.Manager[K, V]
}

func NewTypedWheel[K comparable, V any](nm *node.Manager[K, V]) *TypedWheel[K, V] {
	return &TypedWheel[K, V]{
		raw: NewWheel(nm.Kind),
		nm:  nm,
	}
}

func (w *TypedWheel[K, V]) Schedule(n *node.Entry[K, V]) {
	w.raw.Schedule(n.Header())
}

func (w *TypedWheel[K, V]) Unschedule(n *node.Entry[K, V]) {
	w.raw.Unschedule(n.Header())
}

func (w *TypedWheel[K, V]) All() iter.Seq[*node.Entry[K, V]] {
	nm := w.nm
	return func(yield func(*node.Entry[K, V]) bool) {
		for h := range w.raw.All() {
			if !yield(nm.FromHeader(h)) {
				return
			}
		}
	}
}

func (w *TypedWheel[K, V]) Advance(nowNanos int64, expire func(n *node.Entry[K, V], nowNanos int64) bool) {
	nm := w.nm
	w.raw.Advance(nowNanos, func(h *node.Header, now int64) bool {
		return expire(nm.FromHeader(h), now)
	})
}
