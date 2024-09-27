// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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
	"testing"
	"time"
)

type testStatsRecorder struct{}

func (np testStatsRecorder) RecordHits(count int)                     {}
func (np testStatsRecorder) RecordMisses(count int)                   {}
func (np testStatsRecorder) RecordEviction(weight uint32)             {}
func (np testStatsRecorder) RecordRejections(count int)               {}
func (np testStatsRecorder) RecordLoadFailure(loadTime time.Duration) {}
func (np testStatsRecorder) RecordLoadSuccess(loadTime time.Duration) {}

func TestStatsRecorder(t *testing.T) {
	sc := newStatsRecorder(testStatsRecorder{})

	if _, ok := sc.(*statsRecorderHub); !ok {
		t.Fatalf("not valid stats recorder. got: %T", sc)
	}
}
