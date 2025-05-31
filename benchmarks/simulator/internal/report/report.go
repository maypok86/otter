package report

import (
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report/chart"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report/simulation"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report/table"
)

type reporter interface {
	Report() error
}

type Reporter struct {
	reporters []reporter
}

func NewReporter(name string, t [][]simulation.Result) *Reporter {
	return &Reporter{
		reporters: []reporter{
			table.NewTable(t),
			chart.NewChart(name, t),
		},
	}
}

func (r *Reporter) Report() error {
	if r == nil {
		return nil
	}

	for _, rep := range r.reporters {
		if err := rep.Report(); err != nil {
			return err
		}
	}
	return nil
}
