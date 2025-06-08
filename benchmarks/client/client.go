package client

import "strconv"

type Client[K comparable, V any] interface {
	Init(capacity int)
	Get(key K) (V, bool)
	Set(key K, value V)
	Name() string
	Close()
}

type keyWrapper[V any] struct {
	client Client[string, V]
}

func (kw *keyWrapper[V]) Init(capacity int) {
	kw.client.Init(capacity)
}

func (kw *keyWrapper[V]) Get(key uint64) (V, bool) {
	return kw.client.Get(strconv.FormatUint(key, 10))
}

func (kw *keyWrapper[V]) Set(key uint64, value V) {
	kw.client.Set(strconv.FormatUint(key, 10), value)
}

func (kw *keyWrapper[V]) Name() string {
	return kw.client.Name()
}

func (kw *keyWrapper[V]) Close() {
	kw.client.Close()
}

func Wrap[V any](client Client[string, V]) Client[uint64, V] {
	return &keyWrapper[V]{
		client: client,
	}
}
