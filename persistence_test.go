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
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/stats"
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
		// Keep the first cache reachable until Sleep returns. Otherwise the GC may
		// collect it mid-Sleep, stop its cleanup goroutine, and the fake clock
		// would block forever trying to deliver ticks nobody reads.
		runtime.KeepAlive(c)

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

func TestSaveCacheTo_KeysReplacedDuringIt(t *testing.T) {
	t.Parallel()

	// SaveCacheTo may run concurrently with writes: a key replaced while the cache is saved is
	// saved too. The first update of a string replaces its node.
	const keys = 100
	for _, opts := range []*Options[int, string]{
		{ExpiryCalculator: ExpiryWriting[int, string](time.Hour)},
		{MaximumSize: 1000, ExpiryCalculator: ExpiryWriting[int, string](time.Hour)},
	} {
		// several rounds, see TestCache_IterationVisitsKeysReplacedDuringIt
		for round := range 10 {
			c := Must(opts)
			for k := range keys {
				c.Set(k, "a")
			}
			w := &updatingWriter{update: func() {
				for k := range keys {
					c.Set(k, "b")
				}
			}}
			require.NoError(t, SaveCacheTo(c, w))

			loaded := Must(&Options[int, string]{})
			require.NoError(t, LoadCacheFrom(loaded, &w.buf))
			require.Equal(t, keys, loaded.EstimatedSize(), "MaximumSize=%d, round %d", opts.MaximumSize, round)
		}
	}
}

// updatingWriter calls update before its second write: the first one is the maximum, which
// SaveCacheTo writes before it iterates over the cache.
type updatingWriter struct {
	buf    bytes.Buffer
	update func()
	writes int
}

func (w *updatingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		w.update()
	}
	return w.buf.Write(p)
}

// LoadCacheFrom credits the restored entries to the eviction policy as reads, but they are not
// reads of the caller: they record no hits, and under ExpiryAccessing they keep the remaining
// lifetime the snapshot restored instead of getting a full one.
func TestLoadCacheFrom_WarmUpIsNotARead(t *testing.T) {
	t.Parallel()

	const keys = 10
	for _, maximumSize := range []int{0, 100} {
		newCache := func(clk *nonTickingClock, recorder stats.Recorder) *Cache[int, int] {
			return Must(&Options[int, int]{
				MaximumSize:      maximumSize,
				ExpiryCalculator: ExpiryAccessing[int, int](time.Hour),
				Clock:            clk,
				StatsRecorder:    recorder,
			})
		}
		clk := newNonTickingClock()
		saved := newCache(clk, nil)
		for k := range keys {
			saved.Set(k, k)
		}
		clk.Sleep(30 * time.Minute)
		var buf bytes.Buffer
		require.NoError(t, SaveCacheTo(saved, &buf))

		counter := stats.NewCounter()
		loaded := newCache(clk, counter)
		require.NoError(t, LoadCacheFrom(loaded, &buf))

		require.Zero(t, loaded.Stats().Hits, "MaximumSize=%d", maximumSize)
		for k := range keys {
			entry, ok := loaded.GetEntryQuietly(k)
			require.True(t, ok)
			require.Equal(t, 30*time.Minute, entry.ExpiresAfter(), "MaximumSize=%d, key %d", maximumSize, k)
		}
	}
}
