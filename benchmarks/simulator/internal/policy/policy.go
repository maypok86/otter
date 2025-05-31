package policy

import (
	"github.com/maypok86/otter/v2/benchmarks/client"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type Policy struct {
	client client.Client[uint64, uint64]
	hits   uint64
	misses uint64
}

func NewPolicy(c client.Client[uint64, uint64]) *Policy {
	return &Policy{
		client: c,
	}
}

func (p *Policy) Record(e event.AccessEvent) {
	key := e.Key()
	value, ok := p.client.Get(key)
	if ok {
		if key != value {
			panic("not valid value")
		}
		p.hits++
	} else {
		p.client.Set(key, key)
		p.misses++
	}
}

func (p *Policy) Name() string {
	return p.client.Name()
}

func (p *Policy) Init(capacity int) {
	p.client.Init(capacity)
}

func (p *Policy) Ratio() float64 {
	return 100 * (float64(p.hits) / float64(p.hits+p.misses))
}

func (p *Policy) Close() {
	p.client.Close()
}
