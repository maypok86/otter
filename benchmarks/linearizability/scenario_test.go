package linearizability

import (
	"context"
	"testing"
	"time"

	"github.com/viccon/sturdyc"

	"github.com/maypok86/otter/v2"
)

// TestLoadRacingSet runs the race behind most of the violations that TestLinearizability finds:
// a Load misses the key and waits in its loader, Set(k, 5) completes meanwhile, and the loader
// returns -1. The Set completed before the Load returned, so a linearizable cache ends up with 5:
// the Load either takes effect first and its value is overwritten, or after the Set and returns
// 5. A Load that returns -1 and leaves -1 in the cache has lost the Set.
func TestLoadRacingSet(t *testing.T) {
	for _, lib := range libraries {
		if !canLoad(lib) {
			continue
		}
		t.Run(lib.name, func(t *testing.T) {
			c := lib.new()
			defer c.close()

			started, release := make(chan struct{}), make(chan struct{})
			loaded := make(chan int)
			go func() {
				v, _ := c.load("k", func() (int, error) {
					close(started)
					<-release
					return -1, nil
				})
				loaded <- v
			}()
			<-started

			setDone := make(chan struct{})
			go func() {
				c.set("k", 5)
				close(setDone)
			}()
			setWaited := false
			select {
			case <-setDone:
			case <-time.After(100 * time.Millisecond):
				setWaited = true
			}
			close(release)
			v := <-loaded
			<-setDone
			got, ok := read(c, "k")

			// The load takes effect either before the Set, returns -1 and is overwritten, or after
			// it and returns 5. Either way the key ends up with 5.
			linearizable := ok && got == 5 && (v == -1 || v == 5)
			if setWaited {
				t.Logf("Set waited for the load to finish")
			}
			switch {
			case !linearizable && v == -1 && got == -1:
				report(t, lib.name, "lost update: Set(k, 5) completed during the load, Load -> -1, then key = -1")
			case !linearizable:
				report(t, lib.name, "not linearizable: Load -> %d, then key = %d, %t", v, got, ok)
			case lib.name == "otter" && v != 5:
				// otter adopts a value written while it loads instead of overwriting it.
				report(t, lib.name, "Load -> %d, want 5", v)
			default:
				t.Logf("Load -> %d, then key = %d, %t", v, got, ok)
			}
		})
	}
}

// read returns the value of k, with a load that fails if the key is absent when the cache has no
// plain read.
func read(c *cache, k string) (int, bool) {
	if c.get != nil {
		return c.get(k)
	}
	v, err := c.load(k, func() (int, error) { return 0, errLoad })
	return v, err == nil
}

// refresher is a cache that refreshes entries. refresh sets k to 1 and starts a refresh of it
// whose loader is fn; fn returns found == false for a key that no longer exists. The returned
// function waits until the refresh has applied its result.
type refresher struct {
	name    string
	set     func(k string, v int)
	del     func(k string)
	get     func(k string) (int, bool)
	refresh func(k string, fn func() (v int, found bool)) (wait func())
}

func newOtterRefresher() refresher {
	c := otter.Must(&otter.Options[string, int]{
		MaximumSize:       capacity,
		RefreshCalculator: otter.RefreshCreating[string, int](100 * time.Hour),
	})
	return refresher{
		name: "otter",
		set:  func(k string, v int) { c.Set(k, v) },
		del:  func(k string) { c.Invalidate(k) },
		get:  c.GetIfPresent,
		refresh: func(k string, fn func() (int, bool)) func() {
			c.Set(k, 1)
			ch := c.Refresh(context.Background(), k, otter.LoaderFunc[string, int](func(context.Context, string) (int, error) {
				v, found := fn()
				if !found {
					return 0, otter.ErrNotFound
				}
				return v, nil
			}))
			return func() { <-ch }
		},
	}
}

func newSturdycRefresher() refresher {
	clock := sturdyc.NewTestClock(time.Now())
	c := sturdyc.New[int](capacity, 10, 100*time.Hour, 10,
		sturdyc.WithClock(clock),
		sturdyc.WithEarlyRefreshes(time.Hour, time.Hour, 50*time.Hour, time.Hour),
		sturdyc.WithNoContinuousEvictions(),
	)
	return refresher{
		name: "sturdyc",
		set:  func(k string, v int) { c.Set(k, v) },
		del:  c.Delete,
		get:  c.Get,
		refresh: func(k string, fn func() (int, bool)) func() {
			// sturdyc refreshes an entry in the background when GetOrFetch reads it after its
			// refresh time.
			c.Set(k, 1)
			clock.Add(2 * time.Hour)
			returned := make(chan struct{})
			_, err := c.GetOrFetch(context.Background(), k, func(context.Context) (int, error) {
				defer close(returned)
				v, found := fn()
				if !found {
					return 0, sturdyc.ErrNotFound
				}
				return v, nil
			})
			if err != nil {
				panic(err)
			}
			// The refresh applies its result on its own goroutine, and nothing reports when it is
			// done.
			return func() {
				<-returned
				time.Sleep(50 * time.Millisecond)
			}
		},
	}
}

// TestRefreshRacingWrite starts a refresh of k = 1 that waits in its loader, runs a write that
// completes before the loader returns, and checks what is left. The refresh read the key before
// the write, so its result is stale: a cache that applies it undoes a write that has completed.
func TestRefreshRacingWrite(t *testing.T) {
	tests := []struct {
		name      string
		write     func(r refresher)
		loaded    int
		found     bool
		wantValue int
		wantOK    bool
	}{
		{"Set during reload", func(r refresher) { r.set("k", 5) }, -2, true, 5, true},
		{"Delete during reload", func(r refresher) { r.del("k") }, -2, true, 0, false},
		{"Set during reload of a missing key", func(r refresher) { r.set("k", 5) }, 0, false, 5, true},
	}
	for _, newRefresher := range []func() refresher{newOtterRefresher, newSturdycRefresher} {
		for _, tt := range tests {
			r := newRefresher()
			t.Run(r.name+"/"+tt.name, func(t *testing.T) {
				started, release := make(chan struct{}), make(chan struct{})
				wait := r.refresh("k", func() (int, bool) {
					close(started)
					<-release
					return tt.loaded, tt.found
				})
				<-started
				tt.write(r)
				close(release)
				wait()

				v, ok := r.get("k")
				if v != tt.wantValue || ok != tt.wantOK {
					report(t, r.name, "the refresh undid the write: key = %d, %t; want %d, %t", v, ok, tt.wantValue, tt.wantOK)
					return
				}
				t.Logf("the write survived: key = %d, %t", v, ok)
			})
		}
	}
}
