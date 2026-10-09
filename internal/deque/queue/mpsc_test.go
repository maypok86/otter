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

package queue

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	numProducers  = 10
	produce       = 100
	populatedSize = 10
	fullSize      = 32
)

type queueType uint8

const (
	empty queueType = iota
	populated
	full
)

var getQueue = map[queueType]func() *MPSC[int]{
	empty: func() *MPSC[int] {
		return newPopulated(0)
	},
	populated: func() *MPSC[int] {
		return newPopulated(populatedSize)
	},
	full: func() *MPSC[int] {
		return newPopulated(fullSize)
	},
}

func ptr[T any](t T) *T {
	return &t
}

func newPopulated(size int) *MPSC[int] {
	m := NewMPSC[int](4, fullSize)
	for i := 0; i < size; i++ {
		m.TryPush(&i)
	}
	return m
}

func TestNewMPSC(t *testing.T) {
	t.Run("initialCapacity_tooSmall", func(t *testing.T) {
		require.Panics(t, func() {
			NewMPSC[int](1, 4)
		})
	})
	t.Run("maxCapacity_tooSmall", func(t *testing.T) {
		require.Panics(t, func() {
			NewMPSC[int](4, 1)
		})
	})
	t.Run("inverted", func(t *testing.T) {
		require.Panics(t, func() {
			NewMPSC[int](8, 4)
		})
	})
	t.Run("ok", func(t *testing.T) {
		m := NewMPSC[int](4, 8)
		require.Equal(t, 8, m.capacity())
	})
}

func TestMPSC_Basic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		qType queueType
		do    func(t *testing.T, m *MPSC[int])
	}{
		{
			name:  "size_whenEmpty",
			qType: empty,
			do: func(t *testing.T, m *MPSC[int]) {
				require.Equal(t, uint64(0), m.Size())
			},
		},
		{
			name:  "size_whenEmpty",
			qType: populated,
			do: func(t *testing.T, m *MPSC[int]) {
				require.Equal(t, uint64(populatedSize), m.Size())
			},
		},
		{
			name:  "tryPush_whenEmpty",
			qType: empty,
			do: func(t *testing.T, m *MPSC[int]) {
				require.True(t, m.TryPush(ptr(1)))
				require.Equal(t, uint64(1), m.Size())
			},
		},
		{
			name:  "tryPush_whenPopulated",
			qType: populated,
			do: func(t *testing.T, m *MPSC[int]) {
				require.True(t, m.TryPush(ptr(1)))
				require.Equal(t, uint64(populatedSize+1), m.Size())
			},
		},
		{
			name:  "tryPush_whenFull",
			qType: full,
			do: func(t *testing.T, m *MPSC[int]) {
				require.False(t, m.TryPush(ptr(1)))
				require.Equal(t, uint64(fullSize), m.Size())
			},
		},
		{
			name:  "tryPop_whenEmpty",
			qType: empty,
			do: func(t *testing.T, m *MPSC[int]) {
				require.Nil(t, m.TryPop())
			},
		},
		{
			name:  "tryPop_whenPopulated",
			qType: populated,
			do: func(t *testing.T, m *MPSC[int]) {
				require.NotNil(t, m.TryPop())
				require.Equal(t, uint64(populatedSize-1), m.Size())
			},
		},
		{
			name:  "tryPop_whenFull",
			qType: full,
			do: func(t *testing.T, m *MPSC[int]) {
				require.NotNil(t, m.TryPop())
				require.Equal(t, uint64(fullSize-1), m.Size())
			},
		},
		{
			name:  "tryPop_toEmpty",
			qType: full,
			do: func(t *testing.T, m *MPSC[int]) {
				for m.TryPop() != nil {
				}
				require.Equal(t, uint64(0), m.Size())
				require.True(t, m.IsEmpty())
			},
		},
		{
			name:  "inspection",
			qType: populated,
			do: func(t *testing.T, m *MPSC[int]) {
				require.Equal(t, uint64(0), m.consumerIndex.Load()/2)
				require.Equal(t, uint64(populatedSize), m.producerIndex.Load()/2)
			},
		},
		{
			name:  "getNextBufferSize_invalid",
			qType: full,
			do: func(t *testing.T, m *MPSC[int]) {
				require.Panics(t, func() {
					b := newBuffer(fullSize + 1)
					m.getNextBufferSize(b)
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := getQueue[tt.qType]()
			tt.do(t, m)
		})
	}
}

func TestMPSC_Concurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		qType queueType
		do    func(t *testing.T, m *MPSC[int])
	}{
		{
			name:  "oneProducer_oneConsumer",
			qType: empty,
			do: func(t *testing.T, m *MPSC[int]) {
				var (
					started atomic.Uint64
					wg      sync.WaitGroup
				)
				wg.Add(2)

				go func() {
					defer wg.Done()

					started.Add(1)
					for started.Load() != 2 {
					}

					for i := 0; i < produce; i++ {
						for !m.TryPush(ptr(i)) {
						}
					}
				}()

				go func() {
					defer wg.Done()

					started.Add(1)
					for started.Load() != 2 {
					}

					for i := 0; i < produce; i++ {
						for m.TryPop() == nil {
						}
					}
				}()

				wg.Wait()
				require.True(t, m.IsEmpty())
			},
		},
		{
			name:  "manyProducers_noConsumer",
			qType: empty,
			do: func(t *testing.T, m *MPSC[int]) {
				var (
					count atomic.Uint64
					wg    sync.WaitGroup
				)

				wg.Add(numProducers)
				for i := 0; i < numProducers; i++ {
					go func() {
						defer wg.Done()

						for i := 0; i < produce; i++ {
							if m.TryPush(ptr(i)) {
								count.Add(1)
							}
						}
					}()
				}

				wg.Wait()
				require.Equal(t, count.Load(), m.Size())
			},
		},
		{
			name:  "manyProducers_oneConsumer",
			qType: empty,
			do: func(t *testing.T, m *MPSC[int]) {
				var (
					started atomic.Uint64
					wg      sync.WaitGroup
				)

				wg.Add(numProducers + 1)
				for i := 0; i < numProducers; i++ {
					go func() {
						defer wg.Done()

						started.Add(1)
						for started.Load() != numProducers+1 {
						}

						for i := 0; i < produce; i++ {
							for !m.TryPush(ptr(i)) {
							}
						}
					}()
				}

				go func() {
					defer wg.Done()

					started.Add(1)
					for started.Load() != numProducers+1 {
					}

					for i := 0; i < numProducers*produce; i++ {
						for m.TryPop() == nil {
						}
					}
				}()

				wg.Wait()
				require.True(t, m.IsEmpty())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := getQueue[tt.qType]()
			tt.do(t, m)
		})
	}
}

