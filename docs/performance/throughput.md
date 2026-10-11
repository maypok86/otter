# Throughput

Throughput benchmarks are a Go port of the Caffeine [benchmarks](https://github.com/ben-manes/caffeine/blob/master/caffeine/src/jmh/java/com/github/benmanes/caffeine/cache/GetPutBenchmark.java), extended with workloads that evict, expire, load and don't fit in the CPU caches.

## Setup

| | |
|---|---|
| CPU | AMD Ryzen 7 7840H: 8 Zen 4 cores with SMT, a private 1 MB L2 per core and a shared 16 MB L3 |
| OS | Linux (NixOS), the `performance` power profile |
| Go | 1.27.1 |
| Caches | otter (main), theine v0.6.2, ristretto v2.4.2, phuslu/lru v1.0.24, go-freelru v0.16.0, sturdyc v1.1.6, ccache v3.0.8, ttlcache v3.4.1, golang-lru v2.0.7 |
| Method | 8 rounds, each running every cache once, so a slowdown of the machine is spread over all of them. The charts show the median. The run is pinned to one hardware thread per core, so two benchmark threads never share a core. |

Every cache is bounded by the number of entries. sturdyc and ccache always expire entries, so they run with a 1 hour TTL everywhere; the others get a TTL only in the expiration benchmark. All benchmarks except the thread scaling run on 8 threads. The benchmark also reports the hit ratio, so a cache that drops entries can't look faster than it is.

The source code is [here](https://github.com/maypok86/otter/blob/main/benchmarks/throughput/bench_test.go). To reproduce the results, run `bash bench.sh` from `benchmarks/throughput`: the script writes its results to `./results`. On a CPU with SMT, pin it to one hardware thread per core, for example `taskset -c 0,2,4,6,8,10,12,14 bash bench.sh` when the siblings are numbered in pairs.

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

The classic benchmark on 1, 2, 4 and 8 threads. At least 8 goroutines always run, so that the read/write split stays the same on fewer cores. Every thread gets a core of its own: SMT siblings share the execution units of their core, so they would show that sharing rather than how the caches scale.

All 8 cores share one L3, so there is no point at which cache lines start to travel between clusters of cores. On pure reads, otter and theine grow almost linearly, about 7x from 1 to 8 threads, while the sharded LRUs (phuslu/lru, go-freelru) grow 2.5–3x. With writes only, otter grows 5.5x and the sharded LRUs 2.5–3x, while theine, ristretto and ccache, which apply writes to their policy under one lock, don't grow at all. The caches that guard an LRU list with a single mutex (ttlcache, golang-lru) peak on one thread and lose a quarter to more than half of that once a second thread joins.

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
| otter | 72.2% | 55.5% |
| theine | 69.8% | 52.6% |
| ristretto | 72.4% | 55.4% |
| phuslu/lru | 68.8% | 50.5% |
| go-freelru | 68.0% | 49.6% |
| sturdyc | 65.5% | 45.6% |
| ccache | 70.0% | 52.5% |
| ttlcache | 66.7% | 44.9% |
| golang-lru | 69.3% | 48.2% |

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

Otter is the fastest cache on pure reads in every benchmark except the large cache: 1.5x ahead of the next cache in the classic benchmark (165M ops/s against 113M for theine) and 1.4–1.5x under eviction, but only 6% ahead of theine with expiration. With 75% reads it is still the fastest in the classic benchmark (126M ops/s against 92M for phuslu/lru) and under eviction, and about even with phuslu/lru with expiration. It also scales the best, about 7x from 1 to 8 threads on reads and 5.5x on writes. Under eviction, its hit ratio is the highest or within a quarter of a point of it.

On writes, phuslu/lru is faster. With half reads, otter is about even with it in the classic benchmark (95M ops/s against 91M) and slower under eviction and expiration; with fewer reads, phuslu/lru is faster in every benchmark. Its sharded, array-backed LRU makes a write cheap, while otter also updates its eviction policy and buffers. The gap is the largest when every write is an insert that evicts: 65–69M ops/s for phuslu/lru against 6.5–9.4M for otter. Such workloads are uncommon for caches, since they mean a low hit ratio, but if that's your workload, a sharded LRU will serve it faster.

On a single thread, otter is slower than phuslu/lru, go-freelru and golang-lru on every mix: with pure reads, it does 22M ops/s against 24–36M for them. With a million entries, the hash table no longer fits in the CPU caches. There, otter falls behind ristretto on pure reads (31M ops/s against 36M), is level with phuslu/lru, and is behind phuslu/lru on every other mix.

The caches that guard an LRU list with a single mutex (ttlcache, golang-lru) do at most 13M ops/s on any workload from 2 threads up.
