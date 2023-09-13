package unixtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNow(t *testing.T) {
	expected := time.Now().Unix()
	got := Now()
	require.Equal(t, uint64(expected), got)
}
