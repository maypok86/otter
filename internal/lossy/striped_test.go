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
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xmath"
	"github.com/maypok86/otter/v2/internal/xruntime"
)

var validStatuses = map[Status]bool{
	Success: true,
	Failed:  true,
	Full:    true,
}

func TestNewStriped(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	s := NewStriped(64, nm)
	if got := s.striped.Load(); got != nil {
		t.Fatalf("the striped buffer must be nil, but got: %v", got)
	}
	res := s.Add(nm.Create(1, 2, 100, 0, 1))
	if l := s.striped.Load().len; l != 1 {
		t.Fatalf("the striped buffer length must be 1, but got: %d", l)
	}
	if res != Success {
		t.Fatalf("the status must be success, but got: %v", res)
	}
}

func TestStriped_Add(t *testing.T) {
	t.Parallel()

	parallelism := int(xmath.RoundUpPowerOf2(xruntime.Parallelism()))
	maxBufferLen := 4 * parallelism
	goroutines := 100 * parallelism

	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	s := NewStriped(maxBufferLen, nm)
	n := nm.Create(1, 2, 100, 0, 1)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < 1000; j++ {
				res := s.Add(n)
				if !validStatuses[res] {
					t.Errorf("the status must be valid, but got: %v", res)
					return
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	if l := s.striped.Load().len; l > maxBufferLen {
		t.Fatalf("the striped buffer length must less than %d, but got: %d", maxBufferLen, l)
	}
}

func TestStriped_DrainTo(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	s := NewStriped(64, nm)
	n := nm.Create(1, 2, 100, 0, 1)

	var drains int
	s.DrainTo(func(n node.Node[int, int]) {
		drains++
	})
	if drains != 0 {
		t.Fatalf("Expected number of drained nodes is 0, but got %d", drains)
	}

	res := s.Add(n)
	if l := s.striped.Load().len; l != 1 {
		t.Fatalf("the striped buffer length must be 1, but got: %d", l)
	}
	if res != Success {
		t.Fatalf("the status must be success, but got: %v", res)
	}

	s.DrainTo(func(n node.Node[int, int]) {
		drains++
	})
	if drains != 1 {
		t.Fatalf("Expected number of drained nodes is 1, but got %d", drains)
	}
}

func TestStriped_AddAndDrain(t *testing.T) {
	t.Parallel()

	parallelism := int(xmath.RoundUpPowerOf2(xruntime.Parallelism()))
	maxBufferLen := 4 * parallelism
	goroutines := 100 * parallelism

	nm := node.NewManager[int, int](node.Config{
		WithSize:       true,
		WithExpiration: true,
	})
	s := NewStriped(maxBufferLen, nm)
	n := nm.Create(1, 2, 100, 0, 1)

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		drains atomic.Uint64
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < 1000; j++ {
				res := s.Add(n)
				if !validStatuses[res] {
					t.Errorf("the status must be valid, but got: %v", res)
					return
				}
				if res == Full && mu.TryLock() {
					s.DrainTo(func(n node.Node[int, int]) {
						drains.Add(1)
					})
					mu.Unlock()
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	s.DrainTo(func(n node.Node[int, int]) {
		drains.Add(1)
	})

	if l := s.striped.Load().len; l < 1 || l > maxBufferLen {
		t.Fatalf("the striped buffer length must be in [1, %d], but got: %d", maxBufferLen, l)
	}
}

func TestStriped_Len(t *testing.T) {
	t.Parallel()

	s := &Striped[int, int]{}
	require.Equal(t, 0, s.Len())
	nm := node.NewManager[int, int](node.Config{})
	r := newRing(nm, nm.Create(1, 1, 0, 2, 3))
	ss := &striped[int, int]{
		buffers: make([]atomic.Pointer[ring[int, int]], 2),
		len:     2,
	}
	ss.buffers[0].Store(r)
	s.striped.Store(ss)
	require.Equal(t, 1, s.Len())
}
