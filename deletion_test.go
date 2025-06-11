package otter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeletionCause_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cause DeletionCause
		want  string
	}{
		{
			cause: CauseInvalidation,
			want:  "Invalidation",
		},
		{
			cause: CauseOverflow,
			want:  "Overflow",
		},
		{
			cause: CauseReplacement,
			want:  "Replacement",
		},
		{
			cause: CauseExpiration,
			want:  "Expiration",
		},
		{
			cause: causeUnknown,
			want:  "<unknown otter.DeletionCause>",
		},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, tt.cause.String())
	}
}

func TestDeletionCause_IsEviction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cause DeletionCause
		want  bool
	}{
		{
			cause: CauseInvalidation,
			want:  false,
		},
		{
			cause: CauseOverflow,
			want:  true,
		},
		{
			cause: CauseReplacement,
			want:  false,
		},
		{
			cause: CauseExpiration,
			want:  true,
		},
		{
			cause: causeUnknown,
			want:  true,
		},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, tt.cause.IsEviction())
	}
}

func TestDeletionEvent_WasEvicted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cause DeletionCause
		want  bool
	}{
		{
			cause: CauseInvalidation,
			want:  false,
		},
		{
			cause: CauseOverflow,
			want:  true,
		},
		{
			cause: CauseReplacement,
			want:  false,
		},
		{
			cause: CauseExpiration,
			want:  true,
		},
		{
			cause: causeUnknown,
			want:  true,
		},
	}

	for _, tt := range tests {
		e := DeletionEvent[int, int]{
			Key:   1,
			Value: 2,
			Cause: tt.cause,
		}
		require.Equal(t, tt.want, e.WasEvicted())
	}
}
