package generator

import (
	"fmt"
	"log"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/trace"
)

type File struct {
	base
}

func NewFile(path, traceType string, limit *uint) (*File, error) {
	reader, err := trace.NewReader(path)
	if err != nil {
		return nil, fmt.Errorf("create file reader: %w", err)
	}

	parser, err := trace.NewParser(traceType, reader)
	if err != nil {
		return nil, fmt.Errorf("create trace parser: %w", err)
	}

	generate := func(sender *sender[event.AccessEvent]) (stop bool) {
		done, err := parser.Parse(sender.Send)
		if err != nil {
			log.Println(err)
		}

		return done || err != nil
	}

	return &File{
		base: newBase(generate, limit),
	}, nil
}
