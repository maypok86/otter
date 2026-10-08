package product

type Product[K comparable, V any] interface {
	Init(capacity int)
	Get(key K) (V, bool)
	Set(key K, value V)
	Name() string
	Close()
}
