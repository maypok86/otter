// Copyright (c) 2026 Alexey Mayshev and contributors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package xruntime

import (
	"bytes"
	"fmt"
	"runtime/debug"
)

// PanicError is an arbitrary value recovered from a panic
// with the stack trace during the execution of given function.
type PanicError struct {
	Value any
	Stack []byte
}

// Error implements error interface.
func (p *PanicError) Error() string {
	return fmt.Sprintf("%v\n\n%s", p.Value, p.Stack)
}

func (p *PanicError) Unwrap() error {
	err, ok := p.Value.(error)
	if !ok {
		return nil
	}

	return err
}

// NewPanicError returns a PanicError for v. It must be called by the deferred function that
// recovered v, while the stack still holds the frames of the panic.
func NewPanicError(v any) error {
	stack := debug.Stack()

	// The first line of the stack trace is of the form "goroutine N [status]:"
	// but by the time the panic reaches cache the goroutine may no longer exist
	// and its status will have changed. Trim out the misleading line.
	if line := bytes.IndexByte(stack, '\n'); line >= 0 {
		stack = stack[line+1:]
	}
	return &PanicError{Value: v, Stack: stack}
}
