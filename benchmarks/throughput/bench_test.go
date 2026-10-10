package throughput

import (
	"fmt"
	"math/rand"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pingcap/go-ycsb/pkg/generator"

	"github.com/maypok86/otter/v2/benchmarks/client"
)

const dataLength = 2 << 14

var (
	values []string
	datas  []data

	clients = []client.Client[string, string]{
		&client.Otter[string, string]{},
		&client.Theine[string, string]{},
		&client.Ristretto[string, string]{},
		&client.Sturdyc[string]{},
		&client.Ccache[string]{},
		&client.Gcache[string, string]{},
		&client.TTLCache[string, string]{},
		&client.GolangLRU[string, string]{},
	}
)

func init() {
	values = make([]string, 0, dataLength)
	for i := 0; i < dataLength; i++ {
		v := newValue(strconv.Itoa(i))
		values = append(values, v)
	}

	datas = []data{
		newZipfData(),
	}
}

func newKey(k string) string {
	return "0i02-3rj203rn230rjx0m238ex10eu1-x n-9u" + k
}

func newValue(v string) string {
	return "ololololooooooookkeeeke_9njijuinugyih" + v
}

type benchCase struct {
	readPercentage int
	setPercentage  uint64
}

var benchCases = []benchCase{
	{100, 0},
	{75, 25},
	{50, 50},
	{25, 75},
	{0, 100},
}

type data struct {
	name string
	keys []string
}

func newZipfData() data {
	// populate using realistic distribution
	z := generator.NewScrambledZipfian(0, dataLength/3, generator.ZipfianConstant)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	keys := make([]string, 0, dataLength)
	for i := 0; i < dataLength; i++ {
		k := newKey(strconv.Itoa(int(z.Next(r))))
		keys = append(keys, k)
	}

	return data{
		name: "zipf",
		keys: keys,
	}
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
	keys []string,
	c client.Client[string, string],
) {
	b.Helper()

	c.Init(dataLength)
	defer c.Close()

	for i := 0; i < dataLength; i++ {
		c.Set(keys[i], values[i])
	}
	// Let the prepopulated entries land before the measurement starts.
	if w, ok := c.(client.Waiter); ok {
		w.Wait()
	}

	var (
		rc     uint64
		hits   atomic.Uint64
		misses atomic.Uint64
	)
	mask := dataLength - 1

	runParallelBenchmark(b, func(pb *testing.PB) {
		index := int(rand.Uint32() & uint32(mask))
		mc := atomic.AddUint64(&rc, 1)
		if benchCase.setPercentage*mc/100 != benchCase.setPercentage*(mc-1)/100 {
			for pb.Next() {
				c.Set(keys[index&mask], values[index&mask])
				index++
			}
		} else {
			var h, m uint64
			for pb.Next() {
				if _, ok := c.Get(keys[index&mask]); ok {
					h++
				} else {
					m++
				}
				index++
			}
			hits.Add(h)
			misses.Add(m)
		}
	})

	// Every key fits in the cache, so a miss means that the cache lost
	// an entry, and its reads are cheaper than they should be.
	if reads := hits.Load() + misses.Load(); reads > 0 {
		b.ReportMetric(100*float64(hits.Load())/float64(reads), "hit%")
	}
}

func BenchmarkCache(b *testing.B) {
	for _, data := range datas {
		for _, benchCase := range benchCases {
			for _, c := range clients {
				name := fmt.Sprintf("dist=%s/cache=%s/reads=%d%%", data.name, c.Name(), benchCase.readPercentage)
				b.Run(name, func(b *testing.B) {
					runCacheBenchmark(b, benchCase, data.keys, c)
				})
			}
		}
	}
}
