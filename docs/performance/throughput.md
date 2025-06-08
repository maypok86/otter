# Throughput

Throughput benchmarks are a Go port of the caffeine [benchmarks](https://github.com/ben-manes/caffeine/blob/master/caffeine/src/jmh/java/com/github/benmanes/caffeine/cache/GetPutBenchmark.java).

### Description

The [benchmark](https://github.com/maypok86/benchmarks/blob/main/throughput/bench_test.go) that I use has a pre-populated cache with a zipf distribution for the access requests. That creates hot spots as some entries are used much more often than others, just like a real cache would experience. A common mistake is a uniform policy (distribution consisting of unique elements) which evenly spreads the contention, which would imply random replacement is the ideal policy. A skewed distribution means that locks suffer higher contention so it exacerbates that as a problems, while also benefiting contention-free implementations who can better utilize the cpu cache.

The benchmark's overhead is thread-local work for an index increment, array lookup, loop, and operation counter. If we introduce a cost like data dependencies (such as by using a random number generator to select the next key), we'd see this fall sharply as we no longer allow the CPU/compiler to use the hardware's full potential.

Also in benchmarks, the cache initially fully populated because a cache miss always takes much less time than a hit, since you don't need to update the eviction policy. Because of this, implementations with a small hit ratio get a huge advantage and as a result it is easy to misinterpret the results of such benchmarks.

In the end, I think this is one of the best benchmarks to compare cache speeds because this micro-benchmark tries to isolate the costs to only measuring the cache in a concurrent workload. It would show bottlenecks such as due to hardware cache coherence, data dependencies, poor branch prediction, saturation of the logic units (there are multiple ALUs for every FP unit), (lack of) SIMD, waits on the store buffer, blocking and context switches, system calls, inefficient algorithms, etc.

### Read (100%)

In this [benchmark](https://github.com/maypok86/benchmarks/blob/main/throughput/bench_test.go) **8 threads** concurrently read from a cache configured with a maximum size.

![reads=100%,writes=0%](https://raw.githubusercontent.com/maypok86/benchmarks/main/throughput/results/reads=100,writes=0.png)

### Read (75%) / Write (25%)

In this [benchmark](https://github.com/maypok86/benchmarks/blob/main/throughput/bench_test.go) **6 threads** concurrently read from and **2 threads** write to a cache configured with a maximum size.

![reads=75%,writes=25%](https://raw.githubusercontent.com/maypok86/benchmarks/main/throughput/results/reads=75,writes=25.png)

### Read (50%) / Write (50%)

In this [benchmark](https://github.com/maypok86/benchmarks/blob/main/throughput/bench_test.go) **4 threads** concurrently read from and **4 threads** write to a cache configured with a maximum size.

![reads=50%,writes=50%](https://raw.githubusercontent.com/maypok86/benchmarks/main/throughput/results/reads=50,writes=50.png)

### Read (25%) / Write (75%)

In this [benchmark](https://github.com/maypok86/benchmarks/blob/main/throughput/bench_test.go) **2 threads** concurrently read from and **6 threads** write to a cache configured with a maximum size.

![reads=25%,writes=75%](https://raw.githubusercontent.com/maypok86/benchmarks/main/throughput/results/reads=25,writes=75.png)

### Write (100%)

In this [benchmark](https://github.com/maypok86/benchmarks/blob/main/throughput/bench_test.go) **8 threads** concurrently write to a cache configured with a maximum size.

![reads=0%,writes=100%](https://raw.githubusercontent.com/maypok86/benchmarks/main/throughput/results/reads=0,writes=100.png)

### Conclusion

Otter shows fantastic speed under all workloads except extreme write-heavy, but such a workload is rare for caches and usually indicates that the cache has a very small hit ratio.
