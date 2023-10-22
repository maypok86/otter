package otter

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuilder_NewFailed(t *testing.T) {
	_, err := NewBuilder[int, int](-63)
	if err == nil || !errors.Is(err, ErrIllegalCapacity) {
		t.Fatalf("should fail with an error %v, but got %v", ErrIllegalCapacity, err)
	}
}

func TestBuilder_BuildFailed(t *testing.T) {
	b, err := NewBuilder[int, int](10)
	if err != nil {
		t.Fatalf("builder creates with error %v", err)
	}

	_, err = b.ShardCount(129).Build()
	if err == nil || !errors.Is(err, ErrIllegalShardCount) {
		t.Fatalf("should fail with an error %v, but got %v", ErrIllegalShardCount, err)
	}
}

func TestBuilder_BuildSuccess(t *testing.T) {
	b, err := NewBuilder[int, int](10)
	if err != nil {
		t.Fatalf("builder creates with error %v", err)
	}

	c, err := b.
		ShardCount(256).
		StatsEnabled(true).
		Cost(func(key int, value int) uint32 {
			return 2
		}).Build()
	if err != nil {
		t.Fatalf("builded cache with error: %v", err)
	}

	if !reflect.DeepEqual(reflect.TypeOf(&Cache[int, int]{}), reflect.TypeOf(c)) {
		t.Fatalf("builder returned a different type of cache: %v", err)
	}
}
