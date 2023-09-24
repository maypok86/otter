package shard

import "github.com/dolthub/maphash"

type Option[K comparable] func(*options[K])

type options[K comparable] struct {
	initNodeCount int
	hasher        func(K) uint64
}

func defaultOptions[K comparable]() *options[K] {
	hasher := maphash.NewHasher[K]()
	return &options[K]{
		initNodeCount: minNodeCount,
		hasher:        hasher.Hash,
	}
}

func WithHasher[K comparable](hasher func(K) uint64) Option[K] {
	return func(o *options[K]) {
		o.hasher = hasher
	}
}

func WithNodeCount[K comparable](initNodeCount int) Option[K] {
	return func(o *options[K]) {
		o.initNodeCount = initNodeCount
	}
}
