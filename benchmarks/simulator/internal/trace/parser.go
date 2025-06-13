package trace

import (
	"errors"
	"io"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/parser"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/parser/libcachesim"
)

var ErrUnknownTraceFormat = errors.New("unknown trace format")

type parserContract interface {
	Parse(send func(event event.AccessEvent) bool) (bool, error)
}

func NewParser(traceType string, reader io.Reader) (parserContract, error) {
	switch traceType {
	case parser.ArcFormat:
		return parser.NewARC(reader), nil
	case parser.LirsFormat:
		return parser.NewLIRS(reader), nil
	case parser.OracleGeneralFormat:
		return parser.NewOracleGeneral(reader), nil
	case parser.LibcachesimCSVFormat:
		return libcachesim.NewCSV(reader), nil
	case parser.ScarabFormat:
		return parser.NewScarab(reader), nil
	case parser.CordaFormat:
		return parser.NewCorda(reader), nil
	default:
		return nil, ErrUnknownTraceFormat
	}
}
