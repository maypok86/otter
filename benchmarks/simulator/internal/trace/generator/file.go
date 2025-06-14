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

type FilePath struct {
	TraceType string
	Path      string
}

func NewFile(paths []FilePath, limit *uint) (*File, error) {
	generate := func(sender *sender[event.AccessEvent]) (stop bool) {
		for _, p := range paths {
			reader, err := trace.NewReader(p.Path)
			if err != nil {
				log.Println(fmt.Errorf("create file reader: %w", err))
				return true
			}

			parser, err := trace.NewParser(p.TraceType, reader)
			if err != nil {
				log.Println(fmt.Errorf("create file reader: %w", err))
				return true
			}

			for {
				done, err := parser.Parse(sender.Send)
				if err != nil {
					log.Println(err)
					return true
				}
				if done {
					break
				}
			}
		}

		return true
	}

	return &File{
		base: newBase(generate, limit),
	}, nil
}
