package libcachesim

import (
	"bufio"
	"io"
	"strconv"
	"strings"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/parser"
)

type CSV struct {
	scanner *bufio.Scanner
	i       int
}

func NewCSV(reader io.Reader) *CSV {
	return &CSV{
		scanner: bufio.NewScanner(reader),
	}
}

func (c *CSV) Parse(send func(event event.AccessEvent) bool) (bool, error) {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return false, parser.WrapError(err)
		}

		return true, nil
	}

	c.i++
	if c.i == 1 {
		return false, nil
	}
	line := c.scanner.Text()
	fields := strings.Split(line, ",")
	if len(fields) != 4 {
		return false, parser.WrapError(parser.ErrInvalidFormat)
	}

	id, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
	if err != nil {
		return false, parser.WrapError(err)
	}

	return send(event.NewAccessEvent(id)), nil
}
