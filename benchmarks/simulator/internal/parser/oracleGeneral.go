package parser

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

/*
struct {
    uint32_t timestamp;
    uint64_t obj_id;
    uint32_t obj_size;
    int64_t next_access_vtime;  // -1 if no next access
}
*/

type OracleGeneral struct {
	reader io.Reader
	buffer []byte
}

func NewOracleGeneral(reader io.Reader) *OracleGeneral {
	return &OracleGeneral{
		reader: bufio.NewReader(reader),
		buffer: make([]byte, 24),
	}
}

func (og *OracleGeneral) Parse(send func(event event.AccessEvent) bool) (bool, error) {
	bin := binary.LittleEndian
	_, err := io.ReadFull(og.reader, og.buffer)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return true, nil
		}

		return false, WrapError(err)
	}

	id := bin.Uint64(og.buffer[4:])

	return send(event.NewAccessEvent(id)), nil
}
