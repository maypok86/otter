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

// Package xlog holds the logger that otter and its packages use by default.
package xlog

import (
	"context"
	"log/slog"
)

// Logger logs with the default slog logger. It implements otter.Logger.
type Logger struct {
	log *slog.Logger
}

// Default returns a Logger that logs with slog.Default() at the time of the call.
func Default() *Logger {
	return &Logger{
		log: slog.Default(),
	}
}

// Warn logs a message at the warn level with an error.
func (l *Logger) Warn(ctx context.Context, msg string, err error) {
	l.log.WarnContext(ctx, msg, slog.Any("err", err))
}

// Error logs a message at the error level with an error.
func (l *Logger) Error(ctx context.Context, msg string, err error) {
	l.log.ErrorContext(ctx, msg, slog.Any("err", err))
}
