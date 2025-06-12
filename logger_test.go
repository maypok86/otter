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

package otter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func checkLogOutput(t *testing.T, got, wantRegexp string) {
	t.Helper()
	got = clean(got)
	wantRegexp = "^" + wantRegexp + "$"
	matched, err := regexp.MatchString(wantRegexp, got)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Errorf("\ngot  %s\nwant %s", got, wantRegexp)
	}
}

// clean prepares log output for comparison.
func clean(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return strings.ReplaceAll(s, "\n", "~")
}

// textTimeRE is a regexp to match log timestamps for Text handler.
// This is RFC3339Nano with the fixed 3 digit sub-second precision.
const textTimeRE = `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{2}:\d{2})`

func TestDefaultLogger(t *testing.T) {
	d := slog.Default()

	var b bytes.Buffer
	def := slog.New(slog.NewTextHandler(&b, nil))

	slog.SetDefault(def)
	t.Cleanup(func() {
		slog.SetDefault(d)
	})

	check := func(want string) {
		t.Helper()
		if want != "" {
			want = "time=" + textTimeRE + " " + want
		}
		checkLogOutput(t, b.String(), want)
		b.Reset()
	}

	dl := newDefaultLogger()
	dl.Error(context.Background(), "lololol", ErrNotFound)
	check("level=ERROR msg=lololol err=\"otter: the entry was not found in the data source\"")
	dl.Warn(context.Background(), "qokpokp", fmt.Errorf("gol: %w", ErrNotFound))
	check("level=WARN msg=qokpokp err=\"gol: otter: the entry was not found in the data source\"")
}

func TestNoopLogger(t *testing.T) {
	t.Parallel()

	nl := &NoopLogger{}
	require.NotPanics(t, func() {
		nl.Warn(context.Background(), "lololoo", ErrNotFound)
		nl.Error(context.Background(), "ytuvut", errors.New("hjihiuh"))
	})
}
