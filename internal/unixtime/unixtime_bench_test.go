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

package unixtime

import (
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkNow(b *testing.B) {
	Start()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var ts uint32
		for pb.Next() {
			ts += Now()
		}
		atomic.StoreUint32(&sink, ts)
	})

	Stop()
}

func BenchmarkTimeNowUnix(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var ts uint32
		for pb.Next() {
			ts += uint32(time.Now().Unix())
		}
		atomic.StoreUint32(&sink, ts)
	})
}

// sink should prevent from code elimination by optimizing compiler.
var sink uint32
