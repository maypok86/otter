package otter

import (
	"errors"

	"github.com/maypok86/otter/internal/xmath"
)

const (
	illegalCapacity     = -1
	illegalShardCount   = -1
	defaultShardCount   = 512
	defaultStatsEnabled = false
)

var (
	ErrIllegalCapacity   = errors.New("capacity should be positive")
	ErrIllegalShardCount = errors.New("shard count should be positive")
)

type Option func(*options)

type options struct {
	capacity     int
	shardCount   int
	statsEnabled bool
}

func defaultOptions() *options {
	return &options{
		capacity:     illegalCapacity,
		shardCount:   defaultShardCount,
		statsEnabled: defaultStatsEnabled,
	}
}

func (o *options) validate() error {
	if o.capacity == illegalCapacity {
		return ErrIllegalCapacity
	}

	if o.shardCount == illegalShardCount {
		return ErrIllegalShardCount
	}

	return nil
}

func WithCapacity(capacity int) Option {
	return func(o *options) {
		if capacity <= 0 {
			o.capacity = illegalCapacity
			return
		}

		o.capacity = capacity
	}
}

func WithShardCount(shardCount int) Option {
	return func(o *options) {
		if shardCount < 2 {
			o.shardCount = illegalShardCount
			return
		}

		o.shardCount = int(xmath.RoundUpPowerOf2(uint32(shardCount)))
	}
}

func WithStatsEnabled(statsEnabled bool) Option {
	return func(o *options) {
		o.statsEnabled = statsEnabled
	}
}
