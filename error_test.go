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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPanicError(t *testing.T) {
	t.Parallel()

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = newPanicError(r)
			}
		}()

		panic("olololo")
	}()

	require.Contains(t, err.Error(), "olololo")
}
