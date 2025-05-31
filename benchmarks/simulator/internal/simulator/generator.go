package simulator

import (
	"errors"
	"fmt"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/config"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/trace/generator"
)

func newGenerator(cfg config.Config) (traceGenerator, error) {
	switch cfg.Type {
	case generator.ZipfType:
		return generator.NewZipf(cfg.Zipf.S, cfg.Zipf.V, cfg.Zipf.IMAX, cfg.Limit), nil
	case generator.FileType:
		traceGenerator, err := generator.NewFile(cfg.File.Paths[0], cfg.File.TraceType, cfg.Limit)
		if err != nil {
			return nil, fmt.Errorf("create trace generator from file: %w", err)
		}
		return traceGenerator, nil
	default:
		return nil, errors.New("unknown trace type")
	}
}
