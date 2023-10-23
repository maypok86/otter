package stats

import (
	"math/rand"
	"testing"
	"time"
)

const (
	maxCount = 10_000
)

func generateCount(t *testing.T) int64 {
	t.Helper()

	r := rand.NewSource(time.Now().UnixNano())
	count := r.Int63() % maxCount
	if count == 0 {
		return 1
	}

	return count
}

func TestStats_Nil(t *testing.T) {
	var s *Stats
	expected := int64(0)
	for _, inc := range []func(){
		s.IncHits,
		s.IncMisses,
		s.IncHits,
		s.IncMisses,
	} {
		inc()
	}
	for _, f := range []func() int64{
		s.Hits,
		s.Misses,
		func() int64 {
			return int64(s.Ratio())
		},
	} {
		if expected != f() {
			t.Fatalf("hits and misses for nil stats should always be %d", expected)
		}
	}
	s.Clear()
}

func TestStats_Hits(t *testing.T) {
	expected := generateCount(t)

	s := New()
	for i := int64(0); i < expected; i++ {
		s.IncHits()
	}

	hits := s.Hits()
	if expected != hits {
		t.Fatalf("number of hits should be %d, but got %d", expected, hits)
	}
}

func TestStats_Misses(t *testing.T) {
	expected := generateCount(t)

	s := New()
	for i := int64(0); i < expected; i++ {
		s.IncMisses()
	}

	misses := s.Misses()
	if expected != misses {
		t.Fatalf("number of misses should be %d, but got %d", expected, misses)
	}
}

func TestStats_Ratio(t *testing.T) {
	s := New()
	ratio := s.Ratio()
	if ratio != 0.0 {
		t.Fatalf("ratio in stats without operations should be 0.0, but got %v", ratio)
	}

	count := generateCount(t)
	for i := int64(0); i < count; i++ {
		s.IncHits()
	}
	for i := int64(0); i < count; i++ {
		s.IncMisses()
	}

	expected := 0.5
	ratio = s.Ratio()
	if expected != ratio {
		t.Fatalf("ratio should be %v, but got %v", expected, ratio)
	}
}

func TestStats_Clear(t *testing.T) {
	s := New()

	count := generateCount(t)
	for i := int64(0); i < count; i++ {
		s.IncHits()
	}
	for i := int64(0); i < count; i++ {
		s.IncMisses()
	}

	misses := s.Misses()
	if count != misses {
		t.Fatalf("number of misses should be %d, but got %d", count, misses)
	}
	hits := s.Hits()
	if count != hits {
		t.Fatalf("number of hits should be %d, but got %d", count, hits)
	}

	s.Clear()

	hits = s.Hits()
	misses = s.Misses()
	if hits != 0 || misses != 0 {
		t.Fatalf("hits and misses after clear should be 0, but got hits: %d and misses: %d", hits, misses)
	}
}
