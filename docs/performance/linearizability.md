# Linearizability

A cache is linearizable if every concurrent history of its operations can be explained by some order of them, where each operation takes effect at one point between its call and its return. For a cache, the part that matters is that a write which has returned stays written until another operation changes the key. A later read sees it, and a load or refresh that started before it doesn't replace it with an older value. A cache that loses a write keeps serving the stale value until the key is written again or expires.

We checked the caches from the [benchmarks](throughput.md) for this property: their basic operations, loading and refresh.

## Setup

| | |
|---|---|
| Caches | otter (main), theine v0.6.2, ristretto v2.4.2, phuslu/lru v1.0.24, go-freelru v0.16.0, sturdyc v1.1.6, ccache v3.0.8, ttlcache v3.4.1, golang-lru v2.0.7 |
| Histories | 8 goroutines, 200 random operations each, on 2 keys. Written values are positive, loaded values are negative, and every value is unique |
| Checker | [porcupine](https://github.com/anishathalye/porcupine), against a sequential map, one key at a time |
| Configuration | 1024 entries and no expiration (ccache and sturdyc always expire, so they get an hour), which means nothing is evicted or expires during a run. Otherwise each cache is set up as in the throughput benchmarks |

A load is checked as a get-or-load: if the key is present, it returns the value; if not, it loads a value, stores it and returns it. About one load in ten fails, and one in four sleeps in the loader for a bit so that writes have a chance to race with it.

Most violations come down to one of two races, so the tests also run each of them on its own.

In the first, a load misses the key and blocks in its loader. `Set(k, 5)` runs and returns, and then the loader returns `-1`. A linearizable cache must end up with `5`. Either the load took effect first and the `Set` overwrote it, or the load took effect after the `Set`, in which case it should have found `5` and returned it.

In the second, a refresh of `k = 1` blocks in its loader while a `Set` or `Delete` of `k` runs and returns. Then the loader returns a value, or reports that the key doesn't exist. The refresh read the key before the write happened, so if the cache applies its result, it undoes a write that has already returned.

The source code is [here](https://github.com/maypok86/otter/blob/main/benchmarks/linearizability). To reproduce the results, run `LINCHECK_DURATION=15s go test -race -v ./linearizability` from `benchmarks`. The tests log the verdict for each cache and only fail for otter.

## Results

| Cache | Get, Set, Delete | Load | Refresh |
|---|---|---|---|
| otter | yes | yes | yes |
| theine | yes | no | n/a |
| go-freelru | yes | n/a | n/a |
| golang-lru | yes | n/a | n/a |
| ristretto | no | n/a | n/a |
| ccache | yes | no | n/a |
| phuslu/lru | yes | no | n/a |
| sturdyc | yes | no | no |
| ttlcache | yes | no | n/a |

"n/a" means the cache doesn't have the operation.

## Get, Set and Delete

All caches pass except ristretto.

ristretto applies writes asynchronously, through a buffer and its admission policy, and its documentation says a `Set` may be dropped. So a goroutine can call `Set`, then `Get` the same key and miss. The documentation also says `Wait` makes a `Set` visible to later reads, so we tried calling it after every `Set` and `Delete`. That isn't enough. With several goroutines writing, the checker still finds histories where a `Get` misses a key after a `Set` of that key has returned, and no order of the operations explains them.

## Loading

otter is the only cache here whose loading is linearizable. In all the others, a write that completes while a load of the same key is in flight can be lost:

| Cache | What happens |
|---|---|
| theine | `LoadingCache.Get` looks the key up before taking the shard lock and doesn't look again once it holds it, so the load overwrites a `Set` that lands in between. The loader also runs under the shard lock. In the scenario, the `Set` waits for the whole load, and any other write to that shard would wait too |
| ccache | `Fetch` doesn't deduplicate loads. Concurrent loads of one key return different values and overwrite each other and the `Set`. The scenario ends with `-1` |
| phuslu/lru | `GetOrLoad` deduplicates loads, but stores the loaded value unconditionally. The scenario ends with `-1` |
| sturdyc | `GetOrFetch` stores the fetched value unconditionally. The scenario ends with `-1` |
| ttlcache | The loader stores its value with `Set`, unconditionally. The scenario ends with `-1` |

All of these treat a load as a miss followed by a blind write. If you model a load as those two separate operations, the histories are linearizable, but under that model the cache is allowed to lose any write that happens during a load. That's a weak guarantee to build on.

otter stores a loaded value only if the key hasn't been written or invalidated since the load started. If it has, the loaded value is dropped: the `Get` returns the value the key holds now, or loads it again if the key is gone. In the scenario, the load returns `5`, and `5` stays in the cache.

## Refresh

Only otter and sturdyc refresh entries. otter does it through `Refresh`, `BulkRefresh` and a `RefreshCalculator`; sturdyc does it in the background with `WithEarlyRefreshes`.

| Scenario | otter | sturdyc |
|---|---|---|
| `Set` during a reload | the write survives | the reloaded value replaces it |
| `Delete` during a reload | the key stays deleted | the reloaded value brings the key back |
| `Set` during a reload that finds no value | the write survives | the key is deleted, and the write goes with it |

When a sturdyc refresh finishes, it stores the result, or deletes the key if the fetch says it doesn't exist (unless missing records are stored). It doesn't check whether the key changed in the meantime. Since a background refresh has no call or return that a caller can observe, a checker can always place it anywhere after it started, for example right after the write it overwrites. So it's linearizable only in a degenerate sense: the spec it satisfies allows the cache to overwrite any write with an older value.

otter discards a refresh result if the key was written, invalidated or expired after the refresh was requested. That means a refresh is linearizable as two operations. The request runs from the call of `Refresh` until it returns. The commit stores the loaded value only if the key hasn't been written or invalidated since the request and is present exactly when it was present at the request. otter's own [lincheck](https://github.com/maypok86/otter/tree/main/lincheck) checks this model on most of the API, `BulkGet` and eviction included. Its clock doesn't move, though, so no entry expires there and no read starts a refresh.

This has two consequences, and both are deliberate:

- `Invalidate` of an absent key still cancels in-flight loads and refreshes of that key. A load may have read its data source before the change the invalidation is reporting, and storing its result afterwards would bring the old data back.
- A refresh of an absent key loses to a concurrent conditional insert such as `SetIfAbsent`. A refresh is also discarded after any write, even one that leaves the key the way it was when the refresh was requested.

## Limitations

- For the other caches, eviction, expiration and bulk loading aren't checked. otter's lincheck covers eviction and `BulkGet`, but not entries that expire or go stale, and not `BulkRefresh`.
- Passing histories don't prove a cache is linearizable; they only show the checker didn't find a counterexample. A failure is stronger: it's a concrete history that no order of the operations can explain.
