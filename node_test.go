// Copyright (c) 2026 Alexey Mayshev and contributors. All rights reserved.
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
	"fmt"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

// nodeConfigs returns every node configuration a cache can use.
func nodeConfigs() []node.Config {
	var configs []node.Config
	for _, withSize := range []bool{false, true} {
		for _, withExpiration := range []bool{false, true} {
			for _, withRefresh := range []bool{false, true} {
				for _, withWeight := range []bool{false, true} {
					if withSize && withWeight {
						continue
					}
					configs = append(configs, node.Config{
						WithSize:       withSize,
						WithExpiration: withExpiration,
						WithRefresh:    withRefresh,
						WithWeight:     withWeight,
					})
				}
			}
		}
	}
	return configs
}

// Nodes are compared with ==, which holds only if a missing node is a nil interface. A typed
// nil (a nil node pointer in a non-nil interface) would make n == nil false, and the eviction
// loops would never see the end of a queue.
func TestNode_MissingNodesAreNilInterfaces(t *testing.T) {
	t.Parallel()

	for _, cfg := range nodeConfigs() {
		t.Run(fmt.Sprintf("%+v", cfg), func(t *testing.T) {
			t.Parallel()

			nm := node.NewManager[int, int](cfg)
			n := nm.Create(1, 1, 0, 0, 1)
			u := nm.CreateUpdatable(2, 2, 0, 0, 1)

			if got := nm.FromPointer(nil); got != nil {
				t.Fatalf("FromPointer(nil) = %#v, want a nil interface", got)
			}
			for _, n := range []node.Node[int, int]{n, u} {
				if cfg.WithSize || cfg.WithWeight {
					if n.Prev() != nil || n.Next() != nil {
						t.Fatalf("Prev() = %#v, Next() = %#v, want nil interfaces", n.Prev(), n.Next())
					}
				}
				if cfg.WithExpiration {
					if n.PrevExp() != nil || n.NextExp() != nil {
						t.Fatalf("PrevExp() = %#v, NextExp() = %#v, want nil interfaces", n.PrevExp(), n.NextExp())
					}
				}
			}
		})
	}
}

// The eviction policy's queues and the timer wheel write a node's links through raw pointers
// at the offsets the manager reports, while the policy's eviction loops read them through the
// node's methods. Both must address the same fields for every node type.
func TestNode_LinksOffsetsMatchMethods(t *testing.T) {
	t.Parallel()

	for _, cfg := range nodeConfigs() {
		t.Run(fmt.Sprintf("%+v", cfg), func(t *testing.T) {
			t.Parallel()

			nm := node.NewManager[int, int](cfg)
			n := nm.Create(1, 1, 0, 0, 1)
			other := nm.Create(2, 2, 0, 0, 1)
			linksAt := func(offset uintptr) *node.Links {
				return (*node.Links)(unsafe.Add(n.AsPointer(), offset))
			}

			if cfg.WithSize || cfg.WithWeight {
				l := linksAt(nm.LinksOffset())
				l.Prev, l.Next = other.AsPointer(), other.AsPointer()
				if n.Prev() != other || n.Next() != other {
					t.Fatalf("Prev() = %#v, Next() = %#v, want the node written at LinksOffset", n.Prev(), n.Next())
				}
				if cfg.WithExpiration && (n.PrevExp() != nil || n.NextExp() != nil) {
					t.Fatalf("writing at LinksOffset changed the expiration links")
				}
			} else {
				require.Panics(t, func() { nm.LinksOffset() })
			}

			if cfg.WithExpiration {
				l := linksAt(nm.ExpLinksOffset())
				l.Prev, l.Next = other.AsPointer(), other.AsPointer()
				if n.PrevExp() != other || n.NextExp() != other {
					t.Fatalf("PrevExp() = %#v, NextExp() = %#v, want the node written at ExpLinksOffset", n.PrevExp(), n.NextExp())
				}
			} else {
				require.Panics(t, func() { nm.ExpLinksOffset() })
			}
			if n.Key() != 1 {
				t.Fatalf("Key() = %d after writing the links, want 1", n.Key())
			}
		})
	}
}
