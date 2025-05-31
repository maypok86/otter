package simulator

import (
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/trace/generator"
)

type traceGenerator interface {
	Generate() generator.Stream[event.AccessEvent]
}

type policyContract interface {
	Record(event event.AccessEvent)
	Name() string
	Init(capacity int)
	Ratio() float64
	Close()
}
