package unixtime

import (
	"testing"
	"time"
)

func TestNow(t *testing.T) {
	Start()

	expected := time.Now().Unix()
	got := Now()
	if uint64(expected) != got {
		t.Fatalf("unexpected unix time; got %d; want %d", got, expected)
	}

	time.Sleep(3 * time.Second)

	expected = time.Now().Unix()
	got = Now()
	if uint64(expected)-got > 1 {
		t.Fatalf("unexpected unix time; got %d; want %d", got, expected)
	}

	Stop()

	time.Sleep(3 * time.Second)

	if Now()-got > 1 {
		t.Fatal("timer should have stopped")
	}
}
