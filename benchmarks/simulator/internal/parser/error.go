package parser

import (
	"errors"
	"fmt"
)

var ErrInvalidFormat = errors.New("invalid trace format")

func WrapError(err error) error {
	return fmt.Errorf("parse: %w", err)
}
