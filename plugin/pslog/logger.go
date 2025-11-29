// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
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

// Package pslog provides a plug-in otter.Logger wrapping slog.Logger for usage in
// an otter.Cache.
//
// This can be used like so:
//
//		cache := otter.Must(&otter.Options[string, string]{
//	         Logger: pslog.New(slog.Default()),
//		     // ...other opts
//		})
package pslog

import (
	"context"
	"log/slog"

	"github.com/maypok86/otter/v2"
)

var _ otter.Logger = (*Logger)(nil)

// Option applies options to the logger.
type Option func(*options)

type options struct {
}

// Logger that wraps the slog.Logger.
type Logger struct {
	log *slog.Logger
}

// New returns a new Logger.
func New(log *slog.Logger, opts ...Option) *Logger {
	if log == nil {
		panic("pslog: log is nil")
	}
	return &Logger{
		log: log,
	}
}

// Warn is for the otter.Logger interface.
func (l *Logger) Warn(ctx context.Context, msg string, err error) {
	l.log.WarnContext(ctx, msg, slog.Any("err", err))
}

// Error is for the otter.Logger interface.
func (l *Logger) Error(ctx context.Context, msg string, err error) {
	l.log.ErrorContext(ctx, msg, slog.Any("err", err))
}
