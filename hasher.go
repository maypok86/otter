package otter

import (
	"unsafe"

	"github.com/zeebo/xxh3"
)

type hasher[K comparable] struct {
	keyIsString bool
	keySize     int
}

func newHasher[K comparable]() *hasher[K] {
	h := &hasher[K]{}

	var key K
	switch (any(key)).(type) {
	case string:
		h.keyIsString = true
	default:
		h.keySize = int(unsafe.Sizeof(key))
	}

	return h
}

func (h *hasher[K]) hash(key K) uint64 {
	var strKey string
	if h.keyIsString {
		strKey = *(*string)(unsafe.Pointer(&key))
	} else {
		strKey = *(*string)(unsafe.Pointer(&struct {
			data unsafe.Pointer
			len  int
		}{unsafe.Pointer(&key), h.keySize}))
	}

	return xxh3.HashString(strKey)
}
