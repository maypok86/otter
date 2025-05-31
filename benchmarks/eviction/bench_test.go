package eviction

import (
	"fmt"
	"math"
	"testing"
	"time"
	_ "unsafe"

	"github.com/maypok86/otter/v2/benchmarks/client"
)

//go:noescape
//go:linkname fastrand runtime.fastrand
func fastrand() uint32

var clients = []client.Client[int, struct{}]{
	&client.Theine[int, struct{}]{},
	&client.Ristretto[int, struct{}]{},
	&client.LRU[int, struct{}]{},
	&client.ARC[int, struct{}]{},
	&client.FIFO[int, struct{}]{},
	&client.Otter[int, struct{}]{},
}

type benchCase struct {
	name      string
	cacheSize int
}

var benchCases = []benchCase{
	// {"capacity=1", 1},
	// {"capacity=100", 100},
	//{"capacity=10000", 10000},
	//{"capacity=1000000", 1000000},
	//{"capacity=10000000", 10000000},
}

func runParallelBenchmark(b *testing.B, benchFunc func(pb *testing.PB)) {
	b.Helper()

	b.ResetTimer()
	start := time.Now()
	b.RunParallel(benchFunc)
	opsPerSec := float64(b.N) / time.Since(start).Seconds()
	b.ReportMetric(opsPerSec, "ops/s")
}

func runCacheBenchmark(
	b *testing.B,
	benchCase benchCase,
	c client.Client[int, struct{}],
) {
	b.Helper()

	c.Init(benchCase.cacheSize)

	for i := 0; i < benchCase.cacheSize; i++ {
		c.Set(math.MinInt+i, struct{}{})
	}

	runParallelBenchmark(b, func(pb *testing.PB) {
		key := int(fastrand())

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
			c.Close()
		}
	}
}
