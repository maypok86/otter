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

package node

import "testing"

func TestNode(t *testing.T) {
	key := 1
	value := 2
	expiration := uint32(6)
	cost := uint32(4)
	n := New[int, int](key, value, expiration, cost)

	// key
	if n.Key() != key {
		t.Fatalf("n.Key() = %d, want %d", n.Key(), key)
	}

	// value
	if n.Value() != value {
		t.Fatalf("n.Value() = %d, want %d", n.Value(), value)
	}

	newValue := 3
	n.SetValue(newValue)
	if n.Value() != newValue {
		t.Fatalf("n.Value() = %d, want %d", n.Value(), newValue)
	}

	// expiration
	if n.IsExpired() {
		t.Fatalf("node shouldn't be expired")
	}

	if n.Expiration() != expiration {
		t.Fatalf("n.Exiration() = %d, want %d", n.Expiration(), expiration)
	}

	// cost
	if n.Cost() != cost {
		t.Fatalf("n.Cost() = %d, want %d", n.Cost(), cost)
	}

	newCost := uint32(5)
	n.SetCost(newCost)
	if n.Cost() != newCost {
		t.Fatalf("n.Cost() = %d, want %d", n.Cost(), cost)
	}

	// policy cost
	if n.PolicyCost() != 0 {
		t.Fatalf("n.PolicyCost() = %d, want %d", n.PolicyCost(), 0)
	}

	n.AddPolicyCostDiff(1)
	if n.PolicyCost() != 1 {
		t.Fatalf("n.PolicyCost() = %d, want %d", n.PolicyCost(), 1)
	}

	// frequency
	for i := uint8(0); i < 10; i++ {
		if i < 4 {
			if n.Frequency() != i {
				t.Fatalf("n.Frequency() = %d, want %d", n.Frequency(), i)
			}
		} else {
			if n.Frequency() != 3 {
				t.Fatalf("n.Frequency() = %d, want %d", n.Frequency(), 3)
			}
		}
		n.IncrementFrequency()
	}

	n.DecrementFrequency()
	n.DecrementFrequency()
	if n.Frequency() != 1 {
		t.Fatalf("n.Frequency() = %d, want %d", n.Frequency(), 1)
	}

	n.IncrementFrequency()
	n.ResetFrequency()
	if n.Frequency() != 0 {
		t.Fatalf("n.Frequency() = %d, want %d", n.Frequency(), 0)
	}

	// queueType
	if n.IsSmall() || n.IsMain() {
		t.Fatalf("queueType should be unknown")
	}

	n.MarkSmall()
	if !n.IsSmall() || n.IsMain() {
		t.Fatalf("queueType should be smallQueue")
	}
	n.MarkSmall()
	if !n.IsSmall() || n.IsMain() {
		t.Fatalf("queueType should be smallQueue")
	}

	n.MarkMain()
	if n.IsSmall() || !n.IsMain() {
		t.Fatalf("queueType should be mainQueue")
	}
	n.MarkMain()
	if n.IsSmall() || !n.IsMain() {
		t.Fatalf("queueType should be mainQueue")
	}

	n.Unmark()
	if n.IsSmall() || n.IsMain() {
		t.Fatalf("queueType should be unknown")
	}
}
