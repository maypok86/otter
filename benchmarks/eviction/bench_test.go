package eviction

import (
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"testing"
	"time"
	_ "unsafe"

	"github.com/maypok86/otter/v2/benchmarks/client"
)

var clients = []client.Client[int, struct{}]{
	&client.Otter[int, struct{}]{},
	&client.Theine[int, struct{}]{},
	&client.Ristretto[int, struct{}]{},
	&client.GolangLRU[int, struct{}]{},
	&client.Gcache[int, struct{}]{},
	&client.TTLCache[int, struct{}]{},
}

type benchCase struct {
	name      string
	cacheSize int
}

var benchCases = []benchCase{
	//{"capacity=100", 100},
	//{"capacity=10000", 10000},
	{"capacity=1000000", 1000000},
}

func runParallelBenchmark(b *testing.B, benchFunc func(pb *testing.PB)) {
	b.Helper()

	// Collect the garbage left by the previous benchmark, so that
	// this one doesn't pay for it.
	runtime.GC()
	b.ResetTimer()
	b.ReportAllocs()
	start := time.Now()
	b.RunParallel(benchFunc)
	opsPerSec := float64(b.N) / time.Since(start).Seconds()
	b.StopTimer()
	b.ReportMetric(opsPerSec, "ops/s")
}

func runCacheBenchmark(
	b *testing.B,
	benchCase benchCase,
	c client.Client[int, struct{}],
) {
	b.Helper()

	c.Init(benchCase.cacheSize)
	defer c.Close()

	for i := 0; i < benchCase.cacheSize; i++ {
		c.Set(math.MinInt+i, struct{}{})
	}
	// Fill the cache before the measurement, so that every insert evicts.
	if w, ok := c.(client.Waiter); ok {
		w.Wait()
	}

	runParallelBenchmark(b, func(pb *testing.PB) {
		key := int(rand.Uint32())

		for pb.Next() {
			c.Set(key, struct{}{})
			key++
		}
	})
}

func BenchmarkCache(b *testing.B) {
	for _, benchCase := range benchCases {
		for _, c := range clients {
			name := fmt.Sprintf("%s_%s", c.Name(), benchCase.name)
			b.Run(name, func(b *testing.B) {
				runCacheBenchmark(b, benchCase, c)
			})
		}
	}
}
