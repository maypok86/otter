package core

import (
	"testing"
	"time"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func TestCache_SetWithWeight(t *testing.T) {
	size := 10
	c := NewCache[int, int](Config[int, int]{
		Capacity: size,
		Weigher: func(key int, value int) uint32 {
			return uint32(key)
		},
	})

	goodWeight := c.policy.MaxAvailableWeight()
	badWeight := goodWeight + 1

	added := c.Set(goodWeight, 1)
	if !added {
		t.Fatalf("Set was dropped, even though it shouldn't have been. Max available weight: %d, actual weight: %d",
			c.policy.MaxAvailableWeight(),
			c.weigher(goodWeight, 1),
		)
	}
	added = c.Set(badWeight, 1)
	if added {
		t.Fatalf("Set wasn't dropped, though it should have been. Max available weight: %d, actual weight: %d",
			c.policy.MaxAvailableWeight(),
			c.weigher(badWeight, 1),
		)
	}
}

func TestCache_Range(t *testing.T) {
	size := 10
	ttl := time.Hour
	c := NewCache[int, int](Config[int, int]{
		Capacity: size,
		Weigher: func(key int, value int) uint32 {
			return 1
		},
		TTL: &ttl,
	})

	time.Sleep(3 * time.Second)

	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithWeight:     true,
	})

	c.Set(1, 1)
	c.hashmap.Set(nm.Create(2, 2, 1, 1))
	c.Set(3, 3)
	aliveNodes := 2
	iters := 0
	c.Range(func(key, value int) bool {
		if key != value {
			t.Fatalf("got unexpected key/value for iteration %d: %d/%d", iters, key, value)
			return false
		}
		iters++
		return true
	})
	if iters != aliveNodes {
		t.Fatalf("got unexpected number of iterations: %d", iters)
	}
}

func TestCache_Close(t *testing.T) {
	size := 10
	c := NewCache[int, int](Config[int, int]{
		Capacity: size,
		Weigher: func(key int, value int) uint32 {
			return 1
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	if cacheSize := c.Size(); cacheSize != size {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, size)
	}

	c.Close()

	time.Sleep(10 * time.Millisecond)

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}

	c.Close()

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
}

func TestCache_Clear(t *testing.T) {
	size := 10
	c := NewCache[int, int](Config[int, int]{
		Capacity: size,
		Weigher: func(key int, value int) uint32 {
			return 1
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	if cacheSize := c.Size(); cacheSize != size {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, size)
	}

	c.Clear()

	time.Sleep(10 * time.Millisecond)

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
}
