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

func ptr[T any](t T) *T {
	return &t
}

func TestBuilder_Build(t *testing.T) {
	for _, test := range []struct {
		fn   func(b *Builder[string, string])
		want *string
	}{
		{
			fn: func(b *Builder[string, string]) {
				b.MaximumSize(100).MaximumWeight(uint64(1000))
			},
			want: ptr("otter: both maximumSize and maximumWeight are set"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.MaximumSize(100).Weigher(func(key string, value string) uint32 {
					return 1
				})
			},
			want: ptr("otter: both maximumSize and weigher are set"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.MaximumSize(0)
			},
			want: ptr("otter: maximumSize should be positive"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.MaximumWeight(0).Weigher(func(key string, value string) uint32 {
					return 1
				})
			},
			want: ptr("otter: maximumWeight should be positive"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.MaximumWeight(10)
			},
			want: ptr("otter: maximumWeight requires weigher"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.Weigher(func(key string, value string) uint32 {
					return 0
				})
			},
			want: ptr("otter: weigher requires maximumWeight"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.Weigher(func(key string, value string) uint32 {
					return 0
				})
			},
			want: ptr("otter: weigher requires maximumWeight"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.Weigher(func(key string, value string) uint32 {
					return 0
				})
			},
			want: ptr("otter: weigher requires maximumWeight"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.InitialCapacity(0)
			},
			want: ptr("otter: initial capacity should be positive"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.InitialCapacity(0)
			},
			want: ptr("otter: initial capacity should be positive"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.RecordStats(nil)
			},
			want: ptr("otter: stats collector should not be nil"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.Logger(nil)
			},
			want: ptr("otter: logger should not be nil"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.WithTTL(-1)
			},
			want: ptr("otter: ttl should be positive"),
		},
		{
			fn: func(b *Builder[string, string]) {
				b.MaximumWeight(10).
					RecordStats(stats.NewCounter()).
					InitialCapacity(10).
					Weigher(func(key string, value string) uint32 {
						return 2
					}).
					WithTTL(time.Hour)
			},
			want: nil,
		},
	} {
		b := NewBuilder[string, string]()
		test.fn(b)
		_, err := b.Build()
		if test.want == nil && err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if test.want != nil && err == nil {
			t.Fatalf("wanted error: %s, but got nil", *test.want)
		}
	}
}
