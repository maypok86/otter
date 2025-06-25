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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type nonTickingClock struct {
	now atomic.Int64
}

func newNonTickingClock() *nonTickingClock {
	return &nonTickingClock{}
}

func (c *nonTickingClock) NowNano() int64 {
	return c.now.Load()
}

func (c *nonTickingClock) Tick(duration time.Duration) <-chan time.Time {
	return nil
}

func (c *nonTickingClock) Sleep(duration time.Duration) {
	c.now.Add(int64(duration))
}

func TestRealSource(t *testing.T) {
	t.Parallel()

	c := newTimeSource(&realSource{})
	start := time.Now().UnixNano()
	c.Init()

	got := (c.NowNano() - start) / 1e9
	if got != 0 {
		t.Fatalf("unexpected time since program start; got %d; want %d", got, 0)
	}

	c.Sleep(3 * time.Second)

	got = (c.NowNano() - start) / 1e9
	if got != 3 {
		t.Fatalf("unexpected time since program start; got %d; want %d", got, 3)
	}
}

func TestFakeSource(t *testing.T) {
	t.Parallel()

	fs := &fakeSource{}
	fs.Init()

	fs.Sleep(3 * time.Minute)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch := fs.Tick(time.Second)
		<-ch
		fs.ProcessTick()
		tick := <-ch
		fs.ProcessTick()
		require.Equal(t, fs.getNow().Add(-3*time.Minute).Add(time.Second), tick)
	}()
	wg.Wait()
}

type testClock struct{}

func newTestClock() *testClock {
	return &testClock{}
}

func (tc *testClock) NowNano() int64 {
	return 1
}

func (tc *testClock) Tick(duration time.Duration) <-chan time.Time {
	ticker := make(chan time.Time, 1)
	ticker <- time.Now()
	return ticker
}

func TestCustomSource(t *testing.T) {
	c := newTimeSource(newTestClock())

	require.Equal(t, int64(0), c.NowNano())
	c.Init()
	require.Equal(t, int64(1), c.NowNano())
	require.Equal(t, 1, len(c.Tick(time.Second)))
	c.Sleep(20 * time.Millisecond)
	c.ProcessTick()
}
