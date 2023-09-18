package stats

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func TestCounterIncrement(t *testing.T) {
	c := newCounter()
	for i := 0; i < 1000; i++ {
		if v := c.value(); v != int64(i) {
			t.Fatalf("got %v, want %d", v, i)
		}
		c.increment()
	}
}

func TestCounterDecrement(t *testing.T) {
	c := newCounter()
	for i := 0; i < 1000; i++ {
		if v := c.value(); v != int64(-i) {
			t.Fatalf("got %v, want %d", v, -i)
		}
		c.decrement()
	}
}

func TestCounterAdd(t *testing.T) {
	c := newCounter()
	for i := 0; i < 100; i++ {
		if v := c.value(); v != int64(i*42) {
			t.Fatalf("got %v, want %d", v, i*42)
		}
		c.add(42)
	}
}

func TestCounterReset(t *testing.T) {
	c := newCounter()
	c.add(42)
	if v := c.value(); v != 42 {
		t.Fatalf("got %v, want %d", v, 42)
	}
	c.reset()
	if v := c.value(); v != 0 {
		t.Fatalf("got %v, want %d", v, 0)
	}
}

func parallelIncrement(c *counter, incs int, wg *sync.WaitGroup) {
	for i := 0; i < incs; i++ {
		c.increment()
	}
	wg.Done()
}

func testParallelIncrement(t *testing.T, modifiers, gomaxprocs int) {
	t.Helper()
	runtime.GOMAXPROCS(gomaxprocs)
	c := newCounter()
	wg := &sync.WaitGroup{}
	incs := 10_000
	wg.Add(modifiers)
	for i := 0; i < modifiers; i++ {
		go parallelIncrement(c, incs, wg)
	}
	wg.Wait()
	expected := int64(modifiers * incs)
	if v := c.value(); v != expected {
		t.Fatalf("got %d, want %d", v, expected)
	}
}

func TestCounterParallelIncrementors(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(-1))

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
			testParallelIncrement(t, tt.modifiers, tt.gomaxprocs)
			testParallelIncrement(t, tt.modifiers, tt.gomaxprocs)
			testParallelIncrement(t, tt.modifiers, tt.gomaxprocs)
		})
	}
}
