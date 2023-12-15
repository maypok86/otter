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

	// hash
	if n.Hash() != 0 {
		t.Fatalf("n.Hash() = %d, want %d", n.Hash(), 0)
	}

	hash := uint64(4)
	n.SetHash(hash)
	if n.Hash() != hash {
		t.Fatalf("n.Hash() = %d, want %d", n.Hash(), hash)
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
	oldCost := n.SwapCost(newCost)
	if cost != oldCost {
		t.Fatalf("old cost = %d, want %d", oldCost, cost)
	}

	if n.Cost() != newCost {
		t.Fatalf("n.Cost() = %d, want %d", n.Cost(), newCost)
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
