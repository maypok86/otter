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
	"github.com/dgraph-io/ristretto"
	hashicorp "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/jellydator/ttlcache/v3"
	"github.com/karlseguin/ccache/v3"
	"github.com/viccon/sturdyc"

	"github.com/maypok86/otter/v2"
)

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

func main() {
	name := os.Args[1]
	stringCapacity := os.Args[2]
	capacity, err := strconv.Atoi(stringCapacity)
	if err != nil {
		log.Fatal(err)
	}

	keys = make([]string, 0, capacity)
	for i := 0; i < capacity; i++ {
		keys = append(keys, strconv.Itoa(i))
	}

	constructor, ok := map[string]func(int){
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

	var o runtime.MemStats
	runtime.ReadMemStats(&o)

	constructor(capacity)

	// runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	fmt.Printf("%s\t%d\t%v MB\t%v MB\n",
		name,
		capacity,
		toFixed(toMB(m.Alloc-o.Alloc), 2),
		toFixed(toMB(m.TotalAlloc-o.TotalAlloc), 2),
	)
}

func newOtter(capacity int) {
	cache := otter.Must[string, string](&otter.Options[string, string]{
		MaximumSize:      capacity,
		ExpiryCalculator: otter.ExpiryWriting[string, string](time.Hour),
	})
	for _, key := range keys {
		cache.Set(key, key)
		for i := 0; i < 10; i++ {
			cache.GetIfPresent(key)
		}
		time.Sleep(5 * time.Microsecond)
	}
}

func newRistretto(capacity int) {
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
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
		time.Sleep(5 * time.Microsecond)
	}
}

func newTheine(capacity int) {
	cache, err := theine.NewBuilder[string, string](int64(capacity)).Build()
	if err != nil {
		log.Fatal(err)
	}
	for _, key := range keys {
		cache.SetWithTTL(key, key, 1, time.Hour)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
		time.Sleep(5 * time.Microsecond)
	}
}

func newCcache(capacity int) {
	cache := ccache.New(ccache.Configure[string]().MaxSize(int64(capacity)))
	for _, key := range keys {
		cache.Set(key, key, time.Hour)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
		time.Sleep(5 * time.Microsecond)
	}
}

func newGcache(capacity int) {
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
		time.Sleep(5 * time.Microsecond)
	}
}

func newTTLCache(capacity int) {
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
		time.Sleep(5 * time.Microsecond)
	}
}

func newHashicorp(capacity int) {
	cache := hashicorp.NewLRU[string, string](capacity, nil, time.Hour)
	for _, key := range keys {
		cache.Add(key, key)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
		time.Sleep(5 * time.Microsecond)
	}
}

func newSturdyc(capacity int) {
	cache := sturdyc.New[string](capacity, 10, time.Hour, 10)
	for _, key := range keys {
		cache.Set(key, key)
		for i := 0; i < 10; i++ {
			cache.Get(key)
		}
		time.Sleep(5 * time.Microsecond)
	}
}
