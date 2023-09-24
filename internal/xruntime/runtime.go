package xruntime

import (
	"runtime"
	_ "unsafe"
)

const (
	// CacheLineSize is useful for preventing false sharing.
	CacheLineSize = 64
)

func Parallelism() uint32 {
	maxProcs := uint32(runtime.GOMAXPROCS(0))
	numCPU := uint32(runtime.NumCPU())
	if maxProcs < numCPU {
		return maxProcs
	}
	return numCPU
}

//go:noescape
//go:linkname Fastrand runtime.fastrand
func Fastrand() uint32
