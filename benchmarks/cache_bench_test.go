package benchmarks

import (
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/allegro/bigcache"
	"github.com/coocood/freecache"
	"github.com/dgraph-io/ristretto"
	"github.com/pingcap/go-ycsb/pkg/generator"

	"github.com/maypok86/otter"
)

//go:noescape
//go:linkname fastrand runtime.fastrand
func fastrand() uint32

func b2s(data []byte) string {
	return *(*string)(unsafe.Pointer(&data))
}

func s2b(s string) (b []byte) {
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	slh := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	slh.Data = sh.Data
	slh.Len = sh.Len
	slh.Cap = sh.Len
	return b
}

const (
	// number of entries to use in benchmarks
	benchmarkNumEntries = 5_000_000
	// key prefix used in benchmarks
	benchmarkKeyPrefix = "what_a_looooooooooooooooooooooong_key_prefix_"
	// value prefix used in benchmarks
	benchmarkValuePrefix = "what_a_loooooooooooooooooooooooooong_value_prefix_"
)

var benchmarkCases = []struct {
	name           string
	readPercentage int
	setPercentage  int
}{
	{"reads=100%,writes=0%", 100, 0}, // 100% loads,    0% stores,    0% deletes
	{"reads=99%,writes=1%", 99, 1},   //  99% loads,  1% stores,  0% deletes
	{"reads=90%,writes=5%", 90, 5},   //  90% loads,    5% stores,    5% deletes
	{"reads=75%,writes=20%", 75, 20}, //  75% loads, 12.5% stores, 5% deletes
	{"reads=50%,writes=45", 50, 45},  //  50% loads, 45% stores, 5% deletes
}

var (
	benchmarkKeys   []string
	benchmarkValues []string
)

func init() {
	// To ensure repetition of keys in the array,
	// we are generating keys in the range from 0 to workloadSize/3.
	maxKey := int64(benchmarkNumEntries) / 3

	// scrambled zipfian to ensure same keys are not together
	z := generator.NewScrambledZipfian(0, maxKey, generator.ZipfianConstant)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	benchmarkKeys = make([]string, benchmarkNumEntries)
	for i := 0; i < benchmarkNumEntries; i++ {
		benchmarkKeys[i] = strconv.Itoa(int(z.Next(r)))
	}

	benchmarkValues = make([]string, benchmarkNumEntries)
	for i := 0; i < benchmarkNumEntries; i++ {
		benchmarkValues[i] = benchmarkValuePrefix + strconv.Itoa(i)
	}
}

func runParallel(b *testing.B, benchFn func(pb *testing.PB)) {
	b.ResetTimer()
	b.ReportAllocs()
	start := time.Now()
	b.RunParallel(benchFn)
	opsPerSec := float64(b.N) / float64(time.Since(start).Seconds())
	b.ReportMetric(opsPerSec, "ops/s")
}

func benchmarkCache(
	b *testing.B,
	getFn func(k string) (string, bool),
	setFn func(k string, v string),
	deleteFn func(k string),
	getPercentage int,
	setPercentage int,
) {
	numEntries := benchmarkNumEntries / 4 * 3
	for i := 0; i < numEntries; i++ {
		setFn(benchmarkKeys[i], benchmarkValues[i])
	}
	runParallel(b, func(pb *testing.PB) {
		// convert percent to permille to support 99% case
		setThreshold := 10 * getPercentage
		deleteThreshold := 10 * (getPercentage + setPercentage)
		for pb.Next() {
			op := int(fastrand() % 1000)
			i := int(fastrand() % uint32(benchmarkNumEntries))
			if op >= deleteThreshold {
				deleteFn(benchmarkKeys[i])
			} else if op >= setThreshold {
				setFn(benchmarkKeys[i], benchmarkValues[i])
			} else {
				getFn(benchmarkKeys[i])
			}
		}
	})
}

func BenchmarkCache(b *testing.B) {
	for _, bc := range benchmarkCases {
		if bc.readPercentage == 100 {
			continue
		}
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			c, err := otter.New[string, string](otter.WithCapacity(1e6))
			if err != nil {
				b.Fatalf("cannot create cache: %s", err)
			}
			benchmarkCache(b, func(k string) (string, bool) {
				return c.Get(k)
			}, func(k string, v string) {
				c.Set(k, v)
			}, func(k string) {
				c.Delete(k)
			}, bc.readPercentage, bc.setPercentage)
		})
	}
}

