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

package xmath

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAbs(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(1), Abs(1))
	require.Equal(t, int64(1), Abs(-1))
}

func TestRoundUpPowerOf2(t *testing.T) {
	t.Parallel()

	require.Equal(t, uint32(1), RoundUpPowerOf2(0))
	require.Equal(t, uint32(4), RoundUpPowerOf2(3))
	require.Equal(t, uint32(4), RoundUpPowerOf2(4))
}

func TestRoundUpPowerOf264(t *testing.T) {
	t.Parallel()

	require.Equal(t, uint64(1), RoundUpPowerOf264(0))
	require.Equal(t, uint64(4), RoundUpPowerOf264(3))
	require.Equal(t, uint64(4), RoundUpPowerOf264(4))
}
