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

package tinylfu

import (
	"math"
	"math/bits"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSketch_Basic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		withoutEnsure bool
		do            func(t *testing.T, s *Sketch[int])
	}{
		{
			name:          "construct",
			withoutEnsure: true,
			do: func(t *testing.T, s *Sketch[int]) {
				require.Nil(t, s.Table)
				require.True(t, s.IsNotInitialized())

				item := rand.Int()
				s.Increment(item)
				require.Equal(t, uint64(0), s.Frequency(item))
			},
		},
		{
			name: "ensureCapacity_smaller",
			do: func(t *testing.T, s *Sketch[int]) {
				size := uint64(len(s.Table))
				s.EnsureCapacity(size / 2)
				require.Len(t, s.Table, int(size))
				require.Equal(t, 10*size, s.SampleSize)
				require.Equal(t, (size>>3)-1, s.BlockMask)
			},
		},
		{
			name: "ensureCapacity_larger",
			do: func(t *testing.T, s *Sketch[int]) {
				size := uint64(len(s.Table))
				s.EnsureCapacity(size * 2)
				require.Len(t, s.Table, 2*int(size))
				require.Equal(t, 10*2*size, s.SampleSize)
				require.Equal(t, ((2*size)>>3)-1, s.BlockMask)
			},
		},
		{
			name: "increment_once",
			do: func(t *testing.T, s *Sketch[int]) {
				item := rand.Int()

				s.Increment(item)
				require.Equal(t, uint64(1), s.Frequency(item))
			},
		},
		{
			name: "increment_max",
			do: func(t *testing.T, s *Sketch[int]) {
				item := rand.Int()

				for i := 0; i < 20; i++ {
					s.Increment(item)
				}
				require.Equal(t, uint64(15), s.Frequency(item))
			},
		},
		{
			name: "increment_distinct",
			do: func(t *testing.T, s *Sketch[int]) {
				item := rand.Int()

				s.Increment(item)
				s.Increment(item + 1)

				require.Equal(t, uint64(1), s.Frequency(item))
				require.Equal(t, uint64(1), s.Frequency(item+1))
				require.Equal(t, uint64(0), s.Frequency(item+2))
			},
		},
		{
			name: "increment_zero",
			do: func(t *testing.T, s *Sketch[int]) {
				s.Increment(0)
				require.Equal(t, uint64(1), s.Frequency(0))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newSketch[int]()
			if !tt.withoutEnsure {
				s.EnsureCapacity(512)
			}

			tt.do(t, s)
		})
	}
}

func TestSketch_Reset(t *testing.T) {
	t.Parallel()

	reset := false
	s := newSketch[int]()
	s.EnsureCapacity(64)

	for i := 1; i < 20*len(s.Table); i++ {
		s.Increment(i)
		if s.Size != uint64(i) {
			reset = true
			break
		}
	}

	require.True(t, reset)
	require.LessOrEqual(t, s.Size, s.SampleSize/2)
}

func TestSketch_Full(t *testing.T) {
	t.Parallel()

	s := newSketch[int]()
	s.EnsureCapacity(512)
	s.SampleSize = math.MaxUint64

	for i := 0; i < 100000; i++ {
		s.Increment(i)
	}

	for _, slot := range s.Table {
		require.Equal(t, 64, bits.OnesCount64(slot))
	}
}

func TestSketch_HeavyHitters(t *testing.T) {
	t.Parallel()

	s := newSketch[float64]()
	s.EnsureCapacity(2000)

	for i := 100; i < 5000; i++ {
		s.Increment(float64(i))
	}
	for i := 0; i < 10; i += 2 {
		for j := 0; j < i; j++ {
			s.Increment(float64(i))
		}
	}

	// A perfect popularity count yields an array [0, 0, 2, 0, 4, 0, 6, 0, 8, 0].
	popularity := make([]uint64, 0, 10)
	for i := 0; i < 10; i++ {
		popularity = append(popularity, s.Frequency(float64(i)))
	}
	for i := 0; i < len(popularity); i++ {
		switch i {
		case 0, 1, 3, 5, 7, 9:
			require.LessOrEqual(t, popularity[i], popularity[2])
		case 2:
			require.LessOrEqual(t, popularity[2], popularity[4])
		case 4:
			require.LessOrEqual(t, popularity[4], popularity[6])
		case 6:
			require.LessOrEqual(t, popularity[6], popularity[8])
		}
	}
}
