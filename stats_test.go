// Copyright (c) 2024 Alexey Mayshev. All rights reserved.
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

type testStatsCollector struct{}

func (np testStatsCollector) CollectHits(count int)                     {}
func (np testStatsCollector) CollectMisses(count int)                   {}
func (np testStatsCollector) CollectEviction(weight uint32)             {}
func (np testStatsCollector) CollectRejectedSets(count int)             {}
func (np testStatsCollector) CollectLoadFailure(loadTime time.Duration) {}
func (np testStatsCollector) CollectLoadSuccess(loadTime time.Duration) {}

func TestStatsCollector(t *testing.T) {
	sc := newStatsCollector(testStatsCollector{})

	if _, ok := sc.(*statsCollectorComposition); !ok {
		t.Fatalf("not valid for stats counter. got: %T", sc)
	}
}
