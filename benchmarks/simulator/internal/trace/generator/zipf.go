package generator

import (
	"math/rand"
	"time"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type Zipf struct {
	base
}

func NewZipf(s, v float64, imax uint64, limit *uint) *Zipf {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	z := rand.NewZipf(r, s, v, imax)

	generate := func(sender *sender[event.AccessEvent]) (stop bool) {
		key := z.Uint64()
		return sender.Send(event.NewAccessEvent(key))
	}

	return &Zipf{
		base: newBase(generate, limit),
	}
}
