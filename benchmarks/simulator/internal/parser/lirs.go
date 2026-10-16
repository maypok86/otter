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
	if line == "" || line == "*" {
		// "*" is a breakpoint of the LIRS authors' simulator, not an access
		return true, nil
	}

	key, err := strconv.ParseUint(line, 10, 64)
	if err != nil {
		// Traces rewritten from other formats carry signed 64-bit keys.
		signed, serr := strconv.ParseInt(line, 10, 64)
		if serr != nil {
			return false, WrapError(err)
		}
		key = uint64(signed)
	}

	return send(event.NewAccessEvent(key)), nil
}
