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

// Header(nil), SentinelHeader(nil), and EntryHeader(nil) must return untyped nil HeaderIface
// values, and missing handle conversions and links must return nil pointers across all 12 configs.
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
						n := nm.Create(1, 10, 0, 0, 1)
						u := nm.CreateUpdatable(2, 20, 0, 0, 1)

						if got := nm.Header(nil); got != nil {
							t.Fatalf("Header(nil) = %#v, want untyped nil interface", got)
						}
						if got := nm.SentinelHeader(nil); got != nil {
							t.Fatalf("SentinelHeader(nil) = %#v, want untyped nil interface", got)
						}
						if got := nm.EntryHeader(nil); got != nil {
							t.Fatalf("EntryHeader(nil) = %#v, want untyped nil interface", got)
						}
						if got := nm.FromHeader(nil); got != nil {
							t.Fatalf("FromHeader(nil) = %#v, want nil", got)
						}
						if got := nm.FromKeyed(nil); got != nil {
							t.Fatalf("FromKeyed(nil) = %#v, want nil", got)
						}
						lens := nm.KeyLens()
						if got := lens.FromHeader(nil); got != nil {
							t.Fatalf("KeyLens.FromHeader(nil) = %#v, want nil", got)
						}
						if got := lens.Key(n.Keyed()); got != 1 {
							t.Fatalf("KeyLens.Key(n) = %d, want 1", got)
						}
						withState := withSize || withExpiration || withWeight
						if val, hdr, word := nm.ValueAndIdentity(n); val != 10 || hdr != nil || (withState && word != 10) {
							t.Fatalf("ValueAndIdentity(n) = (%d, %v, %d), want (10, nil, word)", val, hdr, word)
						}
						nmBox := node.NewManager[int, [3]int](cfg)
						ub := nmBox.CreateUpdatable(2, [3]int{7, 8, 9}, 0, 0, 1)
						if bval, bhdr, _ := nmBox.ValueAndIdentity(ub); bval != ([3]int{7, 8, 9}) || (withState && bhdr == nil) || (!withState && bhdr != nil) {
							t.Fatalf("ValueAndIdentity(ub) = (%v, %v), withState=%v", bval, bhdr, withState)
						}

						for _, e := range []*node.Entry[int, int]{n, u} {
							nh := nm.EntryHeader(e)
							if withSize || withWeight {
								if nh.Prev() != nil || nh.Next() != nil {
									t.Fatalf("Prev() = %#v, Next() = %#v, want nil pointers", nh.Prev(), nh.Next())
								}
							}
							if withExpiration {
								if nh.PrevExp() != nil || nh.NextExp() != nil {
									t.Fatalf("PrevExp() = %#v, NextExp() = %#v, want nil pointers", nh.PrevExp(), nh.NextExp())
								}
							}
						}
						if withExpiration {
							s := nm.NewSentinel()
							sh := nm.SentinelHeader(s)
							if sh.PrevExp() != nil || sh.NextExp() != nil {
								t.Fatalf("NewSentinel initial links (%v, %v), want (nil, nil)", sh.PrevExp(), sh.NextExp())
							}
							sh.SetPrevExp(s.Header())
							sh.SetNextExp(s.Header())
							if sh.PrevExp() != s.Header() || sh.NextExp() != s.Header() {
								t.Fatalf("NewSentinel self links (%v, %v), want self %v", sh.PrevExp(), sh.NextExp(), s.Header())
							}
						}
					})
				}
			}
		}
	}
}
