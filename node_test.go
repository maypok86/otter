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

	"github.com/maypok86/otter/v2/internal/generated/node"
)

// Nodes are compared with ==, which holds only if a missing node is a nil interface. A typed
// nil (a nil node pointer in a non-nil interface) would make n == nil false, and the eviction
// loops would never see the end of a queue.
func TestNode_MissingNodesAreNilInterfaces(t *testing.T) {
	t.Parallel()

	for _, withSize := range []bool{false, true} {
		for _, withExpiration := range []bool{false, true} {
			for _, withRefresh := range []bool{false, true} {
				for _, withWeight := range []bool{false, true} {
					if withSize && withWeight {
						continue
					}
					cfg := node.Config{
						WithSize:       withSize,
						WithExpiration: withExpiration,
						WithRefresh:    withRefresh,
						WithWeight:     withWeight,
					}
					t.Run(fmt.Sprintf("%+v", cfg), func(t *testing.T) {
						t.Parallel()

						nm := node.NewManager[int, int](cfg)
						n := nm.Create(1, 1, 0, 0, 1)
						u := nm.CreateUpdatable(2, 2, 0, 0, 1)

						if got := nm.FromPointer(nil); got != nil {
							t.Fatalf("FromPointer(nil) = %#v, want a nil interface", got)
						}
						for _, n := range []node.Node[int, int]{n, u} {
							if withSize || withWeight {
								if n.Prev() != nil || n.Next() != nil {
									t.Fatalf("Prev() = %#v, Next() = %#v, want nil interfaces", n.Prev(), n.Next())
								}
							}
							if withExpiration {
								if n.PrevExp() != nil || n.NextExp() != nil {
									t.Fatalf("PrevExp() = %#v, NextExp() = %#v, want nil interfaces", n.PrevExp(), n.NextExp())
								}
							}
						}
					})
				}
			}
		}
	}
}
