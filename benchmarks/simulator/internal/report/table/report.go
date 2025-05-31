package table

import (
	"fmt"
	"os"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report/simulation"
)

type Table struct {
	table [][]simulation.Result
}

func NewTable(table [][]simulation.Result) *Table {
	return &Table{
		table: table,
	}
}

func (t *Table) Report() error {
	if t == nil {
		return nil
	}

	capacities := make([]string, 0, len(t.table[0]))
	for _, r := range t.table[0] {
		capacities = append(capacities, strconv.FormatInt(int64(r.Capacity()), 10))
	}

	w := tablewriter.NewWriter(os.Stdout).Options(tablewriter.WithRendition(tw.Rendition{
		Borders: tw.Border{
			Left:   tw.On,
			Top:    tw.Off,
			Right:  tw.On,
			Bottom: tw.Off,
		},
	}), tablewriter.WithHeader(append([]string{"Cache"}, capacities...)))
	for _, results := range t.table {
		processed := make([]any, 0, len(capacities)+1)
		processed = append(processed, results[0].Name())
		for _, r := range results {
			processed = append(processed, fmt.Sprintf("%0.2f", r.Ratio()))
		}
		if err := w.Append(processed...); err != nil {
			return err
		}
	}
	return w.Render()
}
