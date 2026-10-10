package policy

import (
	"math/bits"
	"math/rand/v2"
	"testing"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/event"
)

// bestHits returns the most hits any cache of the given capacity can get on
// keys, by trying every choice: on a miss, skip the key, add it, or replace
// any cached key with it. Keys must be below 8.
func bestHits(keys []uint64, capacity int) int {
	memo := make(map[[2]int]int)
	var solve func(i, cached int) int
	solve = func(i, cached int) int {
		if i == len(keys) {
			return 0
		}
		state := [2]int{i, cached}
		if v, ok := memo[state]; ok {
			return v
		}
		bit := 1 << keys[i]
		var best int
		if cached&bit != 0 {
			best = 1 + solve(i+1, cached)
		} else {
			best = solve(i+1, cached)
			if bits.OnesCount(uint(cached)) < capacity {
				best = max(best, solve(i+1, cached|bit))
			}
			for victim := 1; victim <= cached; victim <<= 1 {
				if cached&victim != 0 {
					best = max(best, solve(i+1, cached&^victim|bit))
				}
			}
		}
		memo[state] = best
		return best
	}
	return solve(0, 0)
}

func TestOptimal_MatchesExhaustiveSearch(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		n := 1 + r.IntN(14)
		keys := make([]uint64, n)
		for i := range keys {
			keys[i] = uint64(r.IntN(5))
		}
		capacity := 1 + r.IntN(3)

		o := NewOptimal()
		o.Init(capacity)
		for _, key := range keys {
			o.Record(event.NewAccessEvent(key))
		}

		want := 100 * float64(bestHits(keys, capacity)) / float64(n)
		if got := o.Ratio(); got != want {
			t.Fatalf("keys %v, capacity %d: got %.2f%%, want %.2f%%", keys, capacity, got, want)
		}
	}
}
