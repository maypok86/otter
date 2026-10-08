// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
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

package otter

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/maypok86/otter/v2/internal/deque"
	"github.com/maypok86/otter/v2/internal/generated/node"
)

// validateCache checks the invariants that tie the hash table to the eviction and
// expiration policies. It replays the buffers and holds the eviction lock while checking,
// so all writers and background tasks must be finished before it is called.
func validateCache[K comparable, V any](t testing.TB, c *Cache[K, V]) {
	t.Helper()

	ci := c.cache
	if !ci.withMaintenance {
		return
	}
	ci.evictionMutex.Lock()
	defer ci.evictionMutex.Unlock()
	ci.maintenance(nil)

	var problems []string
	report := func(format string, args ...any) {
		if len(problems) < 20 {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}

	inMap := make(map[unsafe.Pointer]node.Node[K, V])
	ci.hashmap.Range(func(n node.Node[K, V]) bool {
		inMap[n.AsPointer()] = n
		if !n.IsAlive() {
			report("key %v: the node in the hash table is not alive", n.Key())
		}
		return true
	})

	if ci.withEviction {
		p := ci.evictionPolicy
		queued := make(map[unsafe.Pointer]string, len(inMap))
		var weighted, window, protected uint64
		walk := func(name string, d *deque.Linked[K, V], belongs func(n node.Node[K, V]) bool, size *uint64) {
			for n := range d.All() {
				if other, ok := queued[n.AsPointer()]; ok {
					report("key %v: the node is in both %s and %s", n.Key(), other, name)
				}
				queued[n.AsPointer()] = name
				if !belongs(n) {
					report("key %v: the node is in %s, but its queue type is %d", n.Key(), name, n.GetQueueType())
				}
				if !n.IsAlive() {
					report("key %v: a node that is not alive is in %s", n.Key(), name)
				}
				if _, ok := inMap[n.AsPointer()]; !ok {
					report("key %v: the node is in %s, but not in the hash table", n.Key(), name)
				}
				w := uint64(n.Weight())
				weighted += w
				if size != nil {
					*size += w
				}
			}
		}
		walk("window", p.window, func(n node.Node[K, V]) bool { return n.InWindow() }, &window)
		walk("probation", p.probation, func(n node.Node[K, V]) bool { return n.InMainProbation() }, nil)
		walk("protected", p.protected, func(n node.Node[K, V]) bool { return n.InMainProtected() }, &protected)

		for ptr, n := range inMap {
			if _, ok := queued[ptr]; !ok {
				report("key %v: the node is in the hash table, but in no policy queue", n.Key())
			}
		}
		if p.weightedSize != weighted {
			report("weightedSize = %d, but the queued nodes weigh %d", p.weightedSize, weighted)
		}
		if p.windowWeightedSize != window {
			report("windowWeightedSize = %d, but the window nodes weigh %d", p.windowWeightedSize, window)
		}
		if p.mainProtectedWeightedSize != protected {
			report("mainProtectedWeightedSize = %d, but the protected nodes weigh %d", p.mainProtectedWeightedSize, protected)
		}
		if p.weightedSize > p.maximum {
			report("weightedSize = %d exceeds the maximum %d", p.weightedSize, p.maximum)
		}
	}

	if ci.withExpiration {
		scheduled := make(map[unsafe.Pointer]bool, len(inMap))
		for n := range ci.expirationPolicy.All() {
			if scheduled[n.AsPointer()] {
				report("key %v: the node is scheduled in the timer wheel twice", n.Key())
			}
			scheduled[n.AsPointer()] = true
			if !n.IsAlive() {
				report("key %v: a node that is not alive is in the timer wheel", n.Key())
			}
			if _, ok := inMap[n.AsPointer()]; !ok {
				report("key %v: the node is in the timer wheel, but not in the hash table", n.Key())
			}
		}
		for ptr, n := range inMap {
			if !scheduled[ptr] {
				report("key %v: the node is in the hash table, but not in the timer wheel", n.Key())
			}
		}
	}

	if len(problems) > 0 {
		t.Fatalf("cache invariants are violated:\n%s", strings.Join(problems, "\n"))
	}
}

// trackingExecutor runs tasks on new goroutines and lets a test wait until all of them,
// including the tasks they schedule themselves, have finished.
type trackingExecutor struct {
	wg sync.WaitGroup
}

func (e *trackingExecutor) execute(fn func()) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		fn()
	}()
}

