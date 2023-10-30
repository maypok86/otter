package otter

import "errors"

const (
	defaultShardCount   = 128
	defaultStatsEnabled = false
)

var (
	ErrIllegalCapacity   = errors.New("capacity should be positive")
	ErrIllegalShardCount = errors.New("hashtable count should be positive")
)

type options[K comparable, V any] struct {
	capacity     int
	shardCount   int
	statsEnabled bool
	costFunc     func(key K, value V) uint32
}

func (o *options[K, V]) validate() error {
	// shard count should be power of two.
	if o.shardCount <= 0 || (o.shardCount&(o.shardCount-1)) != 0 {
		return ErrIllegalShardCount
	}

	return nil
}

func (o *options[K, V]) toConfig() Config[K, V] {
	return Config[K, V]{
		Capacity:     o.capacity,
		ShardCount:   o.shardCount,
		StatsEnabled: o.statsEnabled,
		CostFunc:     o.costFunc,
	}
}

type Builder[K comparable, V any] struct {
	options[K, V]
}

func MustBuilder[K comparable, V any](capacity int) *Builder[K, V] {
	b, err := NewBuilder[K, V](capacity)
	if err != nil {
		panic(err)
	}
	return b
}

func NewBuilder[K comparable, V any](capacity int) (*Builder[K, V], error) {
	if capacity <= 0 {
		return nil, ErrIllegalCapacity
	}

	return &Builder[K, V]{
		options: options[K, V]{
			capacity:     capacity,
			shardCount:   defaultShardCount,
			statsEnabled: defaultStatsEnabled,
			costFunc: func(key K, value V) uint32 {
				return 1
			},
		},
	}, nil
}

func (b *Builder[K, V]) ShardCount(shardCount int) *Builder[K, V] {
	b.shardCount = shardCount
	return b
}

func (b *Builder[K, V]) StatsEnabled(statsEnabled bool) *Builder[K, V] {
	b.statsEnabled = statsEnabled
	return b
}

func (b *Builder[K, V]) Cost(costFunc func(key K, value V) uint32) *Builder[K, V] {
	b.costFunc = costFunc
	return b
}

func (b *Builder[K, V]) Build() (*Cache[K, V], error) {
	if err := b.validate(); err != nil {
		return nil, err
	}

	return NewCache(b.toConfig()), nil
}
