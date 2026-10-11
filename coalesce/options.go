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

package coalesce

import (
	"errors"
	"time"

	"github.com/maypok86/otter/v2"
	"github.com/maypok86/otter/v2/internal/xlog"
)

// Options configures a [Loader].
type Options struct {
	// MaxBatchSize limits the number of keys in one call of BulkLoad. Zero means no limit.
	MaxBatchSize int
	// MaxInFlight limits the number of BulkLoad calls that run at once. Zero means one, and a
	// negative value means no limit.
	//
	// The keys that arrive while every call is busy wait and leave together in the next batch,
	// so without MaxDelay this limit is what makes batches grow: with one call at a time, a key
	// waits for at most the batch in flight and its own. With no limit, every key leaves in a
	// batch of its own.
	MaxInFlight int
	// MaxDelay is how long a batch waits for more keys after its first key comes, unless it
	// reaches MaxBatchSize first. Zero means no wait: a batch leaves as soon as a call is free.
	//
	// The wait adds up to MaxDelay to the time of a load. Use it when the keys come too spread
	// out for MaxInFlight alone to batch them, typically with no limit on MaxInFlight.
	MaxDelay time.Duration
	// Logger reports a panic of BulkLoad that no call of Load waits for any more, so that it is
	// not lost. By default, slog.Default() is used.
	Logger otter.Logger
}

func (o *Options) validate() error {
	if o.MaxBatchSize < 0 {
		return errors.New("coalesce: maxBatchSize should not be negative")
	}
	if o.MaxDelay < 0 {
		return errors.New("coalesce: maxDelay should not be negative")
	}
	return nil
}

func (o *Options) getLogger() otter.Logger {
	if o.Logger == nil {
		return xlog.Default()
	}
	return o.Logger
}

func (o *Options) getMaxInFlight() int {
	if o.MaxInFlight == 0 {
		return 1
	}
	if o.MaxInFlight < 0 {
		return -1
	}
	return o.MaxInFlight
}
