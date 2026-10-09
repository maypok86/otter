// Copyright (c) 2026 Alexey Mayshev and contributors. All rights reserved.
// Copyright (c) 2021 Andrey Pechkurov. All rights reserved.
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
//
// Copyright notice. TestHashUint64_NoDifferentialBias is a fork of a test for xsync.Map from this file:
// https://github.com/puzpuzpuz/xsync/blob/main/map_test.go

package xruntime

import "testing"

type userID int32

func TestHasher_IntKeys(t *testing.T) {
	t.Parallel()

	if !NewHasher[int]().isInt || !NewHasher[uint8]().isInt || !NewHasher[uintptr]().isInt {
		t.Fatal("integer keys are expected to take the integer path")
	}
	if !NewHasher[userID]().isInt {
		t.Fatal("named integer keys are expected to take the integer path")
	}
	if NewHasher[string]().isInt || NewHasher[float64]().isInt || NewHasher[[2]int]().isInt {
		t.Fatal("non-integer keys are expected to use maphash")
	}

	h := NewHasher[userID]()
	if h.Hash(-1) == h.Hash(1) {
		t.Fatal("sign must take part in the hash")
	}
	a, b := userID(42), userID(42)
	if h.Hash(a) != h.Hash(b) {
		t.Fatal("equal keys must have equal hashes")
	}
}

// TestHashUint64_NoDifferentialBias verifies that keys differing by a fixed
// XOR delta do not collide more often than chance, whatever the seed is
// (see https://github.com/puzpuzpuz/xsync/issues/192). It checks the low bits
// and the bits from 7 up, which the hash table takes the bucket index from.
func TestHashUint64_NoDifferentialBias(t *testing.T) {
	t.Parallel()

	const (
		nBuckets  = 256
		mask      = nBuckets - 1
		nTrials   = 20_000
		threshold = 3.0
	)
	expected := float64(nTrials) / float64(nBuckets)

	deltas := []uint64{
		0x0000015000000000,
		0x0000004F00000000,
		0x000000A300000000,
		0x00000D0000000000,
	}
	for _, shift := range []uint{0, 8, 16, 24, 32, 40, 48} {
		for k := uint64(1); k <= 64; k++ {
			deltas = append(deltas, k<<shift)
		}
	}
	for b := 0; b < 64; b++ {
		deltas = append(deltas, 1<<b)
	}

	for _, seed := range []uint64{0, 42, 0xDEADBEEF} {
		for _, delta := range deltas {
			for _, shift := range []uint{0, 7} {
				collisions := 0
				for i := 0; i < nTrials; i++ {
					v := uint64(i) * 0x9E3779B97F4A7C15
					if (hashUint64(seed, v)>>shift)&mask == (hashUint64(seed, v^delta)>>shift)&mask {
						collisions++
					}
				}
				if ratio := float64(collisions) / expected; ratio > threshold {
					t.Errorf("seed=%#x delta=%#x shift=%d: %.2fx the expected collision rate",
						seed, delta, shift, ratio)
				}
			}
		}
	}
}