func hammerMPSCBlockingCalls(t *testing.T, gomaxprocs, numOps, numThreads int) {
	runtime.GOMAXPROCS(gomaxprocs)
	q := getQueue[empty]()
	startwg := sync.WaitGroup{}
	startwg.Add(1)
	csum := make(chan int, 1)
	// Start producers.
	for i := 0; i < numThreads; i++ {
		go func(n int) {
			startwg.Wait()
			for j := n; j < numOps; j += numThreads {
				p := ptr(j)
				for !q.TryPush(p) {
				}
			}
		}(i)
	}
	// Start consumer.
	go func() {
		startwg.Wait()
		sum := 0
		for i := 0; i < numOps; i++ {
			var item int
			for {
				v := q.TryPop()
				if v != nil {
					item = *v
					break
				}
			}
			sum += item
		}
		csum <- sum
	}()
	startwg.Done()
	// Wait for the sum from the consumer.
	sum := <-csum
	// Assert the sum.
	expectedSum := numOps * (numOps - 1) / 2
	if sum != expectedSum {
		t.Fatalf("sums don't match for %d num ops, %d num threads: got %d, want %d",
			numOps, numThreads, sum, expectedSum)
	}
}

func TestMPSC_BlockingCalls(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(-1))
	n := 10
	hammerMPSCBlockingCalls(t, 1, n, n)
	hammerMPSCBlockingCalls(t, 2, 10*n, 2*n)
	hammerMPSCBlockingCalls(t, 4, 100*n, 4*n)
}

func TestMPSC_KeepsTheOrderOfEachProducer(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(-1))

	type item struct {
		producer int
		seq      int
	}

	const (
		producers   = 8
		perProducer = 5000
	)
	for _, procs := range []int{1, 4, runtime.NumCPU()} {
		// A small maximum keeps the queue full most of the time; a large one makes it grow
		// through several buffers while the consumer follows the links.
		for _, maxCapacity := range []uint32{4, 1024} {
			runtime.GOMAXPROCS(procs)
			q := NewMPSC[item](2, maxCapacity)

			var wg sync.WaitGroup
			wg.Add(producers)
			for p := 0; p < producers; p++ {
				go func() {
					defer wg.Done()

					for i := 0; i < perProducer; i++ {
						it := &item{producer: p, seq: i}
						for !q.TryPush(it) {
							runtime.Gosched()
						}
					}
				}()
			}

			next := make([]int, producers)
			lastPop := time.Now()
			for popped := 0; popped < producers*perProducer; {
				it := q.TryPop()
				if it == nil {
					if time.Since(lastPop) > 10*time.Second {
						t.Fatalf("GOMAXPROCS=%d, maxCapacity=%d: no item for 10s after %d of %d",
							procs, maxCapacity, popped, producers*perProducer)
					}
					runtime.Gosched()
					continue
				}
				lastPop = time.Now()
				if it.seq != next[it.producer] {
					t.Fatalf("GOMAXPROCS=%d, maxCapacity=%d: producer %d: got item %d, want %d",
						procs, maxCapacity, it.producer, it.seq, next[it.producer])
				}
				next[it.producer]++
				popped++
			}
			wg.Wait()

			require.Nil(t, q.TryPop())
			require.True(t, q.IsEmpty())
			require.Equal(t, uint64(0), q.Size())
		}
	}
}