func TestCache_InvariantsAfterConcurrentLoad(t *testing.T) {
	t.Parallel()

	configs := []struct {
		name string
		opts func(o *Options[int, int])
	}{
		{"size", func(o *Options[int, int]) { o.MaximumSize = 100 }},
		{"weight", func(o *Options[int, int]) {
			o.MaximumWeight = 500
			o.Weigher = func(key, value int) uint32 { return uint32(value%8 + 1) }
		}},
		{"size_expiry", func(o *Options[int, int]) {
			o.MaximumSize = 100
			o.ExpiryCalculator = ExpiryWriting[int, int](20 * time.Millisecond)
		}},
		{"size_expiry_refresh", func(o *Options[int, int]) {
			o.MaximumSize = 100
			o.ExpiryCalculator = ExpiryAccessing[int, int](50 * time.Millisecond)
			o.RefreshCalculator = RefreshWriting[int, int](10 * time.Millisecond)
		}},
		{"expiry", func(o *Options[int, int]) {
			o.ExpiryCalculator = ExpiryWriting[int, int](20 * time.Millisecond)
		}},
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			t.Parallel()

			exec := &trackingExecutor{}
			o := &Options[int, int]{Executor: exec.execute}
			cfg.opts(o)
			c := Must(o)

			loader := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
				return key, nil
			})

			const (
				goroutines = 8
				keys       = 400
			)
			ctx := context.Background()
			deadline := time.Now().Add(150 * time.Millisecond)
			var wg sync.WaitGroup
			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r := rand.New(rand.NewPCG(uint64(g), 42))
					for time.Now().Before(deadline) {
						// a skewed key space, so that some keys are written back to back
						k := r.IntN(keys) % (1 + r.IntN(keys))
						v := r.IntN(1000)
						switch op := r.IntN(100); {
						case op < 30:
							c.Set(k, v)
						case op < 40:
							c.SetIfAbsent(k, v)
						case op < 55:
							c.Compute(k, func(old int, found bool) (int, ComputeOp) {
								return v, ComputeOp(v % 3)
							})
						case op < 65:
							c.Invalidate(k)
						case op < 80:
							_, _ = c.Get(ctx, k, loader)
						default:
							c.GetIfPresent(k)
						}
					}
				}()
			}
			wg.Wait()
			exec.wg.Wait()

			validateCache(t, c)
		})
	}
}

// A value whose expiration time has not passed must never be removed as expired, even if
// maintenance expires the previous value of the same key concurrently with the write.
func TestCache_ExpirationKeepsFreshWrites(t *testing.T) {
	t.Parallel()

	for _, withSize := range []bool{false, true} {
		t.Run(fmt.Sprintf("size=%v", withSize), func(t *testing.T) {
			t.Parallel()

			const (
				writers = 4
				keys    = 64
				ttl     = 2 * time.Second
			)
			clk := newNonTickingClock()
			// Every value is the clock reading taken right before it was written, a lower
			// bound of the write time the cache used, so its expiration time is at least
			// value + ttl.
			var violations atomic.Int64
			var first atomic.Pointer[string]
			o := &Options[int, int64]{
				Clock:            clk,
				ExpiryCalculator: ExpiryWriting[int, int64](ttl),
				OnDeletion: func(e DeletionEvent[int, int64]) {
					if e.Cause != CauseExpiration && e.Cause != CauseOverflow {
						return
					}
					if now := clk.NowNano(); e.Value+int64(ttl) > now {
						violations.Add(1)
						msg := fmt.Sprintf("key %d: a value written %v ago with a TTL of %v was removed (%v)",
							e.Key, time.Duration(now-e.Value), ttl, e.Cause)
						first.CompareAndSwap(nil, &msg)
					}
				},
			}
			if withSize {
				// large enough that nothing is evicted by size
				o.MaximumSize = 10 * keys
			}
			c := Must(o)

			var stop atomic.Bool
			var wg sync.WaitGroup
			for w := 0; w < writers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; !stop.Load(); i++ {
						c.Set(w+(i%(keys/writers))*writers, clk.NowNano())
					}
				}()
			}
			for step := 0; step < 40; step++ {
				clk.Sleep(250 * time.Millisecond)
				c.CleanUp()
			}
			stop.Store(true)
			wg.Wait()
			c.CleanUp()

			if n := violations.Load(); n > 0 {
				t.Fatalf("%d fresh values were removed as expired, first: %s", n, *first.Load())
			}
			validateCache(t, c)
		})
	}
}
