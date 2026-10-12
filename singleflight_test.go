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
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

// testGroup is a group whose commit and publish are recorded and can be replaced.
type testGroup struct {
	*group[int, int]

	mu        sync.Mutex
	committed []*call[int, int]
	published []mutation[int, int]
	// onCommit runs in commit, under what would be the key's bucket lock.
	onCommit func(cl *call[int, int])
}

func newTestGroup() *testGroup {
	tg := &testGroup{group: &group[int, int]{}}
	tg.commit = func(cl *call[int, int]) mutation[int, int] {
		tg.mu.Lock()
		tg.committed = append(tg.committed, cl)
		onCommit := tg.onCommit
		tg.mu.Unlock()
		if onCommit != nil {
			onCommit(cl)
		}
		return mutation[int, int]{op: opWrite}
	}
	tg.publish = func(m mutation[int, int]) {
		tg.mu.Lock()
		defer tg.mu.Unlock()
		tg.published = append(tg.published, m)
	}
	tg.executor = func(fn func()) { go fn() }
	tg.init()
	return tg
}

// A finished call is committed while it is still registered, and published only after it is
// deleted and its waiters are woken, so that a new load of the key sees the committed value.
func TestGroup_FinishOrder(t *testing.T) {
	t.Parallel()

	g := newTestGroup()
	cl, shouldLoad := g.startLoad(1)
	require.True(t, shouldLoad)
	g.onCommit = func(c *call[int, int]) {
		require.Same(t, c, g.getCall(1), "committed after the call was deleted")
	}
	g.publish = func(mutation[int, int]) {
		require.Nil(t, g.getCall(1), "published before the call was deleted")
		cl.wait() // woken before publishing: this would block otherwise
	}

	err := g.doCall(context.Background(), cl, func(context.Context, int) (int, error) { return 2, nil })
	require.NoError(t, err)
	require.Equal(t, 2, cl.value)
	require.Len(t, g.committed, 1)
}

// A commit that panics (the weigher or a calculator) still deletes the call and wakes its
// waiters, with the panic as the error, and nothing is published.
func TestGroup_FinishWhenCommitPanics(t *testing.T) {
	t.Parallel()

	g := newTestGroup()
	cl, _ := g.startLoad(1)
	g.onCommit = func(*call[int, int]) { panic("weigher") }

	require.Panics(t, func() {
		_ = g.doCall(context.Background(), cl, func(context.Context, int) (int, error) { return 2, nil })
	})
	require.Nil(t, g.getCall(1))
	cl.wait()
	var pe *panicError
	require.ErrorAs(t, cl.err, &pe)
	require.Empty(t, g.published)
}

// A commit that calls runtime.Goexit still deletes the call and wakes its waiters.
func TestGroup_FinishWhenCommitExits(t *testing.T) {
	t.Parallel()

	g := newTestGroup()
	cl, _ := g.startLoad(1)
	g.onCommit = func(*call[int, int]) { runtime.Goexit() }

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = g.doCall(context.Background(), cl, func(context.Context, int) (int, error) { return 2, nil })
	}()
	<-done
	require.Nil(t, g.getCall(1))
	cl.wait()
	require.Empty(t, g.published)
}

// A loader that panics or calls runtime.Goexit finishes its call with an error, which is
// committed like any other.
func TestGroup_LoaderFailures(t *testing.T) {
	t.Parallel()

	t.Run("panic", func(t *testing.T) {
		t.Parallel()

		g := newTestGroup()
		cl, _ := g.startLoad(1)
		err := g.doCall(context.Background(), cl, func(context.Context, int) (int, error) { panic("loader") })
		var pe *panicError
		require.ErrorAs(t, err, &pe)
		require.ErrorAs(t, cl.err, &pe)
		require.Len(t, g.committed, 1)
		require.Nil(t, g.getCall(1))
	})
	t.Run("Goexit", func(t *testing.T) {
		t.Parallel()

		g := newTestGroup()
		cl, _ := g.startLoad(1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = g.doCall(context.Background(), cl, func(context.Context, int) (int, error) {
				runtime.Goexit()
				return 0, nil
			})
		}()
		<-done
		require.ErrorIs(t, cl.err, errLoaderExited)
		require.Len(t, g.committed, 1)
		require.Nil(t, g.getCall(1))
	})
}

