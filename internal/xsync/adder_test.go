// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
// Copyright (c) 2021 Andrey Pechkurov. All rights reserved.
//
// Copyright notice. This code is a fork of tests for xsync.Counter from this file with some changes:
// https://github.com/puzpuzpuz/xsync/blob/main/counter_test.go
//
// Use of this source code is governed by a MIT license that can be found
// at https://github.com/puzpuzpuz/xsync/blob/main/LICENSE

package xsync

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func TestAdderAdd(t *testing.T) {
	t.Parallel()

	a := NewAdder()
	for i := 0; i < 100; i++ {
		if v := a.Value(); v != uint64(i*42) {
			t.Fatalf("got %v, want %d", v, i*42)
		}
		a.Add(42)
	}
}

func parallelIncrement(a *Adder, incs int, wg *sync.WaitGroup) {
	for i := 0; i < incs; i++ {
		a.Add(1)
	}
	wg.Done()
}

func testParallelIncrement(t *testing.T, modifiers, gomaxprocs int) {
	t.Helper()
	runtime.GOMAXPROCS(gomaxprocs)
	a := NewAdder()
	wg := &sync.WaitGroup{}
	incs := 10_000
	wg.Add(modifiers)
	for i := 0; i < modifiers; i++ {
		go parallelIncrement(a, incs, wg)
	}
	wg.Wait()
	expected := uint64(modifiers * incs)
	if v := a.Value(); v != expected {
		t.Fatalf("got %d, want %d", v, expected)
	}
}

func TestAdderParallelIncrementors(t *testing.T) {
	t.Parallel()

	t.Cleanup(func() {
		runtime.GOMAXPROCS(runtime.GOMAXPROCS(-1))
	})

	tests := []struct {
		modifiers  int
		gomaxprocs int
	}{
		{
			modifiers:  4,
			gomaxprocs: 2,
		},
		{
			modifiers:  16,
			gomaxprocs: 4,
		},
		{
			modifiers:  64,
			gomaxprocs: 8,
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("parallelIncrement-%d", i+1), func(t *testing.T) {
			t.Parallel()

			testParallelIncrement(t, tt.modifiers, tt.gomaxprocs)
			testParallelIncrement(t, tt.modifiers, tt.gomaxprocs)
			testParallelIncrement(t, tt.modifiers, tt.gomaxprocs)
		})
	}
}
