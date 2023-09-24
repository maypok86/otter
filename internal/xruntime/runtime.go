package xruntime

import (
	_ "unsafe"
)

const (
	// CacheLineSize is useful for preventing false sharing.
	CacheLineSize = 64
)

//go:noescape
//go:linkname Fastrand runtime.fastrand
func Fastrand() uint32
