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
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoader(t *testing.T) {
	t.Parallel()

	t.Run("LoaderFunc", func(t *testing.T) {
		t.Parallel()

		l := LoaderFunc[int, int](func(ctx context.Context, key int) (int, error) {
			return key + 10, nil
		})

		var _ Loader[int, int] = l
		ctx := context.Background()
		k := 5
		v, err := l.Load(ctx, k)
		require.NoError(t, err)
		require.Equal(t, k+10, v)
		v, err = l.Reload(ctx, k, 1)
		require.NoError(t, err)
		require.Equal(t, k+10, v)
	})
	t.Run("BulkLoaderFunc", func(t *testing.T) {
		t.Parallel()

		l := BulkLoaderFunc[int, int](func(ctx context.Context, keys []int) (map[int]int, error) {
			m := make(map[int]int, len(keys))
			for _, k := range keys {
				m[k] = k + 100
			}
			return m, nil
		})

		var _ BulkLoader[int, int] = l
		ctx := context.Background()
		keys := []int{1, 2}
		res, err := l.BulkLoad(ctx, keys)
		require.NoError(t, err)
		require.Equal(t, len(keys), len(res))
		for k, v := range res {
			require.Equal(t, k+100, v)
		}
		res, err = l.BulkReload(ctx, keys, nil)
		require.NoError(t, err)
		require.Equal(t, len(keys), len(res))
		for k, v := range res {
			require.Equal(t, k+100, v)
		}
	})
}
