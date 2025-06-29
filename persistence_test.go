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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSaveLoadCache(t *testing.T) {
	t.Parallel()

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{})

		err := LoadCacheFromFile(c, "not-found.gob")
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		const (
			maximum          = 5
			expiresAfter     = time.Hour
			refreshableAfter = 30 * time.Minute
			filePath         = "./ololo/ok.gob"
		)
		fs := &fakeSource{}
		c := Must(&Options[int, int]{
			MaximumSize:       maximum,
			ExpiryCalculator:  ExpiryWriting[int, int](expiresAfter),
			RefreshCalculator: RefreshWriting[int, int](refreshableAfter),
			Clock:             fs,
		})

		for i := 0; i < maximum; i++ {
			c.Set(i, -i)
		}

		err := SaveCacheToFile(c, filePath)
		require.Nil(t, err)
		t.Cleanup(func() {
			os.RemoveAll(filepath.Dir(filePath))
		})

		fs.Sleep(time.Hour + time.Minute)

		c = Must(&Options[int, int]{
			MaximumSize:       maximum + 1,
			ExpiryCalculator:  ExpiryWriting[int, int](expiresAfter - 1),
			RefreshCalculator: RefreshWriting[int, int](refreshableAfter + 1),
			Clock:             fs,
		})

		err = LoadCacheFromFile(c, filePath)
		require.Nil(t, err)
		require.Equal(t, 0, c.EstimatedSize())

		for i := 0; i < maximum; i++ {
			c.Set(i, -i)
		}

		err = SaveCacheToFile(c, filePath)
		require.Nil(t, err)

		sl := 20 * time.Minute
		fs.Sleep(sl)

		c.InvalidateAll()
		err = LoadCacheFromFile(c, filePath)
		require.Nil(t, err)

		for k, v := range c.All() {
			require.Equal(t, k, -v)
			require.Less(t, k, maximum)
			require.GreaterOrEqual(t, k, 0)

			entry, ok := c.GetEntryQuietly(k)
			require.True(t, ok)
			require.Equal(t, k, entry.Key)
			require.Equal(t, v, entry.Value)
			require.Equal(t, uint32(1), entry.Weight)
			require.Equal(t, expiresAfter-1-sl, entry.ExpiresAfter())
			require.Equal(t, refreshableAfter+1-sl, entry.RefreshableAfter())
		}
	})
}
