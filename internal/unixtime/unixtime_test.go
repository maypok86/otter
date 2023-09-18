package unixtime

import (
	"testing"
	"time"
)

func TestNow(t *testing.T) {
	expected := time.Now().Unix()
	got := Now()
	if uint64(expected) != got {
		t.Fatalf("unexpected unix time; got %d; want %d", got, expected)
	}
}
