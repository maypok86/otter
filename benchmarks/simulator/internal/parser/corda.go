package parser

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

type Corda struct {
	reader io.Reader
	buffer []byte
}

func NewCorda(reader io.Reader) *Corda {
	return &Corda{
		reader: bufio.NewReader(reader),
		buffer: make([]byte, 8),
	}
}

func (c *Corda) Parse(send func(event event.AccessEvent) bool) (bool, error) {
	bin := binary.BigEndian
	_, err := io.ReadFull(c.reader, c.buffer)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return true, nil
		}

		return false, WrapError(err)
	}

	key := bin.Uint64(c.buffer)

	return send(event.NewAccessEvent(key)), nil
}
