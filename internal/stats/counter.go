package stats

import (
	"sync"
	"sync/atomic"

	"github.com/maypok86/otter/internal/xmath"
	"github.com/maypok86/otter/internal/xruntime"
)

var tokenPool sync.Pool

type token struct {
	idx     uint32
	padding [xruntime.CacheLineSize - 4]byte
}

// much faster than atomic in write heavy scenarios (for example stats).
type counter struct {
	shards []cshard
	mask   uint32
}

type cshard struct {
	c       int64
	padding [xruntime.CacheLineSize - 8]byte
}

func newCounter() *counter {
	nshards := xmath.RoundUpPowerOf2(xruntime.Parallelism())
	return &counter{
		shards: make([]cshard, nshards),
		mask:   nshards - 1,
	}
}

func (c *counter) increment() {
	c.add(1)
}

func (c *counter) decrement() {
	c.add(-1)
}

func (c *counter) add(delta int64) {
	t, ok := tokenPool.Get().(*token)
	if !ok {
		t = &token{}
		t.idx = xruntime.Fastrand()
	}
	for {
		shard := &c.shards[t.idx&c.mask]
		cnt := atomic.LoadInt64(&shard.c)
		if atomic.CompareAndSwapInt64(&shard.c, cnt, cnt+delta) {
			break
		}
		t.idx = xruntime.Fastrand()
	}
	tokenPool.Put(t)
}

func (c *counter) value() int64 {
	v := int64(0)
	for i := 0; i < len(c.shards); i++ {
		shard := &c.shards[i]
		v += atomic.LoadInt64(&shard.c)
	}
	return v
}

func (c *counter) reset() {
	for i := 0; i < len(c.shards); i++ {
		shard := &c.shards[i]
		atomic.StoreInt64(&shard.c, 0)
	}
}
