package parser

import (
	"bufio"
	"io"
	"strconv"
	"strings"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type LIRS struct {
	scanner *bufio.Scanner
}

func NewLIRS(reader io.Reader) *LIRS {
	return &LIRS{
		scanner: bufio.NewScanner(reader),
	}
}

func (l *LIRS) Parse(send func(event event.AccessEvent) bool) (bool, error) {
	if !l.scanner.Scan() {
		if err := l.scanner.Err(); err != nil {
			return false, WrapError(err)
		}

		return true, nil
	}

	line := strings.TrimSpace(l.scanner.Text())
	if line == "" {
		return true, nil
	}

	key, err := strconv.ParseUint(line, 10, 64)
	if err != nil {
		return false, WrapError(err)
	}

	return send(event.NewAccessEvent(key)), nil
}
