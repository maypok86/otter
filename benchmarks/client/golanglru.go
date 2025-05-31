package client

import (
	"time"

	"github.com/hashicorp/golang-lru/arc/v2"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

type LRU[K comparable, V any] struct {
	client *lru.Cache[K, V]
}

func (c *LRU[K, V]) Init(capacity int) {
	client, err := lru.New[K, V](capacity)
	if err != nil {
		panic(err)
	}
	c.client = client
}

func (c *LRU[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *LRU[K, V]) Set(key K, value V) {
	c.client.Add(key, value)
}

func (c *LRU[K, V]) Name() string {
	return "lru"
}

func (c *LRU[K, V]) Close() {
	c.client = nil
}

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

type GolangLRU[K comparable, V any] struct {
	client *expirable.LRU[K, V]
}

func (c *GolangLRU[K, V]) Init(capacity int) {
	client := expirable.NewLRU[K, V](capacity, nil, time.Hour)
	c.client = client
}

func (c *GolangLRU[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *GolangLRU[K, V]) Set(key K, value V) {
	c.client.Add(key, value)
}

func (c *GolangLRU[K, V]) Name() string {
	return "golang-lru"
}

func (c *GolangLRU[K, V]) Close() {
	c.client.Purge()
	c.client = nil
}
