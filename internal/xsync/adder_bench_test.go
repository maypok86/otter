// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
// Copyright (c) 2021 Andrey Pechkurov
//
// Copyright notice. This code is a fork of benchmarks for xsync.Counter from this file with some changes:
// https://github.com/puzpuzpuz/xsync/blob/main/counter_test.go
//
// Use of this source code is governed by a MIT license that can be found
// at https://github.com/puzpuzpuz/xsync/blob/main/LICENSE

package xsync

import (
	"sync/atomic"
	"testing"
)

func runBenchAdder(b *testing.B, value func() uint64, increment func(), writeRatio int) {
	b.Helper()
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		sink := 0
		for pb.Next() {
			sink++
			if writeRatio > 0 && sink%writeRatio == 0 {
				value()
			} else {
				increment()
			}
		}
		_ = sink
	})
}

func benchmarkAdder(b *testing.B, writeRatio int) {
	b.Helper()
	a := NewAdder()
	runBenchAdder(b, func() uint64 {
		return a.Value()
	}, func() {
		a.Add(1)
	}, writeRatio)
}

func BenchmarkAdder(b *testing.B) {
	benchmarkAdder(b, 10000)
}

func benchmarkAtomicUint64(b *testing.B, writeRatio int) {
	b.Helper()
	var c atomic.Uint64
	runBenchAdder(b, func() uint64 {
		return c.Load()
	}, func() {
		c.Add(1)
	}, writeRatio)
}

func BenchmarkAtomicUint64(b *testing.B) {
	benchmarkAtomicUint64(b, 10000)
}
