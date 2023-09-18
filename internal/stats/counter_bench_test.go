package stats

import (
	"sync/atomic"
	"testing"
)

func runBenchCounter(b *testing.B, value func() int64, increment func(), writeRatio int) {
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

func benchmarkCounter(b *testing.B, writeRatio int) {
	b.Helper()
	c := newCounter()
	runBenchCounter(b, func() int64 {
		return c.value()
	}, func() {
		c.increment()
	}, writeRatio)
}

func BenchmarkCounter(b *testing.B) {
	benchmarkCounter(b, 10000)
}

func benchmarkAtomicInt64(b *testing.B, writeRatio int) {
	b.Helper()
	var c int64
	runBenchCounter(b, func() int64 {
		return atomic.LoadInt64(&c)
	}, func() {
		atomic.AddInt64(&c, 1)
	}, writeRatio)
}

func BenchmarkAtomicInt64(b *testing.B) {
	benchmarkAtomicInt64(b, 10000)
}