// A key the bulk loader leaves out is not found. An ErrNotFound for the whole bulk is an error
// of every key, not a "not found" that would remove them. A key the loader adds is committed as
// a fake call, which is never registered.
func TestGroup_BulkResults(t *testing.T) {
	t.Parallel()

	t.Run("omitted and extra keys", func(t *testing.T) {
		t.Parallel()

		g := newTestGroup()
		c1, _ := g.startLoad(1)
		c2, _ := g.startLoad(2)
		calls := map[int]*call[int, int]{1: c1, 2: c2}
		err := g.doBulkCall(context.Background(), calls, func(context.Context, []int) (map[int]int, error) {
			return map[int]int{1: 10, 3: 30}, nil
		})
		require.NoError(t, err)
		require.Equal(t, 10, c1.value)
		require.ErrorIs(t, c2.err, ErrNotFound)
		require.True(t, c2.isNotFound)
		require.True(t, calls[3].isFake)
		require.Len(t, g.committed, 3)
		require.Nil(t, g.getCall(1))
		require.Nil(t, g.getCall(2))
	})
	t.Run("ErrNotFound for the whole bulk", func(t *testing.T) {
		t.Parallel()

		g := newTestGroup()
		c1, _ := g.startLoad(1)
		err := g.doBulkCall(context.Background(), map[int]*call[int, int]{1: c1}, func(context.Context, []int) (map[int]int, error) {
			return nil, ErrNotFound
		})
		require.ErrorIs(t, err, ErrNotFound)
		require.ErrorIs(t, c1.err, ErrNotFound)
		require.False(t, c1.isNotFound)
	})
}

// If the executor panics while submitting a refresh, the refresh's calls are finished with the
// panic as their error, and a task that the executor scheduled before panicking does nothing.
func TestGroup_SubmitRefreshWhenExecutorPanics(t *testing.T) {
	t.Parallel()

	for _, scheduled := range []bool{false, true} {
		g := newTestGroup()
		var task func()
		g.executor = func(fn func()) {
			if scheduled {
				task = fn
			}
			panic("executor")
		}
		cl, shouldLoad := g.startRefresh(1, nil)
		require.True(t, shouldLoad)
		ran := false

		require.Panics(t, func() {
			g.submitRefresh([]*call[int, int]{cl}, func() { ran = true })
		})
		require.Nil(t, g.getCall(1))
		cl.wait()
		var pe *panicError
		require.ErrorAs(t, cl.err, &pe)
		if task != nil {
			task()
		}
		require.False(t, ran, "the abandoned task ran")
	}
}

// adopt finishes a registered call with a value the cache already holds.
func TestGroup_Adopt(t *testing.T) {
	t.Parallel()

	g := newTestGroup()
	cl, _ := g.startLoad(1)
	joined, shouldLoad := g.startLoad(1)
	require.False(t, shouldLoad)
	require.Same(t, cl, joined)

	g.adopt(cl, 5, 0, nil)
	joined.wait()
	require.Equal(t, 5, joined.value)
	require.NoError(t, joined.err)
	require.Nil(t, g.getCall(1))
	require.Empty(t, g.committed)
}

// A refresh records the node it was requested for.
func TestGroup_StartRefreshRecordsBase(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[int, int](node.Config{WithExpiration: true})
	base := nm.Create(1, 7, unreachableExpiresAt, 0, 1)
	g := newTestGroup()
	cl, _ := g.startRefresh(1, base)
	require.Same(t, base, cl.base)
	require.True(t, cl.isRefresh)
}

func TestGroup_Authoritative(t *testing.T) {
	t.Parallel()

	const now = int64(100)
	nm := node.NewManager[int, int](node.Config{WithExpiration: true})
	live := nm.Create(1, 1, now+1, 0, 1)
	expired := nm.Create(1, 1, now, 0, 1)
	other := nm.Create(1, 2, now+1, 0, 1)
	prevOf := func(n node.Node[int, int]) previous[int, int] {
		if n == nil {
			return previous[int, int]{}
		}
		return previous[int, int]{node: n, value: n.Value(), live: !n.HasExpired(now)}
	}

	tests := []struct {
		name string
		// setup registers the call and returns it, and the key's node when it finishes
		setup func(g *testGroup) (*call[int, int], node.Node[int, int])
		want  bool
	}{
		{"load over an absent key", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startLoad(1)
			return cl, nil
		}, true},
		{"load cancelled by a write", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startLoad(1)
			g.delete(1)
			return cl, other
		}, false},
		{"refresh of an unchanged node", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startRefresh(1, live)
			return cl, live
		}, true},
		{"refresh of a replaced node", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startRefresh(1, live)
			return cl, other
		}, false},
		{"refresh of a removed node", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startRefresh(1, live)
			return cl, nil
		}, false},
		{"refresh of a node that expired in flight", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startRefresh(1, expired)
			return cl, expired
		}, false},
		{"refresh of an absent key over an expired node", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startRefresh(1, nil)
			return cl, expired
		}, true},
		{"refresh of an absent key over a live node", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			cl, _ := g.startRefresh(1, nil)
			return cl, live
		}, false},
		{"fake call over an absent key", func(*testGroup) (*call[int, int], node.Node[int, int]) {
			return &call[int, int]{key: 1, isFake: true}, nil
		}, true},
		{"fake call over a live node", func(*testGroup) (*call[int, int], node.Node[int, int]) {
			return &call[int, int]{key: 1, isFake: true}, live
		}, false},
		{"fake call while the key has its own load", func(g *testGroup) (*call[int, int], node.Node[int, int]) {
			g.startLoad(1)
			return &call[int, int]{key: 1, isFake: true}, nil
		}, false},
	}
	for _, tt := range tests {
		g := newTestGroup()
		cl, current := tt.setup(g)
		require.Equal(t, tt.want, g.authoritative(cl, prevOf(current), now), tt.name)
	}
}
