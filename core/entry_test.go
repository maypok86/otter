package core

import (
	"testing"
	"time"
)

func TestEntry(t *testing.T) {
	k := 2
	v := 3
	exp := int64(0)
	refr := int64(0)
	w := uint32(5)
	snapshotAt := time.Now().UnixNano()
	e := Entry[int, int]{
		Key:            k,
		Value:          v,
		ExpiresAtNano:  exp,
		Weight:         w,
		SnapshotAtNano: snapshotAt,
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
	e.ExpiresAtNano = time.Now().UnixNano() + (time.Duration(newExpiresAfter) * time.Second).Nanoseconds()
	if expiresAfter := e.ExpiresAfter(); expiresAfter <= 0 || expiresAfter > time.Duration(newExpiresAfter)*time.Second {
		t.Fatalf("expiresAfter should be in the range (0, %d] seconds, but got %d seconds", newExpiresAfter, expiresAfter/time.Second)
	}
	if e.HasExpired() {
		t.Fatal("entry should not be expire")
	}

	newRefreshableAfter := int64(10)
	e.RefreshableAtNano = time.Now().UnixNano() + (time.Duration(newRefreshableAfter) * time.Second).Nanoseconds()
	if refreshableAfter := e.RefreshableAfter(); refreshableAfter <= 0 || refreshableAfter > time.Duration(newRefreshableAfter)*time.Second {
		t.Fatalf("refreshableAfter should be in the range (0, %d] seconds, but got %d seconds", newRefreshableAfter, refreshableAfter/time.Second)
	}

	if !e.SnapshotAt().Equal(time.Unix(0, snapshotAt)) {
		t.Fatalf("not valid snaphotAt. want %v, got %v", time.Unix(0, snapshotAt), e.SnapshotAt())
	}
}
