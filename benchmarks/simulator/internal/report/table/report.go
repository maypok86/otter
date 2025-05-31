package table

import (
	"fmt"
	"os"
	"strconv"

	"github.com/olekukonko/tablewriter"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report/simulation"
)

func formatInt(n int64) string {
	in := strconv.FormatInt(n, 10)
	numOfDigits := len(in)
	if n < 0 {
		numOfDigits-- // First character is the - sign (not a digit)
	}
	numOfCommas := (numOfDigits - 1) / 3

	out := make([]byte, len(in)+numOfCommas)
	if n < 0 {
		in, out[0] = in[1:], '-'
	}

	for i, j, k := len(in)-1, len(out)-1, 0; ; i, j = i-1, j-1 {
		out[j] = in[i]
		if i == 0 {
			return string(out)
		}
		k++
		if k == 3 {
			j, k = j-1, 0
			out[j] = ','
		}
	}
}

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
		capacities = append(capacities, formatInt(int64(r.Capacity())))
	}

	tw := tablewriter.NewWriter(os.Stdout)
	tw.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
	tw.SetCenterSeparator("|")
	tw.SetHeader(append([]string{"Cache"}, capacities...))
	for _, results := range t.table {
		processed := make([]string, 0, len(capacities)+1)
		processed = append(processed, results[0].Name())
		for _, r := range results {
			processed = append(processed, fmt.Sprintf("%0.2f", r.Ratio()))
		}
		tw.Append(processed)
	}
	tw.Render()
	return nil
}
