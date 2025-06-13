package product

import "github.com/hashicorp/golang-lru/arc/v2"

type ARC[K comparable, V any] struct {
	client *arc.ARCCache[K, V]
}

func (c *ARC[K, V]) Init(capacity int) {
	client, err := arc.NewARC[K, V](capacity)
	if err != nil {
		panic(err)
	}
	c.client = client
}

func (c *ARC[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *ARC[K, V]) Set(key K, value V) {
	c.client.Add(key, value)
}

func (c *ARC[K, V]) Name() string {
	return "arc"
}

func (c *ARC[K, V]) Close() {
	c.client = nil
}
