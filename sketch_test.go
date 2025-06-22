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
		do            func(t *testing.T, s *sketch[int])
	}{
		{
			name:          "construct",
			withoutEnsure: true,
			do: func(t *testing.T, s *sketch[int]) {
				require.Nil(t, s.table)
				require.True(t, s.isNotInitialized())

				item := rand.Int()
				s.increment(item)
				require.Equal(t, uint64(0), s.frequency(item))
			},
		},
		{
			name: "ensureCapacity_smaller",
			do: func(t *testing.T, s *sketch[int]) {
				size := uint64(len(s.table))
				s.ensureCapacity(size / 2)
				require.Len(t, s.table, int(size))
				require.Equal(t, 10*size, s.sampleSize)
				require.Equal(t, (size>>3)-1, s.blockMask)
			},
		},
		{
			name: "ensureCapacity_larger",
			do: func(t *testing.T, s *sketch[int]) {
				size := uint64(len(s.table))
				s.ensureCapacity(size * 2)
				require.Len(t, s.table, 2*int(size))
				require.Equal(t, 10*2*size, s.sampleSize)
				require.Equal(t, ((2*size)>>3)-1, s.blockMask)
			},
		},
		{
			name: "increment_once",
			do: func(t *testing.T, s *sketch[int]) {
				item := rand.Int()

				s.increment(item)
				require.Equal(t, uint64(1), s.frequency(item))
			},
		},
		{
			name: "increment_max",
			do: func(t *testing.T, s *sketch[int]) {
				item := rand.Int()

				for i := 0; i < 20; i++ {
					s.increment(item)
				}
				require.Equal(t, uint64(15), s.frequency(item))
			},
		},
		{
			name: "increment_distinct",
			do: func(t *testing.T, s *sketch[int]) {
				item := rand.Int()

				s.increment(item)
				s.increment(item + 1)

				require.Equal(t, uint64(1), s.frequency(item))
				require.Equal(t, uint64(1), s.frequency(item+1))
				require.Equal(t, uint64(0), s.frequency(item+2))
			},
		},
		{
			name: "increment_zero",
			do: func(t *testing.T, s *sketch[int]) {
				s.increment(0)
				require.Equal(t, uint64(1), s.frequency(0))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newSketch[int]()
			if !tt.withoutEnsure {
				s.ensureCapacity(512)
			}

			tt.do(t, s)
		})
	}
}

func TestSketch_Reset(t *testing.T) {
	t.Parallel()

	reset := false
	s := newSketch[int]()
	s.ensureCapacity(64)

	for i := 1; i < 20*len(s.table); i++ {
		s.increment(i)
		if s.size != uint64(i) {
			reset = true
			break
		}
	}

	require.True(t, reset)
	require.LessOrEqual(t, s.size, s.sampleSize/2)
}

func TestSketch_Full(t *testing.T) {
	t.Parallel()

	s := newSketch[int]()
	s.ensureCapacity(512)
	s.sampleSize = math.MaxUint64

	for i := 0; i < 100000; i++ {
		s.increment(i)
	}

	for _, slot := range s.table {
		require.Equal(t, 64, bits.OnesCount64(slot))
	}
}

func TestSketch_HeavyHitters(t *testing.T) {
	t.Parallel()

	s := newSketch[float64]()
	s.ensureCapacity(2000)

	for i := 100; i < 5000; i++ {
		s.increment(float64(i))
	}
	for i := 0; i < 10; i += 2 {
		for j := 0; j < i; j++ {
			s.increment(float64(i))
		}
	}

	// A perfect popularity count yields an array [0, 0, 2, 0, 4, 0, 6, 0, 8, 0].
	popularity := make([]uint64, 0, 10)
	for i := 0; i < 10; i++ {
		popularity = append(popularity, s.frequency(float64(i)))
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
