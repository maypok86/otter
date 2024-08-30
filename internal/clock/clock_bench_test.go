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

package clock

import (
	"sync/atomic"
	"testing"
	"time"
	_ "unsafe"
)

//go:linkname nanotime runtime.nanotime
func nanotime() int64

func BenchmarkTimeNow(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var ts int64
		for pb.Next() {
			ts += time.Now().UnixNano()
		}
		atomic.StoreInt64(&sink, ts)
	})
}

func BenchmarkNanotime(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var ts int64
		for pb.Next() {
			ts += nanotime()
		}
		atomic.StoreInt64(&sink, ts)
	})
}

func BenchmarkClock(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var ts int64
		c := New()
		for pb.Next() {
			ts += c.Offset()
		}
		atomic.StoreInt64(&sink, ts)
	})
}

// sink should prevent from code elimination by optimizing compiler.
var sink int64
