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
