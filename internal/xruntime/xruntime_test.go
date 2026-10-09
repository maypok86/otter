// Copyright (c) 2026 Alexey Mayshev and contributors. All rights reserved.
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

package xruntime

import (
	"fmt"
	"math"
	"sync"
	"testing"
)

func TestStackHash(t *testing.T) {
	t.Parallel()

	if a, b := StackHash(), StackHash(); a != b {
		t.Fatalf("a goroutine must keep its hash, but got %d and %d", a, b)
	}

	// Goroutines that are alive at the same time have different stacks, so their hashes
	// must be spread rather than collapse into a few values.
	const goroutines = 64
	hashes := make([]uint32, goroutines)
	var started, release sync.WaitGroup
	started.Add(goroutines)
	release.Add(1)
	for i := 0; i < goroutines; i++ {
		go func() {
			hashes[i] = StackHash()
			started.Done()
			release.Wait()
		}()
	}
	started.Wait()
	release.Done()

	stripes := make(map[uint32]struct{})
	for _, h := range hashes {
		stripes[h&(goroutines-1)] = struct{}{}
	}
	// 64 goroutines over 64 stripes land in about 40 distinct ones when the hashes are
	// uniform; a hash that ignored the stack would land in one.
	if len(stripes) < goroutines/4 {
		t.Fatalf("the hashes of %d live goroutines must spread over at least %d of %d stripes, but got %d",
			goroutines, goroutines/4, goroutines, len(stripes))
	}
}

func TestHashStack_SpreadsAlignedStacks(t *testing.T) {
	t.Parallel()

	// Goroutine stacks have power-of-two sizes and are aligned to them, so neighboring
	// stacks are often a large power of two apart.
	const base = uintptr(0x1400_0010_0000 & uint64(^uintptr(0)))
	for _, step := range []uintptr{2 << 10, 4 << 10, 8 << 10, 16 << 10, 32 << 10, 64 << 10, 1 << 20} {
		for _, goroutines := range []int{8, 12, 64} {
			for _, stripes := range []uint32{16, 64} {
				t.Run(fmt.Sprintf("step=%d/goroutines=%d/stripes=%d", step, goroutines, stripes), func(t *testing.T) {
					t.Parallel()

					used := make(map[uint32]struct{})
					for i := 0; i < goroutines; i++ {
						used[hashStack(base+uintptr(i)*step)&(stripes-1)] = struct{}{}
					}
					// the number of distinct stripes that uniformly random hashes would use
					uniform := float64(stripes) * (1 - math.Pow(1-1/float64(stripes), float64(goroutines)))
					if float64(len(used)) < uniform/2 {
						t.Fatalf("the hashes must use at least half of the %.1f stripes that random hashes would, but used %d",
							uniform, len(used))
					}
				})
			}
		}
	}
}

func TestStackHash_DoesNotAllocate(t *testing.T) {
	// The hash relies on the variable whose address it takes staying on the stack.
	if allocs := testing.AllocsPerRun(100, func() { _ = StackHash() }); allocs != 0 {
		t.Fatalf("StackHash must not allocate, but allocated %v times per call", allocs)
	}
}
