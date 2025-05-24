package core

import (
	"math"
	"testing"
	"time"
)

func TestEntry(t *testing.T) {
	t.Parallel()

	k := 2
	v := 3
	exp := int64(math.MaxInt64)
	refr := int64(math.MaxInt64)
	w := uint32(5)
	snapshotAt := int64(0)
	e := Entry[int, int]{
		Key:               k,
		Value:             v,
		ExpiresAtNano:     exp,
		RefreshableAtNano: refr,
		Weight:            w,
		SnapshotAtNano:    snapshotAt,
	}

	if !e.ExpiresAt().Equal(time.Unix(0, exp)) {
		t.Fatalf("not valid expiresAt. want %v, got %v", time.Unix(0, exp), e.ExpiresAt())
	}
	if expiresAfter := e.ExpiresAfter(); expiresAfter != maxDuration {
		t.Fatalf("not valid expiresAfter. want %d, got %d", maxDuration, expiresAfter)
	}
	if e.HasExpired() {
		t.Fatal("entry should not be expire")
	}

	if !e.RefreshableAt().Equal(time.Unix(0, refr)) {
		t.Fatalf("not valid refreshableAt. want %v, got %v", time.Unix(0, refr), e.RefreshableAt())
	}
	if refreshableAfter := e.RefreshableAfter(); refreshableAfter != maxDuration {
		t.Fatalf("not valid refreshableAfter. want %d, got %d", maxDuration, refreshableAfter)
	}

	newExpiresAfter := int64(10)
	e.SnapshotAtNano = time.Now().UnixNano()
	e.ExpiresAtNano = e.SnapshotAtNano + (time.Duration(newExpiresAfter) * time.Second).Nanoseconds()
	if expiresAfter := e.ExpiresAfter(); expiresAfter <= 0 || expiresAfter > time.Duration(newExpiresAfter)*time.Second {
		t.Fatalf("expiresAfter should be in the range (0, %d] seconds, but got %d seconds", newExpiresAfter, expiresAfter/time.Second)
	}
	if e.HasExpired() {
		t.Fatal("entry should not be expire")
	}

	newRefreshableAfter := int64(10)
	e.RefreshableAtNano = e.SnapshotAtNano + (time.Duration(newRefreshableAfter) * time.Second).Nanoseconds()
	if refreshableAfter := e.RefreshableAfter(); refreshableAfter <= 0 || refreshableAfter > time.Duration(newRefreshableAfter)*time.Second {
		t.Fatalf("refreshableAfter should be in the range (0, %d] seconds, but got %d seconds", newRefreshableAfter, refreshableAfter/time.Second)
	}

	e.SnapshotAtNano = snapshotAt
	if !e.SnapshotAt().Equal(time.Unix(0, snapshotAt)) {
		t.Fatalf("not valid snaphotAt. want %v, got %v", time.Unix(0, snapshotAt), e.SnapshotAt())
	}
}
