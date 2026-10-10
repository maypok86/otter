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
