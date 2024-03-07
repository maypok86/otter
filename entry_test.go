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
	"testing"
	"time"
)

func TestEntry(t *testing.T) {
	k := 2
	v := 3
	exp := int64(0)
	c := uint32(5)
	e := Entry[int, int]{
		key:        k,
		value:      v,
		expiration: exp,
		cost:       c,
	}

	if e.Key() != k {
		t.Fatalf("not valid key. want %d, got %d", k, e.Key())
	}
	if e.Value() != v {
		t.Fatalf("not valid value. want %d, got %d", v, e.Value())
	}
	if e.Cost() != c {
		t.Fatalf("not valid cost. want %d, got %d", c, e.Cost())
	}
	if e.Expiration() != exp {
		t.Fatalf("not valid expiration. want %d, got %d", exp, e.Expiration())
	}
	if ttl := e.TTL(); ttl != -1 {
		t.Fatalf("not valid ttl. want -1, got %d", ttl)
	}
	if e.HasExpired() {
		t.Fatal("entry should not be expire")
	}

	newTTL := int64(10)
	e.expiration = time.Now().Unix() + newTTL
	if ttl := e.TTL(); ttl <= 0 || ttl > time.Duration(newTTL)*time.Second {
		t.Fatalf("ttl should be in the range (0, %d] seconds, but got %d seconds", newTTL, ttl/time.Second)
	}
	if e.HasExpired() {
		t.Fatal("entry should not be expire")
	}

	e.expiration -= 2 * newTTL
	if ttl := e.TTL(); ttl != 0 {
		t.Fatalf("ttl should be 0 seconds, but got %d seconds", ttl/time.Second)
	}
	if !e.HasExpired() {
		t.Fatalf("entry should have expired")
	}
}
