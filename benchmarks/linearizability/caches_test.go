package linearizability

import (
	"context"
	"errors"
	"hash/maphash"
	"sync/atomic"
	"time"

	"github.com/Yiling-J/theine-go"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/elastic/go-freelru"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/jellydator/ttlcache/v3"
	"github.com/karlseguin/ccache/v3"
	phuslu "github.com/phuslu/lru"
	"github.com/viccon/sturdyc"

	"github.com/maypok86/otter/v2"
)

// capacity is far above the number of keys the tests use, so no cache evicts.
const capacity = 1024

var errLoad = errors.New("load failed")

// loadFunc returns a value to load, unique and negative, or errLoad.
type loadFunc func() (int, error)

// cache is one library behind the operations the tests run. A nil get means the library has no
// plain read, a nil load that it cannot load missing entries.
type cache struct {
	get   func(k string) (int, bool)
	set   func(k string, v int)
	del   func(k string)
	load  func(k string, fn loadFunc) (int, error)
	close func()
}

type library struct {
	name string
	new  func() *cache
}

// libraries are the caches of the benchmarks, configured as in benchmarks/client, except that
// each load takes its loader per call, so that a history can tell its loads apart.
var libraries = []library{
	{"otter", func() *cache {
		c := otter.Must(&otter.Options[string, int]{MaximumSize: capacity})
		return &cache{
			get: c.GetIfPresent,
			set: func(k string, v int) { c.Set(k, v) },
			del: func(k string) { c.Invalidate(k) },
			load: func(k string, fn loadFunc) (int, error) {
				return c.Get(context.Background(), k, otter.LoaderFunc[string, int](func(context.Context, string) (int, error) {
					return fn()
				}))
			},
			close: func() {},
		}
	}},
	{"ristretto", func() *cache {
		c := newRistretto()
		return &cache{
			get:   c.Get,
			set:   func(k string, v int) { c.Set(k, v, 1) },
			del:   c.Del,
			close: c.Close,
		}
	}},
	{"ristretto (with Wait)", func() *cache {
		// Wait applies the buffered writes, which is what a caller that needs to read its own
		// write has to do. A Del goes through the buffer too.
		c := newRistretto()
		return &cache{
			get: c.Get,
			set: func(k string, v int) {
				c.Set(k, v, 1)
				c.Wait()
			},
			del: func(k string) {
				c.Del(k)
				c.Wait()
			},
			close: c.Close,
		}
	}},
	{"theine", func() *cache {
		c, err := theine.NewBuilder[string, int](capacity).Build()
		if err != nil {
			panic(err)
		}
		return &cache{
			get:   c.Get,
			set:   func(k string, v int) { c.Set(k, v, 1) },
			del:   c.Delete,
			close: c.Close,
		}
	}},
	{"theine (LoadingCache)", func() *cache {
		// The loader is fixed when the cache is built, so each load passes its own in the context.
		c, err := theine.NewBuilder[string, int](capacity).Loading(func(ctx context.Context, _ string) (theine.Loaded[int], error) {
			v, err := ctx.Value(loadFuncKey{}).(loadFunc)()
			return theine.Loaded[int]{Value: v, Cost: 1}, err
		}).Build()
		if err != nil {
			panic(err)
		}
		return &cache{
			set: func(k string, v int) { c.Set(k, v, 1) },
			del: c.Delete,
			load: func(k string, fn loadFunc) (int, error) {
				return c.Get(context.WithValue(context.Background(), loadFuncKey{}, fn), k)
			},
			close: c.Close,
		}
	}},
	{"ccache", func() *cache {
		c := ccache.New(ccache.Configure[int]().MaxSize(capacity))
		return &cache{
			get: func(k string) (int, bool) {
				it := c.Get(k)
				if it == nil {
					return 0, false
				}
				return it.Value(), true
			},
			set: func(k string, v int) { c.Set(k, v, time.Hour) },
			del: func(k string) { c.Delete(k) },
			load: func(k string, fn loadFunc) (int, error) {
				it, err := c.Fetch(k, time.Hour, func() (int, error) { return fn() })
				if err != nil {
					return 0, err
				}
				return it.Value(), nil
			},
			close: c.Stop,
		}
	}},
	{"go-freelru", func() *cache {
		seed := maphash.MakeSeed()
		c, err := freelru.NewSharded[string, int](capacity, func(k string) uint32 {
			return uint32(maphash.String(seed, k))
		})
		if err != nil {
			panic(err)
		}
		return &cache{
			get:   c.Get,
			set:   func(k string, v int) { c.Add(k, v) },
			del:   func(k string) { c.Remove(k) },
			close: func() {},
		}
	}},
	{"golang-lru", func() *cache {
		c, err := lru.New[string, int](capacity)
		if err != nil {
			panic(err)
		}
		return &cache{
			get:   c.Get,
			set:   func(k string, v int) { c.Add(k, v) },
			del:   func(k string) { c.Remove(k) },
			close: func() {},
		}
	}},
	{"phuslu/lru", func() *cache {
		c := phuslu.NewLRUCache[string, int](capacity)
		return &cache{
			get: c.Get,
			set: func(k string, v int) { c.Set(k, v) },
			del: func(k string) { c.Delete(k) },
			load: func(k string, fn loadFunc) (int, error) {
				v, err, _ := c.GetOrLoad(context.Background(), k, func(context.Context, string) (int, error) { return fn() })
				return v, err
			},
			close: func() {},
		}
	}},
	{"sturdyc", func() *cache {
		c := sturdyc.New[int](capacity, 10, time.Hour, 10)
		return &cache{
			get: c.Get,
			set: func(k string, v int) { c.Set(k, v) },
			del: c.Delete,
			load: func(k string, fn loadFunc) (int, error) {
				return c.GetOrFetch(context.Background(), k, func(context.Context) (int, error) { return fn() })
			},
			close: func() {},
		}
	}},
	{"ttlcache", func() *cache {
		c := ttlcache.New[string, int](ttlcache.WithCapacity[string, int](capacity))
		// A suppressed loader deduplicates only the loads that go through it, so the cache has
		// one, and each load hands it its loader through current. Concurrent loads may run each
		// other's loader, which is harmless: every loader returns a fresh unique value.
		var current atomic.Value
		loader := ttlcache.NewSuppressedLoader[string, int](ttlcache.LoaderFunc[string, int](
			func(c *ttlcache.Cache[string, int], k string) *ttlcache.Item[string, int] {
				v, err := current.Load().(loadFunc)()
				if err != nil {
					return nil
				}
				return c.Set(k, v, ttlcache.DefaultTTL)
			}), nil)
		return &cache{
			get: func(k string) (int, bool) {
				it := c.Get(k)
				if it == nil {
					return 0, false
				}
				return it.Value(), true
			},
			set: func(k string, v int) { c.Set(k, v, ttlcache.DefaultTTL) },
			del: c.Delete,
			load: func(k string, fn loadFunc) (int, error) {
				current.Store(fn)
				it := c.Get(k, ttlcache.WithLoader[string, int](loader))
				if it == nil {
					return 0, errLoad
				}
				return it.Value(), nil
			},
			close: c.DeleteAll,
		}
	}},
}

type loadFuncKey struct{}

func newRistretto() *ristretto.Cache[string, int] {
	c, err := ristretto.NewCache(&ristretto.Config[string, int]{
		NumCounters:        capacity * 10,
		MaxCost:            capacity,
		BufferItems:        64,
		IgnoreInternalCost: true,
	})
	if err != nil {
		panic(err)
	}
	return c
}
