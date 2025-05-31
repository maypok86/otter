package client

import (
	"time"

	"github.com/Yiling-J/theine-go"
)

type Theine[K comparable, V any] struct {
	client *theine.Cache[K, V]
}

func (c *Theine[K, V]) Init(capacity int) {
	client, err := theine.NewBuilder[K, V](int64(capacity)).Build()
	if err != nil {
		panic(err)
	}
	c.client = client
}

func (c *Theine[K, V]) Name() string {
	return "theine"
}

func (c *Theine[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *Theine[K, V]) Set(key K, value V) {
	c.client.SetWithTTL(key, value, 1, time.Hour)
}

func (c *Theine[K, V]) Close() {
	c.client.Close()
	c.client = nil
}
