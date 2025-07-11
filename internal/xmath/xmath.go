// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
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

import "math"

func Abs(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}

// RoundUpPowerOf2 is based on https://graphics.stanford.edu/~seander/bithacks.html#RoundUpPowerOf2.
func RoundUpPowerOf2(v uint32) uint32 {
	if v == 0 {
		return 1
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}

func RoundUpPowerOf264(x uint64) uint64 {
	if x == 0 {
		return 1
	}
	x--
	x |= x >> 1
	x |= x >> 2
	x |= x >> 4
	x |= x >> 8
	x |= x >> 16
	x |= x >> 32
	x++
	return x
}

func SaturatedAdd(a, b int64) int64 {
	s := a + b
	if s < a || s < b {
		return math.MaxInt64
	}
	return s
}
