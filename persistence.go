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
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"
)

// LoadCacheFromFile loads cache data from the given filePath.
//
// See SaveCacheToFile for saving cache data to file.
func LoadCacheFromFile[K comparable, V any](c *Cache[K, V], filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("otter: open file %s: %w", filePath, err)
	}
	//nolint:errcheck // it's ok
	defer file.Close()

	return LoadCacheFrom(c, file)
}

// LoadCacheFrom loads cache data from the given [io.Reader].
//
// See SaveCacheToFile for saving cache data to file.
//
// The data is decoded with [encoding/gob], so the keys and values have the limits of gob: unexported
// struct fields are not restored. The saved expiration and refresh times are absolute, so both
// caches' clocks must count from the same point, as the default clock does (Unix time).
func LoadCacheFrom[K comparable, V any](c *Cache[K, V], r io.Reader) error {
	dec := gob.NewDecoder(r)

	var savedMaximum uint64
	if err := dec.Decode(&savedMaximum); err != nil {
		return fmt.Errorf("otter: decode maximum: %w", err)
	}

	maximum := min(savedMaximum, c.GetMaximum())
	maximum2 := maximum / 4
	maximum1 := 2 * maximum2
	size := uint64(0)
	for size < maximum {
		var entry Entry[K, V]
		if err := dec.Decode(&entry); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("otter: decode entry: %w", err)
		}

		nowNano := c.cache.clock.NowNano()
		if c.cache.withExpiration && entry.ExpiresAtNano <= nowNano {
			continue
		}
		c.Set(entry.Key, entry.Value)
		if c.cache.withExpiration && entry.ExpiresAtNano != unreachableExpiresAt {
			expiresAfter := max(1, time.Duration(entry.ExpiresAtNano-nowNano))
			c.SetExpiresAfter(entry.Key, expiresAfter)
		}
		if c.cache.withRefresh && entry.RefreshableAtNano != unreachableRefreshableAt {
			refreshableAfter := max(1, time.Duration(entry.RefreshableAtNano-nowNano))
			c.SetRefreshableAfter(entry.Key, refreshableAfter)
		}
		size += uint64(entry.Weight)

		if size <= maximum2 {
			warmUp(c, entry.Key, 2)
			continue
		}
		if size <= maximum1 {
			warmUp(c, entry.Key, 1)
			continue
		}
	}

	return nil
}

// warmUp credits the restored entry of key to the eviction policy as reads would, so that the
// hottest entries of the snapshot are not the first ones evicted. These are not the caller's
// reads: they record no hits, and do not call ExpireAfterRead, which would replace the lifetime
// just restored.
func warmUp[K comparable, V any](c *Cache[K, V], key K, reads int) {
	nowNano := c.cache.clock.NowNano()
	n := c.cache.getNodeQuietly(key, nowNano)
	if n == nil {
		return
	}
	for range reads {
		c.cache.afterRead(n, nowNano, false, false)
	}
}

// SaveCacheToFile atomically saves cache data to the given filePath: the data is written to a
// temporary file in the same directory, which then replaces the file, so that a failed or
// interrupted save leaves the previous file as it was.
//
// SaveCacheToFile may be called concurrently with other operations on the cache.
//
// The saved data may be loaded with LoadCacheFromFile.
//
// WARNING: Beware that this operation is performed within the eviction policy's exclusive lock.
// While the operation is in progress further eviction maintenance will be halted.
func SaveCacheToFile[K comparable, V any](c *Cache[K, V], filePath string) error {
	// The file a link points to is replaced, not the link, as when the file was written in place.
	if resolved, err := filepath.EvalSymlinks(filePath); err == nil {
		filePath = resolved
	}

	// Create dir if it doesn't exist.
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("otter: stat %s: %w", dir, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("otter: create dir %s: %w", dir, err)
		}
	}

	// opened with the permissions os.Create would give the file
	tmpPath := fmt.Sprintf("%s.%d.tmp", filePath, rand.Uint64())
	file, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return fmt.Errorf("otter: create file %s: %w", tmpPath, err)
	}
	if err := writeCacheFile(c, file, filePath); err != nil {
		//nolint:errcheck // the save has failed already
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		//nolint:errcheck // the save has failed already
		_ = os.Remove(tmpPath)
		return fmt.Errorf("otter: rename %s to %s: %w", tmpPath, filePath, err)
	}
	return nil
}

// writeCacheFile saves the data of c to file, which is to replace filePath, flushes it to disk and
// closes file. An existing filePath keeps its permissions, as when it was written in place: a file
// closed to other users must stay closed. Its owner is not kept.
func writeCacheFile[K comparable, V any](c *Cache[K, V], file *os.File, filePath string) error {
	var err error
	if info, statErr := os.Stat(filePath); statErr == nil {
		if err = file.Chmod(info.Mode().Perm()); err != nil {
			err = fmt.Errorf("otter: chmod file %s: %w", file.Name(), err)
		}
	}
	if err == nil {
		err = SaveCacheTo(c, file)
	}
	if err == nil {
		if err = file.Sync(); err != nil {
			err = fmt.Errorf("otter: sync file %s: %w", file.Name(), err)
		}
	}
	if closeErr := file.Close(); closeErr != nil && err == nil {
		err = fmt.Errorf("otter: close file %s: %w", file.Name(), closeErr)
	}
	return err
}

// SaveCacheTo saves cache data to the given [io.Writer].
//
// SaveCacheTo may be called concurrently with other operations on the cache. The data is not a
// point-in-time snapshot: an entry written or removed while it runs may or may not be saved, and
// one updated may be saved with either value.
//
// The saved data may be loaded with LoadCacheFrom.
//
// The data is encoded with [encoding/gob], so the keys and values have the limits of gob: unexported
// struct fields are not saved.
//
// WARNING: Beware that this operation is performed within the eviction policy's exclusive lock.
// While the operation is in progress further eviction maintenance will be halted, and w has the
// restrictions of the loop body of [Cache.Hottest].
func SaveCacheTo[K comparable, V any](c *Cache[K, V], w io.Writer) error {
	enc := gob.NewEncoder(w)

	maximum := c.GetMaximum()
	if err := enc.Encode(maximum); err != nil {
		return fmt.Errorf("otter: encode maximum: %w", err)
	}

	size := uint64(0)
	for entry := range c.Hottest() {
		if size >= maximum {
			break
		}

		if err := enc.Encode(entry); err != nil {
			return fmt.Errorf("otter: encode entry: %w", err)
		}

		size += uint64(entry.Weight)
	}

	return nil
}
