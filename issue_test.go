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
	"fmt"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

type triple struct {
	a int
	b int
	c int
}

// https://github.com/maypok86/otter/issues/132
func TestCache_Issue132(t *testing.T) {
	cache := Must(&Options[string, triple]{})

	accessCounter := 0

	loader := LoaderFunc[string, triple](func(_ context.Context, key string) (triple, error) {
		defer func() {
			accessCounter++
		}()
		if accessCounter == 0 {
			return triple{a: 1}, fmt.Errorf("failed to load b")
		}
		return triple{a: 3, b: 4}, nil
	})

	value1, err1 := cache.Get(t.Context(), "key", loader)
	require.Error(t, err1)
	require.Equal(t, 1, value1.a)
	require.Equal(t, 1, accessCounter)

	value2, err2 := cache.Get(t.Context(), "key", loader)
	require.NoError(t, err2)
	require.Equal(t, 3, value2.a)
	require.Equal(t, 4, value2.b)
	require.Equal(t, 2, accessCounter)

	value3, err3 := cache.Get(t.Context(), "key", loader)
	require.NoError(t, err3)
	require.Equal(t, 3, value3.a)
	require.Equal(t, 4, value3.b)
	require.Equal(t, 2, accessCounter)
}

// https://github.com/maypok86/otter/issues/139
func TestCache_Issue139(t *testing.T) {
	t.Parallel()

	cache := Must(&Options[string, string]{})

	loader := func(ctx context.Context, key string) (string, error) {
		return "v1", nil
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < 10000000; i++ {
			cache.InvalidateAll()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < 10000000; i++ {
			v, _ := cache.Get(context.Background(), "v1", LoaderFunc[string, string](loader))
			if v != "v1" {
				t.Errorf("expected v1, got %s", v)
			}
		}
	}()

	wg.Wait()
}
