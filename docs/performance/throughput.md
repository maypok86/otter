# Throughput

Throughput benchmarks are a Go port of the Caffeine [benchmarks](https://github.com/ben-manes/caffeine/blob/master/caffeine/src/jmh/java/com/github/benmanes/caffeine/cache/GetPutBenchmark.java), extended with workloads that evict, expire, load and don't fit in the CPU caches.

## Setup

| | |
|---|---|
| CPU | Apple M4 Pro: 8 performance cores in two clusters of 4, each cluster with a shared 16 MB L2, and 4 efficiency cores |
| Go | 1.27.1 |
| Caches | otter (main), theine v0.6.2, ristretto v2.4.2, phuslu/lru v1.0.24, go-freelru v0.16.0, sturdyc v1.1.6, ccache v3.0.8, ttlcache v3.4.1, golang-lru v2.0.7 |
| Method | 8 rounds, each running every cache once, so a slowdown of the machine is spread over all of them. The charts show the median. |

Every cache is bounded by the number of entries. sturdyc and ccache always expire entries, so they run with a 1 hour TTL everywhere; the others get a TTL only in the expiration benchmark. All benchmarks except the thread scaling run on 8 threads. The benchmark also reports the hit ratio, so a cache that drops entries can't look faster than it is.

The source code is [here](https://github.com/maypok86/otter/blob/main/benchmarks/throughput/bench_test.go). To reproduce the results, run `benchmarks/throughput/bench.sh`.

## Classic benchmark

The cache is pre-populated, and the requests follow a zipf distribution. That creates hot spots as some entries are used much more often than others, just like a real cache would experience. A common mistake is a uniform policy (distribution consisting of unique elements) which evenly spreads the contention, which would imply random replacement is the ideal policy. A skewed distribution means that locks suffer higher contention so it exacerbates that as a problem, while also benefiting contention-free implementations who can better utilize the cpu cache.

The benchmark's overhead is thread-local work for an index increment, array lookup, loop, and operation counter. If we introduce a cost like data dependencies (such as by using a random number generator to select the next key), we'd see this fall sharply as we no longer allow the CPU/compiler to use the hardware's full potential.

Every key fits in the cache, so every read is a hit and every write is an update. A miss is much cheaper than a hit because there is no eviction policy to update, and with every key in the cache, a cache can't look faster by losing entries. The benchmarks below cover misses and eviction.

This micro-benchmark tries to isolate the costs to only measuring the cache in a concurrent workload. It would show bottlenecks such as due to hardware cache coherence, data dependencies, poor branch prediction, saturation of the logic units, (lack of) SIMD, waits on the store buffer, blocking and context switches, system calls, inefficient algorithms, etc.

The read/write mix is split by threads: with 75% reads, 6 threads read and 2 threads write.

=== "reads=100%"

    ![reads=100%,writes=0%](../results/throughput/reads=100,writes=0.svg)

=== "reads=75%"

    ![reads=75%,writes=25%](../results/throughput/reads=75,writes=25.svg)

=== "reads=50%"

    ![reads=50%,writes=50%](../results/throughput/reads=50,writes=50.svg)

=== "reads=25%"

    ![reads=25%,writes=75%](../results/throughput/reads=25,writes=75.svg)

=== "reads=0%"

    ![reads=0%,writes=100%](../results/throughput/reads=0,writes=100.svg)

## Thread scaling

The classic benchmark on 1, 2, 4 and 8 threads. At least 8 goroutines always run, so that the read/write split stays the same on fewer cores. The efficiency cores are left out: they are slower, so they would show the difference between the cores rather than how the caches scale.

Up to 4 threads, all of them can run in one cluster and share its L2. From 4 to 8 the second cluster joins, and every cache line that both clusters write has to travel between them. On pure reads, only otter and theine grow noticeably from 4 to 8 threads. With 75% reads, only otter does. With writes only, every cache peaks at 4 threads or fewer.

=== "reads=100%"

    ![scaling reads=100%,writes=0%](../results/throughput/scaling_reads=100,writes=0.svg)

=== "reads=75%"

    ![scaling reads=75%,writes=25%](../results/throughput/scaling_reads=75,writes=25.svg)

=== "reads=50%"

    ![scaling reads=50%,writes=50%](../results/throughput/scaling_reads=50,writes=50.svg)

=== "reads=25%"

    ![scaling reads=25%,writes=75%](../results/throughput/scaling_reads=25,writes=75.svg)

=== "reads=0%"

    ![scaling reads=0%,writes=100%](../results/throughput/scaling_reads=0,writes=100.svg)

## Eviction

The cache holds 8,192 entries out of 32,768 keys, so reads miss and writes insert new entries and evict old ones. The requests follow a zipf or a uniform distribution.

Hit ratio with 75% reads:

| Cache | zipf | uniform |
|---|---|---|
| otter | 72.7% | 56.6% |
| theine | 71.8% | 56.0% |
| ristretto | 72.0% | 55.4% |
| phuslu/lru | 70.7% | 53.9% |
| go-freelru | 70.2% | 50.8% |
| sturdyc | 65.2% | 45.7% |
| ccache | 69.0% | 52.7% |
| ttlcache | 63.0% | 42.9% |
| golang-lru | 68.6% | 51.9% |

### Zipf

=== "reads=100%"

    ![eviction zipf reads=100%,writes=0%](../results/throughput/eviction_zipf_reads=100,writes=0.svg)

=== "reads=75%"

    ![eviction zipf reads=75%,writes=25%](../results/throughput/eviction_zipf_reads=75,writes=25.svg)

=== "reads=50%"

    ![eviction zipf reads=50%,writes=50%](../results/throughput/eviction_zipf_reads=50,writes=50.svg)

=== "reads=25%"

    ![eviction zipf reads=25%,writes=75%](../results/throughput/eviction_zipf_reads=25,writes=75.svg)

=== "reads=0%"

    ![eviction zipf reads=0%,writes=100%](../results/throughput/eviction_zipf_reads=0,writes=100.svg)

### Uniform

=== "reads=100%"

    ![eviction uniform reads=100%,writes=0%](../results/throughput/eviction_uniform_reads=100,writes=0.svg)

=== "reads=75%"

    ![eviction uniform reads=75%,writes=25%](../results/throughput/eviction_uniform_reads=75,writes=25.svg)

=== "reads=50%"

    ![eviction uniform reads=50%,writes=50%](../results/throughput/eviction_uniform_reads=50,writes=50.svg)

=== "reads=25%"

    ![eviction uniform reads=25%,writes=75%](../results/throughput/eviction_uniform_reads=25,writes=75.svg)

=== "reads=0%"

    ![eviction uniform reads=0%,writes=100%](../results/throughput/eviction_uniform_reads=0,writes=100.svg)

## Expiration

The classic benchmark with a 1 hour expiration after write in every cache.

=== "reads=100%"

    ![expiration reads=100%,writes=0%](../results/throughput/expiration_zipf_reads=100,writes=0.svg)

=== "reads=75%"

    ![expiration reads=75%,writes=25%](../results/throughput/expiration_zipf_reads=75,writes=25.svg)

=== "reads=50%"

    ![expiration reads=50%,writes=50%](../results/throughput/expiration_zipf_reads=50,writes=50.svg)

=== "reads=25%"

    ![expiration reads=25%,writes=75%](../results/throughput/expiration_zipf_reads=25,writes=75.svg)

=== "reads=0%"

    ![expiration reads=0%,writes=100%](../results/throughput/expiration_zipf_reads=0,writes=100.svg)

## Large cache

A million entries, so the hash table no longer fits in the CPU caches. Every key fits in the cache.

=== "reads=100%"

    ![large reads=100%,writes=0%](../results/throughput/large_zipf_reads=100,writes=0.svg)

=== "reads=75%"

    ![large reads=75%,writes=25%](../results/throughput/large_zipf_reads=75,writes=25.svg)

=== "reads=50%"

    ![large reads=50%,writes=50%](../results/throughput/large_zipf_reads=50,writes=50.svg)

=== "reads=25%"

    ![large reads=25%,writes=75%](../results/throughput/large_zipf_reads=25,writes=75.svg)

=== "reads=0%"

    ![large reads=0%,writes=100%](../results/throughput/large_zipf_reads=0,writes=100.svg)

## Loading

Every request goes through the loading API of the caches that have one, on the zipf eviction workload, where part of the requests miss and load the value. The loader returns right away, so the chart shows only the overhead of the cache. ccache doesn't deduplicate concurrent loads of the same key, and the sturdyc and ccache clients build a closure per call, because their APIs take the loader per call.

![loading](../results/throughput/loading_zipf_reads=100,writes=0.svg)

## Conclusion

Otter is the fastest cache on reads, and on every workload with at least half reads, in every benchmark. On pure reads it is 1.5–2.2x faster than the next cache. It also keeps scaling when the second cluster of cores joins in: from 4 to 8 threads it grows on pure reads, as theine does, and with 75% reads, where no other cache does. Under eviction, its hit ratio is the highest or within a point of it.

On write-heavy workloads, phuslu/lru is faster. Its sharded, array-backed LRU makes an insert cheap, while otter also updates its eviction policy and buffers. The gap is the largest when every write is an insert that evicts: about 40M ops/s for phuslu/lru against 8–11M for otter. Such workloads are uncommon for caches, since they mean a low hit ratio, but if that's your workload, a sharded LRU will serve it faster. With a single thread and only writes, a mutex-guarded LRU (golang-lru) is also faster than otter, because there is no contention to save on.

The caches that guard an LRU list with a single mutex (ttlcache, golang-lru) stay at 1.7–6M ops/s on any workload from 2 threads up.
