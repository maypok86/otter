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
	"testing"
	"time"

	"github.com/maypok86/otter/v2/stats"
)

func TestBuilder_NewFailed(t *testing.T) {
	_, err := NewBuilder[int, int](0).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}

	capacity := uint64(100)
	// negative const ttl
	_, err = NewBuilder[int, int](capacity).WithTTL(-1).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}

	// negative initial capacity
	_, err = NewBuilder[int, int](capacity).InitialCapacity(-2).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}

	_, err = NewBuilder[int, int](capacity).WithTTL(time.Hour).InitialCapacity(0).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}

	_, err = NewBuilder[int, int](capacity).WithVariableTTL().InitialCapacity(-5).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}

	// nil weigher
	_, err = NewBuilder[int, int](capacity).Weigher(nil).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}

	// nil stats collector
	_, err = NewBuilder[int, int](capacity).CollectStats(nil).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}

	// nil logger
	_, err = NewBuilder[int, int](capacity).Logger(nil).Build()
	if err == nil {
		t.Fatalf("should fail with an error")
	}
}

func TestBuilder_BuildSuccess(t *testing.T) {
	_, err := NewBuilder[int, int](10).
		CollectStats(stats.NewCounter()).
		InitialCapacity(10).
		Weigher(func(key int, value int) uint32 {
			return 2
		}).
		WithTTL(time.Hour).
		Build()
	if err != nil {
		t.Fatalf("builded cache with error: %v", err)
	}
}
