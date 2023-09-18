package unixtime

import (
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkNow(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var ts uint64
		for pb.Next() {
			ts += Now()
		}
		atomic.StoreUint64(&sink, ts)
	})
}

func BenchmarkTimeNowUnix(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var ts uint64
		for pb.Next() {
			ts += uint64(time.Now().Unix())
		}
		atomic.StoreUint64(&sink, ts)
	})
}

// sink should prevent from code elimination by optimizing compiler.
var sink uint64
