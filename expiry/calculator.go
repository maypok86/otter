package expiry

import "time"

// Calculator calculates when cache entries expire. A single expiration time is retained so that the lifetime
// of an entry may be extended or reduced by subsequent evaluations.
type Calculator[K comparable, V any] interface {
	// ExpireAfterCreate specifies that the entry should be automatically removed from the cache once the duration has
	// elapsed after the entry's creation. To indicate no expiration, an entry may be given an
	// excessively long period.
	ExpireAfterCreate(key K, value V) time.Duration
	// ExpireAfterUpdate specifies that the entry should be automatically removed from the cache once the duration has
	// elapsed after the replacement of its value. To indicate no expiration, an entry may be given an
	// excessively long period. The currentDuration may be returned to not modify the expiration time.
	ExpireAfterUpdate(key K, value V, currentDuration time.Duration) time.Duration
	// ExpireAfterRead specifies that the entry should be automatically removed from the cache once the duration has
	// elapsed after its last read. To indicate no expiration, an entry may be given an excessively
	// long period. The currentDuration may be returned to not modify the expiration time.
	ExpireAfterRead(key K, value V, currentDuration time.Duration) time.Duration
}

type varCreating[K comparable, V any] struct {
	f func(key K, value V) time.Duration
}

func (c *varCreating[K, V]) ExpireAfterCreate(key K, value V) time.Duration {
	return c.f(key, value)
}

func (c *varCreating[K, V]) ExpireAfterUpdate(key K, value V, currentDuration time.Duration) time.Duration {
	return currentDuration
}

func (c *varCreating[K, V]) ExpireAfterRead(key K, value V, currentDuration time.Duration) time.Duration {
	return currentDuration
}

// Creating returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation. The expiration time is
// not modified when the entry is updated or read.
func Creating[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return CreatingFunc(func(key K, value V) time.Duration {
		return duration
	})
}

// CreatingFunc returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation. The expiration time is
// not modified when the entry is updated or read.
func CreatingFunc[K comparable, V any](f func(key K, value V) time.Duration) Calculator[K, V] {
	return &varCreating[K, V]{
		f: f,
	}
}

type varWriting[K comparable, V any] struct {
	f func(key K, value V) time.Duration
}

func (w *varWriting[K, V]) ExpireAfterCreate(key K, value V) time.Duration {
	return w.f(key, value)
}

func (w *varWriting[K, V]) ExpireAfterUpdate(key K, value V, currentDuration time.Duration) time.Duration {
	return w.f(key, value)
}

func (w *varWriting[K, V]) ExpireAfterRead(key K, value V, currentDuration time.Duration) time.Duration {
	return currentDuration
}

// Writing returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation or replacement of its value.
// The expiration time is not modified when the entry is read.
func Writing[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return WritingFunc(func(key K, value V) time.Duration {
		return duration
	})
}

// WritingFunc returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation or replacement of its value.
// The expiration time is not modified when the entry is read.
func WritingFunc[K comparable, V any](f func(key K, value V) time.Duration) Calculator[K, V] {
	return &varWriting[K, V]{
		f: f,
	}
}

type varAccessing[K comparable, V any] struct {
	f func(key K, value V) time.Duration
}

func (a *varAccessing[K, V]) ExpireAfterCreate(key K, value V) time.Duration {
	return a.f(key, value)
}

func (a *varAccessing[K, V]) ExpireAfterUpdate(key K, value V, currentDuration time.Duration) time.Duration {
	return a.f(key, value)
}

func (a *varAccessing[K, V]) ExpireAfterRead(key K, value V, currentDuration time.Duration) time.Duration {
	return a.f(key, value)
}

// Accessing returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation, replacement of its value,
// or after it was last read.
func Accessing[K comparable, V any](duration time.Duration) Calculator[K, V] {
	return AccessingFunc(func(key K, value V) time.Duration {
		return duration
	})
}

// AccessingFunc returns a Calculator that specifies that the entry should be automatically deleted from
// the cache once the duration has elapsed after the entry's creation, replacement of its value,
// or after it was last read.
func AccessingFunc[K comparable, V any](f func(key K, value V) time.Duration) Calculator[K, V] {
	return &varAccessing[K, V]{
		f: f,
	}
}
