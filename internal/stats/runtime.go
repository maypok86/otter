package stats

import (
	"runtime"
	// this is used for fastrand function.
	_ "unsafe"
)

const (
	// useful for preventing false sharing.
	cacheLineSize = 64
)

// based on https://graphics.stanford.edu/~seander/bithacks.html#RoundUpPowerOf2.
func roundUpPowerOf2(v uint32) uint32 {
	if v == 0 {
		return 1
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}

func parallelism() uint32 {
	maxProcs := uint32(runtime.GOMAXPROCS(0))
	numCPU := uint32(runtime.NumCPU())
	if maxProcs < numCPU {
		return maxProcs
	}
	return numCPU
}

//go:noescape
//go:linkname fastrand runtime.fastrand
func fastrand() uint32
