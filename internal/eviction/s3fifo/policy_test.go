package s3fifo

import (
	"crypto/rand"
	"math"
	"math/big"
	"testing"

	"github.com/maypok86/otter/internal/node"
)

func newNode[K comparable, V any](key K, value V) *node.Node[K, V] {
	return node.New(key, value, 0)
}

func getRand(tb testing.TB) int64 {
	tb.Helper()

	out, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		tb.Fatal(err)
	}
	return out.Int64()
}

func TestPolicy_RandomOps(t *testing.T) {
	n := 200000
	m := make(map[int64]*node.Node[int64, int64])

	capacity := 128
	p := NewPolicy[int64, int64](capacity, func(n *node.Node[int64, int64]) {
		delete(m, n.Key())
	})

	for k := 0; k < 2; k++ {
		for i := 0; i < n; i++ {
			key := getRand(t) % 512
			r := getRand(t)
			switch r % 3 {
			case 0:
				_, ok := m[key]
				if !ok {
					n := newNode(key, key)
					p.Add(n)
					m[key] = n
				}
			case 1:
				n, ok := m[key]
				if ok {
					p.Get(n)
				}
			case 2:
				n, ok := m[key]
				if ok {
					p.Delete(n)
					delete(m, key)
				}
			}

			if p.Size() > p.Capacity() {
				t.Fatalf("too big policy: capacity: %d size: %d",
					p.Capacity(), p.Size())
			}
			if len(m) > p.Size()+p.ghost.length() {
				t.Fatalf("too big m: map len: %d policy size: %d ghost len: %d",
					len(m), p.Size(), p.ghost.length())
			}
		}

		p.Clear()
		m = make(map[int64]*node.Node[int64, int64])
	}
}
