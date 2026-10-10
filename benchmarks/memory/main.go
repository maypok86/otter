package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/Yiling-J/theine-go"
	"github.com/bluele/gcache"
	"github.com/dgraph-io/ristretto/v2"
	hashicorp "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/jellydator/ttlcache/v3"
	"github.com/karlseguin/ccache/v3"
	"github.com/viccon/sturdyc"

	"github.com/maypok86/otter/v2"
)

// keyLength is the length of every key. Values share the keys' memory.
const keyLength = 32

var keys []string

func round(num float64) int {
	return int(num + math.Copysign(0.5, num))
}

func toFixed(num float64, precision int) float64 {
	output := math.Pow(10, float64(precision))
	return float64(round(num*output)) / output
}

func toMB(bytes uint64) float64 {
	return float64(bytes) / 1024 / 1024
}

// liveHeap returns the size of the live heap.
func liveHeap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func main() {
	name := os.Args[1]
	stringCapacity := os.Args[2]
	capacity, err := strconv.Atoi(stringCapacity)
	if err != nil {
		log.Fatal(err)
	}

	// The keys and values are allocated before the measurement, so only
	// the memory that the cache itself needs is counted.
	keys = make([]string, 0, capacity)
	for i := 0; i < capacity; i++ {
		keys = append(keys, fmt.Sprintf("%0*d", keyLength, i))
	}

	constructor, ok := map[string]func(int) func(string) bool{
		"otter":      newOtter,
		"theine":     newTheine,
		"ristretto":  newRistretto,
		"ccache":     newCcache,
		"gcache":     newGcache,
		"ttlcache":   newTTLCache,
		"golang-lru": newHashicorp,
		"sturdyc":    newSturdyc,
	}[name]
	if !ok {
		log.Fatalf("not found cache %s\n", name)
	}

	before := liveHeap()
	contains := constructor(capacity)
	after := liveHeap()

	// A cache that drops writes would look smaller than it is, so count
	// the entries that it actually kept. This also keeps the cache alive
	// until the measurement is done.
	var entries int
	for _, key := range keys {
		if contains(key) {
			entries++
		}
	}
	runtime.KeepAlive(contains)

	overhead := after - before
	perEntry := 0.0
	if entries > 0 {
		perEntry = float64(overhead) / float64(entries)
	}
	fmt.Printf("%s\t%d\t%v MB\t%.0f B/entry\t%d entries\n",
		name,
		capacity,
		toFixed(toMB(overhead), 2),
		perEntry,
		entries,
	)
}

func newOtter(capacity int) func(string) bool {
	cache := otter.Must[string, string](&otter.Options[string, string]{
		MaximumSize:      capacity,
		ExpiryCalculator: otter.ExpiryWriting[string, string](time.Hour),
	})
	for _, key := range keys {
		cache.Set(key, key)
		for i := 0; i < 10; i++ {
			cache.GetIfPresent(key)
		}
	}
	return func(key string) bool {
		_, ok := cache.GetIfPresent(key)
		return ok
	}
}

func newRistretto(capacity int) func(string) bool {
	cache, err := ristretto.NewCache[string, string](&ristretto.Config[string, string]{
		NumCounters:        10 * int64(capacity),
		MaxCost:            int64(capacity),
		BufferItems:        64,
		IgnoreInternalCost: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, key := range keys {
		cache.SetWithTTL(key, key, 1, time.Hour)
		// Writes are applied asynchronously and dropped when the buffer
		// is full.
		cache.Wait()
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
	}
	return func(key string) bool {
		_, ok := cache.Get(key)
		return ok
	}
}

func newTheine(capacity int) func(string) bool {
	cache, err := theine.NewBuilder[string, string](int64(capacity)).Build()
	if err != nil {
		log.Fatal(err)
	}
	for _, key := range keys {
		cache.SetWithTTL(key, key, 1, time.Hour)
		cache.Wait()
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
	}
	return func(key string) bool {
		_, ok := cache.Get(key)
		return ok
	}
}

func newCcache(capacity int) func(string) bool {
	cache := ccache.New(ccache.Configure[string]().MaxSize(int64(capacity)))
	for _, key := range keys {
		cache.Set(key, key, time.Hour)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
	}
	return func(key string) bool {
		return cache.Get(key) != nil
	}
}

func newGcache(capacity int) func(string) bool {
	cache := gcache.New(capacity).Expiration(time.Hour).LRU().Build()
	for _, key := range keys {
		if err := cache.Set(key, key); err != nil {
			panic(err)
		}
		for i := 0; i < 10; i++ {
			if _, err := cache.Get(key); err != nil {
				panic(err)
			}
		}
	}
	return func(key string) bool {
		_, err := cache.Get(key)
		return err == nil
	}
}

func newTTLCache(capacity int) func(string) bool {
	cache := ttlcache.New[string, string](
		ttlcache.WithTTL[string, string](time.Hour),
		ttlcache.WithCapacity[string, string](uint64(capacity)),
	)
	go cache.Start()
	for _, key := range keys {
		cache.Set(key, key, ttlcache.DefaultTTL)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
	}
	return func(key string) bool {
		return cache.Get(key) != nil
	}
}

func newHashicorp(capacity int) func(string) bool {
	cache := hashicorp.NewLRU[string, string](capacity, nil, time.Hour)
	for _, key := range keys {
		cache.Add(key, key)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
	}
	return func(key string) bool {
		_, ok := cache.Get(key)
		return ok
	}
}

func newSturdyc(capacity int) func(string) bool {
	cache := sturdyc.New[string](capacity, 10, time.Hour, 10)
	for _, key := range keys {
		cache.Set(key, key)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
	}
	return func(key string) bool {
		_, ok := cache.Get(key)
		return ok
	}
}
