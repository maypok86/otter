// Copyright (c) 2023 Alexey Mayshev. All rights reserved.
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

package unixtime

import (
	"testing"
	"time"
)

func TestNow(t *testing.T) {
	Start()

	got := Now()
	if got != 0 {
		t.Fatalf("unexpected time since program start; got %d; want %d", got, 0)
	}

	time.Sleep(3 * time.Second)

	got = Now()
	if got != 2 && got != 3 {
		t.Fatalf("unexpected time since program start; got %d; want %d", got, 3)
	}

	Stop()

	time.Sleep(3 * time.Second)

	if Now()-got > 1 {
		t.Fatal("timer should have stopped")
	}
}
