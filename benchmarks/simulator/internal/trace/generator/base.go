package generator

import (
	"runtime"
	"sync"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type genFunc func(sender *sender[event.AccessEvent]) (stop bool)

type base struct {
	once     sync.Once
	stream   Stream[event.AccessEvent]
	generate genFunc
	limit    *uint
}

func newBase(generate genFunc, limit *uint) base {
	return base{
		stream:   newStream[event.AccessEvent](16 * runtime.GOMAXPROCS(0)),
		generate: generate,
		limit:    limit,
	}
}

func (b *base) Generate() Stream[event.AccessEvent] {
	b.once.Do(func() {
		go func() {
			sender := newSender(b.stream, b.limit)
			for {
				if stop := b.generate(sender); stop {
					b.stream.close()
					break
				}
			}
		}()
	})

	return b.stream
}
