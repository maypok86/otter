package policy

import (
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/policy/product"
)

type Policy struct {
	policy product.Policy[uint64, uint64]
	hits   uint64
	misses uint64
}

func NewPolicy(c product.Policy[uint64, uint64]) *Policy {
	return &Policy{
		policy: c,
	}
}

func (p *Policy) Record(e event.AccessEvent) {
	key := e.Key()
	value, ok := p.policy.Get(key)
	if ok {
		if key != value {
			panic("not valid value")
		}
		p.hits++
	} else {
		p.policy.Set(key, key)
		p.misses++
	}
}

func (p *Policy) Name() string {
	return p.policy.Name()
}

func (p *Policy) Init(capacity int) {
	p.policy.Init(capacity)
}

func (p *Policy) Ratio() float64 {
	return 100 * (float64(p.hits) / float64(p.hits+p.misses))
}

func (p *Policy) Close() {
	p.policy.Close()
}
