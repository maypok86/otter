package generator

import (
	"math/rand"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type Zipf struct {
	base
}

func NewZipf(s, v float64, imax uint64, limit *uint) *Zipf {
	// A fixed seed gives every policy the same trace, so their hit ratios
	// can be compared, and makes the results reproducible.
	r := rand.New(rand.NewSource(1))
	z := rand.NewZipf(r, s, v, imax)

	generate := func(sender *sender[event.AccessEvent]) (stop bool) {
		key := z.Uint64()
		return sender.Send(event.NewAccessEvent(key))
	}

	return &Zipf{
		base: newBase(generate, limit),
	}
}
