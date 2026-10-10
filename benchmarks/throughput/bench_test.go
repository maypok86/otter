package throughput

import (
	"fmt"
	"math/rand"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pingcap/go-ycsb/pkg/generator"

	"github.com/maypok86/otter/v2/benchmarks/client"
)

// dataLength is the number of requests in the trace of the small workloads.
const dataLength = 2 << 14

// largeSize is the capacity and the number of keys of the large workload.
const largeSize = 1 << 20

// minGoroutines keeps the split between readers and writers when the
// benchmark runs on fewer cores: 8 goroutines give 25% steps.
const minGoroutines = 8

func newClients(ttl time.Duration) []client.Client[string, string] {
	return []client.Client[string, string]{
		&client.Otter[string, string]{TTL: ttl},
		&client.Theine[string, string]{TTL: ttl},
		&client.Ristretto[string, string]{TTL: ttl},
		&client.Sturdyc[string]{TTL: ttl},
		&client.Ccache[string]{TTL: ttl},
		&client.Gcache[string, string]{TTL: ttl},
		&client.TTLCache[string, string]{TTL: ttl},
		&client.GolangLRU[string, string]{TTL: ttl},
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

// workload is a sequence of requests and the cache they run against.
type workload struct {
	dist     string
	capacity int
	// keys and values are the requests in order. Their length is a power
	// of two, so that an index can wrap around with a mask.
	keys   []string
	values []string
	// fillKeys and fillValues are written before the measurement.
	fillKeys   []string
	fillValues []string
}

// newWorkload builds a trace of length requests to keys in [0, keySpace).
func newWorkload(dist string, capacity, keySpace, length int, next func(r *rand.Rand) int) workload {
	keys := make([]string, keySpace)
	values := make([]string, keySpace)
	for i := range keySpace {
		keys[i] = newKey(strconv.Itoa(i))
		values[i] = newValue(strconv.Itoa(i))
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	w := workload{
		dist:     dist,
		capacity: capacity,
		keys:     make([]string, length),
		values:   make([]string, length),
	}
	for i := range length {
		k := next(r)
		w.keys[i] = keys[k]
		w.values[i] = values[k]
	}

	if keySpace == capacity {
		// Every key fits, so fill them all and every read is a hit.
		w.fillKeys = keys
		w.fillValues = values
	} else {
		// Otherwise, fill the cache with the trace itself.
		w.fillKeys = w.keys[:min(length, dataLength)]
		w.fillValues = w.values[:min(length, dataLength)]
	}
	return w
}

func zipf(keySpace int) func(r *rand.Rand) int {
	z := generator.NewScrambledZipfian(0, int64(keySpace-1), generator.ZipfianConstant)
	return func(r *rand.Rand) int {
		return int(z.Next(r))
	}
}

func uniform(keySpace int) func(r *rand.Rand) int {
	return func(r *rand.Rand) int {
		return r.Intn(keySpace)
	}
}

var (
	// hitsWorkload is the classic one: a zipf distribution over a third of
	// the capacity, so every read is a hit and every write an update.
	hitsWorkload = sync.OnceValue(func() workload {
		return newWorkload("zipf", dataLength, dataLength/3+1, dataLength, zipf(dataLength/3+1))
	})
	// The eviction workloads have four times more keys than fit in the
	// cache, so reads miss and writes insert and evict.
	evictionZipf = sync.OnceValue(func() workload {
		return newWorkload("zipf", dataLength/4, dataLength, dataLength, zipf(dataLength))
	})
	evictionUniform = sync.OnceValue(func() workload {
		return newWorkload("uniform", dataLength/4, dataLength, dataLength, uniform(dataLength))
	})
	// largeWorkload doesn't fit in the CPU caches.
	largeWorkload = sync.OnceValue(func() workload {
		return newWorkload("zipf", largeSize, largeSize, 4*largeSize, zipf(largeSize))
	})
)

func runParallelBenchmark(b *testing.B, benchFunc func(pb *testing.PB)) {
	b.Helper()

	procs := runtime.GOMAXPROCS(0)
	b.SetParallelism((minGoroutines + procs - 1) / procs)
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

func measure(b *testing.B, benchCase benchCase, w workload, c client.Client[string, string]) {
	b.Helper()

	var (
		rc     uint64
		hits   atomic.Uint64
		misses atomic.Uint64
	)
	mask := len(w.keys) - 1

	runParallelBenchmark(b, func(pb *testing.PB) {
		index := int(rand.Uint32() & uint32(mask))
		mc := atomic.AddUint64(&rc, 1)
		if benchCase.setPercentage*mc/100 != benchCase.setPercentage*(mc-1)/100 {
			for pb.Next() {
				c.Set(w.keys[index&mask], w.values[index&mask])
				index++
			}
		} else {
			var h, m uint64
			for pb.Next() {
				if _, ok := c.Get(w.keys[index&mask]); ok {
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

	// When every key fits, a miss means that the cache lost an entry, and
	// its reads are cheaper than they should be. With eviction, it shows
	// how well the cache keeps the hot entries under load.
	if reads := hits.Load() + misses.Load(); reads > 0 {
		b.ReportMetric(100*float64(hits.Load())/float64(reads), "hit%")
	}
}

// runWorkload runs every cache on every read/write mix.
//
// A sub-benchmark is called several times while the testing package picks
// b.N. The cache is built and filled only on the first call and closed
// after the last one: filling a cache with a million entries takes longer
// than the measurement itself.
func runWorkload(b *testing.B, w workload, clients []client.Client[string, string]) {
	b.Helper()

	for _, benchCase := range benchCases {
		for _, c := range clients {
			name := fmt.Sprintf("dist=%s/cache=%s/reads=%d%%", w.dist, c.Name(), benchCase.readPercentage)
			initialized := false
			b.Run(name, func(b *testing.B) {
				if !initialized {
					c.Init(w.capacity)
					for i := range w.fillKeys {
						c.Set(w.fillKeys[i], w.fillValues[i])
					}
					// Let the entries land before the measurement starts.
					if waiter, ok := c.(client.Waiter); ok {
						waiter.Wait()
					}
					initialized = true
				}
				measure(b, benchCase, w, c)
			})
			if initialized {
				c.Close()
			}
		}
	}
}

// BenchmarkCache is the classic benchmark: the cache holds every key, so
// it measures the hot paths without eviction.
func BenchmarkCache(b *testing.B) {
	runWorkload(b, hitsWorkload(), newClients(0))
}

// BenchmarkEviction runs against a cache that is four times smaller than
// the key space.
func BenchmarkEviction(b *testing.B) {
	runWorkload(b, evictionZipf(), newClients(0))
	runWorkload(b, evictionUniform(), newClients(0))
}

// BenchmarkExpiration is BenchmarkCache with an expiration after write.
func BenchmarkExpiration(b *testing.B) {
	runWorkload(b, hitsWorkload(), newClients(time.Hour))
}

// BenchmarkLarge runs against a million entries, which don't fit in the
// CPU caches.
func BenchmarkLarge(b *testing.B) {
	runWorkload(b, largeWorkload(), newClients(0))
}

// BenchmarkLoading reads through the loading API of the caches that have
// one. The cache is four times smaller than the key space, so a part of
// the reads load the value.
func BenchmarkLoading(b *testing.B) {
	w := evictionZipf()
	load := func(key string) string {
		return key
	}

	for _, c := range newClients(0) {
		loader, ok := c.(client.Loader[string, string])
		if !ok {
			continue
		}
		name := fmt.Sprintf("dist=%s/cache=%s/reads=100%%", w.dist, c.Name())
		initialized := false
		b.Run(name, func(b *testing.B) {
			if !initialized {
				loader.InitLoading(w.capacity, load)
				for _, key := range w.fillKeys {
					loader.Load(key)
				}
				initialized = true
			}
			mask := len(w.keys) - 1
			runParallelBenchmark(b, func(pb *testing.PB) {
				index := int(rand.Uint32() & uint32(mask))
				for pb.Next() {
					loader.Load(w.keys[index&mask])
					index++
				}
			})
		})
		if initialized {
			c.Close()
		}
	}
}
