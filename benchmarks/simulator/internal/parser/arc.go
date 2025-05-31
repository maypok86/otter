package parser

import (
	"bufio"
	"io"
	"strconv"
	"strings"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type ARC struct {
	scanner *bufio.Scanner
}

func NewARC(reader io.Reader) *ARC {
	return &ARC{
		scanner: bufio.NewScanner(reader),
	}
}

func (a *ARC) Parse(send func(event event.AccessEvent) bool) (bool, error) {
	if !a.scanner.Scan() {
		if err := a.scanner.Err(); err != nil {
			return false, WrapError(err)
		}

		return true, nil
	}

	line := a.scanner.Text()
	fields := strings.Fields(line)
	if len(fields) != 4 {
		return false, WrapError(ErrInvalidFormat)
	}

	start, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return false, WrapError(err)
	}
	count, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return false, WrapError(err)
	}

	for i := uint64(0); i < count; i++ {
		if stop := send(event.NewAccessEvent(start + i)); stop {
			return true, nil
		}
	}

	return false, nil
}
