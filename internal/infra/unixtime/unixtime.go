package unixtime

import (
	"sync/atomic"
	"time"
)

// We need this package because time.Now() is slower, allocates memory,
// and we don't need a more precise time for the expiry time (and most other operations).
var now uint64

func init() {
	now = uint64(time.Now().Unix())

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for t := range ticker.C {
			atomic.StoreUint64(&now, uint64(t.Unix()))
		}
	}()
}

// Now returns time as a Unix time, the number of seconds elapsed since January 1, 1970 UTC.
func Now() uint64 {
	return atomic.LoadUint64(&now)
}
