<p align="center">
  <img src="./assets/logo.png" width="70%" height="auto" >
  <h1 align="center">In-memory caching library</h1>
</p>

<p align="center">
<a href="https://pkg.go.dev/github.com/maypok86/otter"><img src="https://pkg.go.dev/badge/github.com/maypok86/otter.svg" alt="Go Reference"></a>
<img src="https://github.com/maypok86/otter/actions/workflows/test.yml/badge.svg" />
<a href="https://github.com/maypok86/otter/actions?query=branch%3Amain+workflow%3ATest" >
    <img src="https://gist.githubusercontent.com/maypok86/2aae2cd39836dc7c258df7ffec602d1c/raw/coverage.svg"/></a>
<a href="https://github.com/maypok86/otter/releases"><img alt="GitHub Release" src="https://img.shields.io/github/v/release/maypok86/otter"></a>
<a href="https://github.com/avelino/awesome-go"><img src="https://awesome.re/mentioned-badge.svg" alt="Mentioned in Awesome Go"></a>
</p>

Otter is designed to provide an excellent developer experience while maintaining blazing-fast performance. It aims to address the shortcomings of its predecessors and incorporates design principles from high-performance libraries in other languages (such as [Caffeine](https://github.com/ben-manes/caffeine)).

## :material-star-shooting: Features

Performance-wise, Otter provides:

- **High hit rate**: [Top-tier hit rates](https://maypok86.github.io/otter/performance/hit-ratio/) across all workload types via adaptive W-TinyLFU
- **Blazing fast**: [Excellent throughput](https://maypok86.github.io/otter/performance/throughput/) under high contention on most workload types
- **Low memory overhead**: Among the [lowest memory overheads](https://maypok86.github.io/otter/performance/memory-consumption/) across all cache capacities
- **Self-tuning**: Automatic data structures configuration based on contention/parallelism and workload patterns

Otter also provides a highly configurable caching API, enabling any combination of these optional features:

- **Eviction**: Size-based [eviction](https://maypok86.github.io/otter/user-guide/v2/features/eviction/#size-based) when a maximum is exceeded
- **Expiration**: Time-based [expiration](https://maypok86.github.io/otter/user-guide/v2/features/eviction/#time-based) of entries (using [Hierarchical Timing Wheel](http://www.cs.columbia.edu/~nahum/w6998/papers/ton97-timing-wheels.pdf)), measured since last access or last write
- **Loading**: [Automatic loading](https://maypok86.github.io/otter/user-guide/v2/features/loading/) of entries into the cache
- **Refresh**: [Asynchronously refresh](https://maypok86.github.io/otter/user-guide/v2/features/refresh/) when the first stale request for an entry occurs
- **Stats**: Accumulation of cache access [statistics](https://maypok86.github.io/otter/user-guide/v2/features/statistics/)

## :material-handshake: Contribute

Contributions are welcome as always, before submitting a new PR please make sure to open a new issue so community members can discuss it.
For more information please see [contribution guidelines](https://github.com/maypok86/otter/blob/main/CONTRIBUTING.md).

Additionally, you might find existing open issues which can help with improvements.

This project follows a standard [code of conduct](https://github.com/maypok86/otter/blob/main/CODE_OF_CONDUCT.md) so that you can understand what actions will and will not be tolerated.

## :material-file-document: License

This project is Apache 2.0 licensed, as found in the [LICENSE](https://github.com/maypok86/otter/blob/main/LICENSE).