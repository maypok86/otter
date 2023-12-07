package otter

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestBuilder_MustFailed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recover: ", r)
		}
	}()
	MustBuilder[int, int](-1)
	t.Fatal("no panic detected")
}

func TestBuilder_NewFailed(t *testing.T) {
	_, err := NewBuilder[int, int](-63)
	if err == nil || !errors.Is(err, ErrIllegalCapacity) {
		t.Fatalf("should fail with an error %v, but got %v", ErrIllegalCapacity, err)
	}
}

func TestBuilder_BuildSuccess(t *testing.T) {
	b := MustBuilder[int, int](10)

	c, err := b.
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
