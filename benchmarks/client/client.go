package client

type Client[K comparable, V any] interface {
	Init(capacity int)
	Get(key K) (V, bool)
	Set(key K, value V)
	Name() string
	Close()
}

// Waiter is implemented by the caches that apply writes asynchronously.
type Waiter interface {
	// Wait blocks until the buffered writes are applied.
	Wait()
}

// Loader is implemented by the caches that can load missing entries.
type Loader[K comparable, V any] interface {
	// InitLoading creates a cache that calls load for missing entries.
	InitLoading(capacity int, load func(K) V)
	// Load returns the value of key and loads it on a miss.
	Load(key K) V
}
