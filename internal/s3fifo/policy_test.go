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

package s3fifo

import (
	"testing"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func newNode(k int) node.Node[int, int] {
	m := node.NewManager[int, int](node.Config{})
	n := m.Create(k, k, 0, 1)
	return n
}

func TestPolicy_ReadAndWrite(t *testing.T) {
	n := newNode(2)
	p := NewPolicy[int, int](10, func(n node.Node[int, int]) {
	})
	p.Add(n)
	if !n.IsSmall() {
		t.Fatalf("not valid node state: %+v", n)
	}
}

func TestPolicy_OneHitWonders(t *testing.T) {
	p := NewPolicy[int, int](10, func(n node.Node[int, int]) {
	})

	oneHitWonders := make([]node.Node[int, int], 0, 2)
	for i := 0; i < cap(oneHitWonders); i++ {
		oneHitWonders = append(oneHitWonders, newNode(i+1))
	}

	popular := make([]node.Node[int, int], 0, 8)
	for i := 0; i < cap(popular); i++ {
		popular = append(popular, newNode(i+3))
	}

	for _, n := range oneHitWonders {
		p.Add(n)
	}

	for _, n := range popular {
		p.Add(n)
	}

	p.Read(oneHitWonders)
	for i := 0; i < 3; i++ {
		p.Read(popular)
	}

	newNodes := make([]node.Node[int, int], 0, 11)
	for i := 0; i < cap(newNodes); i++ {
		newNodes = append(newNodes, newNode(i+12))
	}

	for _, n := range newNodes {
		p.Add(n)
	}

	for _, n := range oneHitWonders {
		if n.IsSmall() || n.IsMain() {
			t.Fatalf("one hit wonder should be evicted: %+v", n)
		}
	}

	for _, n := range popular {
		if !n.IsMain() {
			t.Fatalf("popular objects should be in main queue: %+v", n)
		}
	}

	for _, n := range oneHitWonders {
		p.Delete(n)
	}
	for _, n := range popular {
		p.Delete(n)
	}
	for _, n := range newNodes {
		p.Delete(n)
	}

	if p.small.cost+p.main.cost != 0 {
		t.Fatalf("queues should be empty, but small size: %d, main size: %d", p.small.cost, p.main.cost)
	}
}

func TestPolicy_Update(t *testing.T) {
	collect := false
	var deleted []node.Node[int, int]
	p := NewPolicy[int, int](100, func(n node.Node[int, int]) {
		if collect {
			deleted = deleted[:0]
			deleted = append(deleted, n)
		}
	})

	n := newNode(1)
	m := node.NewManager[int, int](node.Config{WithCost: true})
	n1 := m.Create(1, 1, 0, n.Cost()+8)

	p.Add(n)
	p.Delete(n)
	p.Add(n1)

	p.Read([]node.Node[int, int]{n1, n1})

	n2 := m.Create(2, 1, 0, 92)
	collect = true
	p.Add(n2)

	if !n1.IsMain() {
		t.Fatalf("updated node should be in main queue: %+v", n1)
	}

	if n2.IsSmall() || n2.IsMain() || len(deleted) != 1 || deleted[0] != n2 {
		t.Fatalf("inserted node should be evicted: %+v", n2)
	}

	n3 := m.Create(1, 1, 0, 109)
	p.Delete(n1)
	p.Add(n3)
	if n3.IsSmall() || n3.IsMain() || len(deleted) != 1 || deleted[0] != n3 {
		t.Fatalf("updated node should be evicted: %+v", n3)
	}
}
