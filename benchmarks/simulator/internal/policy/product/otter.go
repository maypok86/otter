package product

import "github.com/maypok86/otter/v2"

type Otter[K comparable, V any] struct {
	client *otter.Cache[K, V]
}

func (c *Otter[K, V]) Init(capacity int) {
	c.client = otter.Must[K, V](&otter.Options[K, V]{
		MaximumSize:     capacity,
		InitialCapacity: capacity,
		Executor: func(fn func()) {
			fn()
		},
	})
}

func (c *Otter[K, V]) Name() string {
	return "otter"
}

func (c *Otter[K, V]) Get(key K) (V, bool) {
	return c.client.GetIfPresent(key)
}

func (c *Otter[K, V]) Set(key K, value V) {
	c.client.Set(key, value)
}

func (c *Otter[K, V]) Close() {
	c.client = nil
}
