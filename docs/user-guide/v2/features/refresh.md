---
tags:
  - v2
---

# Refresh

```go
cache := otter.Must(&otter.Options[string, string]{
	ExpiryCalculator: otter.ExpiryWriting[string, string](time.Hour),
	RefreshCalculator: otter.RefreshWriting[string, string](30*time.Minute),
})
```

Refreshing is not quite the same as eviction. As specified in `Cache.Refresh`, refreshing a key loads a new value for the key asynchronously. The old value (if any) is still returned while the key is being refreshed, in contrast to eviction, which forces retrievals to wait until the value is loaded anew.

In contrast to `ExpiryCalculator`, `RefreshCalculator` will make a key eligible for refresh after the returned duration, but a refresh will only be actually initiated when the entry is queried. So, for example, you can specify both `ExpiryCalculator`, `RefreshCalculator` on the same cache so that the expiration timer on an entry isn't blindly reset whenever an entry becomes eligible for a refresh. If an entry isn't queried after it comes eligible for refreshing, it is allowed to expire.

A `Loader` may specify smart behavior to use on a refresh by overriding `Loader.Reload` which allows you to use the old value in computing the new value.

`Cache.Refresh` can be used to explicitly refresh an entry and will deduplicate requests while they are in-flight.

Refresh operations are executed asynchronously using goroutine.

If an error is returned after refresh then the old value is kept and the error is logged (using `Logger`) and swallowed.
