# Prometheus Metric Collector

This package implements a [`prometheus.Collector`](https://pkg.go.dev/github.com/prometheus/client_golang@v1.19.0/prometheus#Collector)
to collect metrics about cache's performance, efficiency and usage.

## Example

```go
import (
    "github.com/maypok86/otter"
    "github.com/prometheus/client_golang/prometheus"
    exporter "github.com/maypok86/otter/exporter/prometheus"
)

func main() {
    cache, err := otter.MustBuilder[string, int](1024).
        WithTTTL(time.Hour).
        CollectStats().
        Build()
    if err != nil {
        panic(err)
    }

    collector := exporter.NewCollector(namespace, subsystem, cache)
    prometheus.MustRegister(collector)
}
```

## Metrics
- `hits`: The number of times a key has been found in the cache.
- `misses`: The number of times a key has not been found in the cache.
- `rejected_sets`: The number of times set operations have been rejected.
- `evicted_count`: The number of times a entry has been evicted from the cache.
- `evicted_cost`: The total cost of the evicted entries.

