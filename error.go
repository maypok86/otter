package otter

const (
	// ErrNotFound should be returned from a Loader.Load/Loader.Reload to indicate that an entry is
	// missing at the underlying data source. This helps the cache to determine
	// if an entry should be deleted.
	//
	// NOTE: this only applies to Cache.Get/Cache.Refresh/Loader.Load/Loader.Reload. For Cache.BulkGet/Cache.BulkRefresh,
	// this works implicitly if you return a map without the key.
	ErrNotFound strError = "otter: the entry was not found in the data source"
)

// errLoaderExited is the error of a load whose loader called runtime.Goexit.
const errLoaderExited strError = "otter: the loader exited the goroutine (runtime.Goexit) without returning"

// strError allows declaring errors as constants.
type strError string

func (err strError) Error() string { return string(err) }
