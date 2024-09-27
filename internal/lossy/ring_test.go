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

package lossy

import (
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

func TestRing_Add(t *testing.T) {
	parallelism := int(xmath.RoundUpPowerOf2(xruntime.Parallelism()))
	goroutines := 100 * parallelism

	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	n := nm.Create(1, 2, 100, 1)
	r := &ring[int, int]{
		nodeManager: nm,
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < 1000; j++ {
				res := r.add(n)
				if !validStatuses[res] {
					t.Errorf("the status must be valid, but got: %v", res)
					return
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	tail := r.tail.Load()
	if tail <= 0 {
		t.Fatalf("the tail must be greater than 0, but got %d", tail)
	}
	if size := tail - r.head.Load(); tail != size {
		t.Fatalf("the tail must be equal to %d, but got %d", size, tail)
	}
}

func TestRing_DrainTo(t *testing.T) {
	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	n := nm.Create(1, 2, 100, 1)
	r := &ring[int, int]{
		nodeManager: nm,
	}

	for i := 0; i < bufferSize; i++ {
		res := r.add(n)
		if res != Success && res != Full {
			t.Fatalf("the status must be Success or Full, but got: %v", res)
		}
	}

	reads := uint64(0)
	r.drainTo(func(n node.Node[int, int]) {
		reads++
	})
	if tail := r.tail.Load(); reads != tail {
		t.Fatalf("the tail must be equal to %d, but got %d", reads, tail)
	}
	if head := r.head.Load(); reads != head {
		t.Fatalf("the head must be equal to %d, but got %d", reads, head)
	}
}

func TestRing_AddAndDrain(t *testing.T) {
	parallelism := int(xmath.RoundUpPowerOf2(xruntime.Parallelism()))
	goroutines := 100 * parallelism

	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	n := nm.Create(1, 2, 100, 1)
	r := &ring[int, int]{
		nodeManager: nm,
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		reads atomic.Uint64
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < 1000; j++ {
				res := r.add(n)
				if !validStatuses[res] {
					t.Errorf("the status must be valid, but got: %v", res)
					return
				}
				if res == Full && mu.TryLock() {
					r.drainTo(func(n node.Node[int, int]) {
						reads.Add(1)
					})
					mu.Unlock()
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	r.drainTo(func(n node.Node[int, int]) {
		reads.Add(1)
	})

	rReads := reads.Load()
	if tail := r.tail.Load(); rReads != tail {
		t.Fatalf("the tail must be equal to %d, but got %d", rReads, tail)
	}
	if head := r.head.Load(); rReads != head {
		t.Fatalf("the head must be equal to %d, but got %d", rReads, head)
	}
}

func TestRing_Overflow(t *testing.T) {
	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	n := nm.Create(1, 2, 100, 1)
	r := &ring[int, int]{
		nodeManager: nm,
	}
	r.head.Store(math.MaxUint64)
	r.tail.Store(math.MaxUint64)

	res := r.add(n)
	if res != Success {
		t.Fatalf("the status must be Success, but got: %v", res)
	}

	var d []node.Node[int, int]
	r.drainTo(func(n node.Node[int, int]) {
		d = append(d, n)
	})

	for i := 0; i < bufferSize; i++ {
		ptr := r.buffer[i]
		if ptr != nil {
			t.Fatalf("the buffer should contain only nils, but buffer[%d] = %v", i, ptr)
		}
	}
	if tail := r.tail.Load(); tail != 0 {
		t.Fatalf("the tail must be equal to 0, but got %d", tail)
	}
	if head := r.head.Load(); head != 0 {
		t.Fatalf("the head must be equal to 0, but got %d", head)
	}
}
