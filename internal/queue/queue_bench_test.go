// Copyright (c) 2024 Alexey Mayshev. All rights reserved.
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
	"sync/atomic"
	"testing"
)

func benchmarkProdCons(b *testing.B, push func(int), pop func() int, queueSize, localWork int) {
	b.Helper()

	callsPerSched := queueSize
	procs := runtime.GOMAXPROCS(-1) / 2
	if procs == 0 {
		procs = 1
	}
	N := int32(b.N / callsPerSched)
	c := make(chan bool, 2*procs)
	for p := 0; p < procs; p++ {
		go func() {
			foo := 0
			for atomic.AddInt32(&N, -1) >= 0 {
				for g := 0; g < callsPerSched; g++ {
					for i := 0; i < localWork; i++ {
						foo *= 2
						foo /= 2
					}
					push(1)
				}
			}
			push(0)
			c <- foo == 42
		}()
		go func() {
			foo := 0
			for {
				v := pop()
				if v == 0 {
					break
				}
				for i := 0; i < localWork; i++ {
					foo *= 2
					foo /= 2
				}
			}
			c <- foo == 42
		}()
	}
	for p := 0; p < procs; p++ {
		<-c
		<-c
	}
}

func BenchmarkGrowableProdConsWork100(b *testing.B) {
	length := 128 * 16
	g := NewGrowable[int](uint32(length), uint32(length))
	b.ResetTimer()
	benchmarkProdCons(b, func(i int) {
		g.Push(i)
	}, func() int {
		return g.Pop()
	}, length, 100)
}

func BenchmarkChanProdConsWork100(b *testing.B) {
	length := 128 * 16
	c := make(chan int, length)
	benchmarkProdCons(b, func(i int) {
		c <- i
	}, func() int {
		return <-c
	}, length, 100)
}
