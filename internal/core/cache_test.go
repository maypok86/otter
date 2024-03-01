package core

import (
	"testing"
	"time"

	"github.com/maypok86/otter/internal/generated/node"
)

func TestCache_SetWithCost(t *testing.T) {
	size := 10
	c := NewCache[int, int](Config[int, int]{
		Capacity: size,
		CostFunc: func(key int, value int) uint32 {
			return uint32(key)
		},
	})

	goodCost := int(c.policy.MaxAvailableCost())
	badCost := goodCost + 1

	added := c.Set(goodCost, 1)
	if !added {
		t.Fatalf("Set was dropped, even though it shouldn't have been. Max available cost: %d, actual cost: %d",
			c.policy.MaxAvailableCost(),
			c.costFunc(goodCost, 1),
		)
	}
	added = c.Set(badCost, 1)
	if added {
		t.Fatalf("Set wasn't dropped, though it should have been. Max available cost: %d, actual cost: %d",
			c.policy.MaxAvailableCost(),
			c.costFunc(badCost, 1),
		)
	}
}

func TestCache_Range(t *testing.T) {
	size := 10
	ttl := time.Hour
	c := NewCache[int, int](Config[int, int]{
		Capacity: size,
		CostFunc: func(key int, value int) uint32 {
			return 1
		},
		TTL: &ttl,
	})

	time.Sleep(3 * time.Second)

	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithCost:       true,
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
		CostFunc: func(key int, value int) uint32 {
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
	if !c.isClosed {
		t.Fatalf("cache should be closed")
	}

	c.Close()

	if cacheSize := c.Size(); cacheSize != 0 {
		t.Fatalf("c.Size() = %d, want = %d", cacheSize, 0)
	}
	if !c.isClosed {
		t.Fatalf("cache should be closed")
	}
}

func TestCache_Clear(t *testing.T) {
	size := 10
	c := NewCache[int, int](Config[int, int]{
		Capacity: size,
		CostFunc: func(key int, value int) uint32 {
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
	if c.isClosed {
		t.Fatalf("cache shouldn't be closed")
	}
}
