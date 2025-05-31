package trace

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

func wrapDecoder(r io.Reader, path string) (io.Reader, error) {
	ext := filepath.Ext(path)

	switch ext {
	case ".gz":
		gzipReader, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("not valid .gzip file: %w", err)
		}
		return gzipReader, nil
	case ".zst":
		zstdReader, err := zstd.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("not valid .zst file")
		}
		return zstdReader, nil
	default:
		// without decoding
		return r, nil
	}
}

func NewReader(path string) (io.Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	return wrapDecoder(file, path)
}
