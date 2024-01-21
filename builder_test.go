// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otter

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
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

	_, err = MustBuilder[int, int](100).WithTTL(-1).Build()
	if err == nil || !errors.Is(err, ErrIllegalTTL) {
		t.Fatalf("should fail with an error %v, but got %v", ErrIllegalTTL, err)
	}
}

func TestBuilder_BuildSuccess(t *testing.T) {
	b := MustBuilder[int, int](10)

	c, err := b.
		CollectStats().
		Cost(func(key int, value int) uint32 {
			return 2
		}).Build()
	if err != nil {
		t.Fatalf("builded cache with error: %v", err)
	}

	if !reflect.DeepEqual(reflect.TypeOf(Cache[int, int]{}), reflect.TypeOf(c)) {
		t.Fatalf("builder returned a different type of cache: %v", err)
	}

	cc, err := b.WithTTL(time.Minute).CollectStats().Cost(func(key int, value int) uint32 {
		return 2
	}).Build()
	if err != nil {
		t.Fatalf("builded cache with error: %v", err)
	}

	if !reflect.DeepEqual(reflect.TypeOf(Cache[int, int]{}), reflect.TypeOf(cc)) {
		t.Fatalf("builder returned a different type of cache: %v", err)
	}

	cv, err := b.WithVariableTTL().CollectStats().Cost(func(key int, value int) uint32 {
		return 2
	}).Build()
	if err != nil {
		t.Fatalf("builded cache with error: %v", err)
	}

	if !reflect.DeepEqual(reflect.TypeOf(CacheWithVariableTTL[int, int]{}), reflect.TypeOf(cv)) {
		t.Fatalf("builder returned a different type of cache: %v", err)
	}
}
