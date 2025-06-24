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

	"github.com/stretchr/testify/require"
)

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