func BenchmarkRistretto(b *testing.B) {
	for _, bc := range benchmarkCases {
		if bc.readPercentage == 100 {
			continue
		}
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			cache, err := ristretto.NewCache(&ristretto.Config{
				NumCounters: 1e7, // number of keys to track frequency of (10M).
				MaxCost:     1e6, // maximum cost of cache (1GB).
				BufferItems: 64,  // number of keys per Get buffer.
			})
			if err != nil {
				b.Fatalf("cannot create cache: %s", err)
			}
			benchmarkCache(b, func(k string) (string, bool) {
				v, ok := cache.Get(k)
				if v == nil {
					return "", ok
				}
				return v.(string), ok
			}, func(k string, v string) {
				cache.Set(k, v, 1)
			}, func(k string) {
				cache.Del(k)
			}, bc.readPercentage, bc.setPercentage)
		})
	}
}

func BenchmarkFastcache(b *testing.B) {
	for _, bc := range benchmarkCases {
		if bc.readPercentage == 100 {
			continue
		}
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			c := fastcache.New(1 << 30)
			benchmarkCache(b, func(k string) (string, bool) {
				v, ok := c.HasGet(nil, s2b(k))
				return b2s(v), ok
			}, func(k string, v string) {
				c.Set(s2b(k), s2b(v))
			}, func(k string) {
				c.Del(s2b(k))
			}, bc.readPercentage, bc.setPercentage)
		})
	}
}

func BenchmarkBigcache(b *testing.B) {
	for _, bc := range benchmarkCases {
		if bc.readPercentage == 100 {
			continue
		}
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			cfg := bigcache.DefaultConfig(10 * time.Minute)
			cfg.Verbose = false
			c, err := bigcache.NewBigCache(cfg)
			if err != nil {
				b.Fatalf("cannot create cache: %s", err)
			}
			benchmarkCache(b, func(k string) (string, bool) {
				v, err := c.Get(k)
				if err != nil {
					return "", false
				}
				return b2s(v), true
			}, func(k string, v string) {
				if err := c.Set(k, s2b(v)); err != nil {
					b.Fatalf("cannot set value: %s", err)
				}
			}, func(k string) {
				_ = c.Delete(k)
			}, bc.readPercentage, bc.setPercentage)
		})
	}
}

func BenchmarkFreecache(b *testing.B) {
	for _, bc := range benchmarkCases {
		if bc.readPercentage == 100 {
			continue
		}
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			c := freecache.NewCache(1 << 30)
			benchmarkCache(b, func(k string) (string, bool) {
				v, err := c.Get(s2b(k))
				if err != nil {
					return "", false
				}
				return b2s(v), true
			}, func(k string, v string) {
				if err := c.Set(s2b(k), s2b(v), 0); err != nil {
					b.Fatalf("cannot set value: %s", err)
				}
			}, func(k string) {
				c.Del(s2b(k))
			}, bc.readPercentage, bc.setPercentage)
		})
	}
}

func BenchmarkMapRW(b *testing.B) {
	for _, bc := range benchmarkCases {
		if bc.readPercentage == 100 {
			continue
		}
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			m := make(map[string]string)
			var mu sync.RWMutex
			benchmarkCache(b, func(k string) (string, bool) {
				mu.RLock()
				v, ok := m[k]
				mu.RUnlock()
				return v, ok
			}, func(k string, v string) {
				mu.Lock()
				m[k] = v
				mu.Unlock()
			}, func(k string) {
				mu.Lock()
				delete(m, k)
				mu.Unlock()
			}, bc.readPercentage, bc.setPercentage)
		})
	}
}

func BenchmarkSyncMap(b *testing.B) {
	for _, bc := range benchmarkCases {
		if bc.readPercentage == 100 {
			continue
		}
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			var m sync.Map
			benchmarkCache(b, func(k string) (string, bool) {
				v, ok := m.Load(k)
				if v == nil {
					return "", false
				}
				return v.(string), ok
			}, func(k string, v string) {
				m.Store(k, v)
			}, func(k string) {
				m.Delete(k)
			}, bc.readPercentage, bc.setPercentage)
		})
	}
}
