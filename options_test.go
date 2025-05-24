// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
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

	"github.com/maypok86/otter/v2/core/expiry"
	"github.com/maypok86/otter/v2/core/stats"
)

func ptr[T any](t T) *T {
	return &t
}

func TestOptions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		fn   func(o *Options[string, string])
		want *string
	}{
		{
			fn: func(o *Options[string, string]) {
				o.MaximumSize = 100
				o.MaximumWeight = 1000
			},
			want: ptr("otter: both maximumSize and maximumWeight are set"),
		},
		{
			fn: func(o *Options[string, string]) {
				o.MaximumSize = 100
				o.Weigher = func(key string, value string) uint32 {
					return 1
				}
			},
			want: ptr("otter: both maximumSize and weigher are set"),
		},
		{
			fn: func(o *Options[string, string]) {
				o.MaximumSize = -1
			},
			want: ptr("otter: maximumSize should be positive"),
		},
		{
			fn: func(o *Options[string, string]) {
				o.MaximumWeight = 10
			},
			want: ptr("otter: maximumWeight requires weigher"),
		},
		{
			fn: func(o *Options[string, string]) {
				o.Weigher = func(key string, value string) uint32 {
					return 0
				}
			},
			want: ptr("otter: weigher requires maximumWeight"),
		},
		{
			fn: func(o *Options[string, string]) {
				o.InitialCapacity = -1
			},
			want: ptr("otter: initial capacity should be positive"),
		},
		{
			fn: func(o *Options[string, string]) {
				o.MaximumWeight = 10
				o.StatsRecorder = stats.NewCounter()
				o.InitialCapacity = 10
				o.Weigher = func(key string, value string) uint32 {
					return 2
				}
				o.ExpiryCalculator = expiry.Writing[string, string](time.Hour)
			},
			want: nil,
		},
	} {
		o := &Options[string, string]{}
		test.fn(o)
		err := o.validate()
		if test.want == nil && err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if test.want != nil && err == nil {
			t.Fatalf("wanted error: %s, but got nil", *test.want)
		}
	}
}
