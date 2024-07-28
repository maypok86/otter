// Copyright (c) 2024 Alexey Mayshev. All rights reserved.
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
	"math"
	"testing"
	"unsafe"
)

func TestStats(t *testing.T) {
	var s Stats
	if s.Ratio() != 0.0 {
		t.Fatalf("not valid hit ratio. want 0.0, got %.2f", s.Ratio())
	}

	expected := int64(math.MaxInt64)

	s = Stats{
		hits:         math.MaxInt64,
		misses:       math.MaxInt64,
		rejectedSets: math.MaxInt64,
		evictedCount: math.MaxInt64,
		evictedCost:  math.MaxInt64,
	}

	if s.Hits() != expected {
		t.Fatalf("not valid hits. want %d, got %d", expected, s.Hits())
	}

	if s.Misses() != expected {
		t.Fatalf("not valid misses. want %d, got %d", expected, s.Misses())
	}

	if s.Ratio() != 1.0 {
		t.Fatalf("not valid hit ratio. want 1.0, got %.2f", s.Ratio())
	}

	if s.RejectedSets() != expected {
		t.Fatalf("not valid rejected sets. want %d, got %d", expected, s.RejectedSets())
	}

	if s.EvictedCount() != expected {
		t.Fatalf("not valid evicted count. want %d, got %d", expected, s.EvictedCount())
	}

	if s.EvictedCost() != expected {
		t.Fatalf("not valid evicted cost. want %d, got %d", expected, s.EvictedCost())
	}
}

func TestCheckedAdd_Overflow(t *testing.T) {
	if unsafe.Sizeof(t) != 8 {
		t.Skip()
	}

	a := int64(math.MaxInt64)
	b := int64(23)

	if got := checkedAdd(a, b); got != math.MaxInt64 {
		t.Fatalf("wrong overflow in checkedAdd. want %d, got %d", int64(math.MaxInt64), got)
	}
}

func TestCheckedAdd_Underflow(t *testing.T) {
	if unsafe.Sizeof(t) != 8 {
		t.Skip()
	}

	a := int64(math.MinInt64 + 10)
	b := int64(-23)

	if got := checkedAdd(a, b); got != math.MinInt64 {
		t.Fatalf("wrong underflow in checkedAdd. want %d, got %d", int64(math.MinInt64), got)
	}
}
