package throughput

import (
	"fmt"
	"math/rand"
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
	name           string
	readPercentage int
	setPercentage  uint64
}

var benchCases = []benchCase{
	{"reads=100%,writes=0%", 100, 0},
	{"reads=75%,writes=25%", 75, 25},
	//{"reads=50%,writes=50%", 50, 50},
	//{"reads=25%,writes=75%", 25, 75},
	{"reads=0%,writes=100%", 0, 100},
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

	b.ResetTimer()
	start := time.Now()
	b.RunParallel(benchFunc)
	opsPerSec := float64(b.N) / time.Since(start).Seconds()
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

	for i := 0; i < dataLength; i++ {
		c.Set(keys[i], values[i])
	}

	rc := uint64(0)
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
			for pb.Next() {
				c.Get(keys[index&mask])
				index++
			}
		}
	})
}

func BenchmarkCache(b *testing.B) {
	for _, data := range datas {
		for _, benchCase := range benchCases {
			for _, c := range clients {
				name := fmt.Sprintf("%s_%s_%s", data.name, c.Name(), benchCase.name)
				b.Run(name, func(b *testing.B) {
					runCacheBenchmark(b, benchCase, data.keys, c)
				})
				c.Close()
			}
		}
	}
}
