<p align="center">
  <img src="https://raw.githubusercontent.com/maypok86/otter/main/assets/logo.png" width="70%" height="auto" >
  <h1 align="center">High performance in-memory cache</h1>
</p>

<p align="center">
<a href="https://pkg.go.dev/github.com/maypok86/otter"><img src="https://pkg.go.dev/badge/github.com/maypok86/otter.svg" alt="Go Reference"></a>
<img src="https://github.com/maypok86/otter/actions/workflows/test.yml/badge.svg" />
<a href="https://codecov.io/gh/maypok86/otter" >
    <img src="https://codecov.io/gh/maypok86/otter/graph/badge.svg?token=G0PJFOR8IF"/>
</a>
<img src="https://goreportcard.com/badge/github.com/maypok86/otter" />
<a href="https://github.com/avelino/awesome-go"><img src="https://awesome.re/mentioned-badge.svg" alt="Mentioned in Awesome Go"></a>
</p>

## :material-lightbulb: Motivation

I once came across the fact that none of the Go cache libraries are truly contention-free. Most of them are a map with a mutex and an eviction policy. Unfortunately, these are not able to reach the speed of caches in other languages (such as [Caffeine](https://github.com/ben-manes/caffeine)). For example, the fastest cache from Dgraph labs called [Ristretto](https://github.com/dgraph-io/ristretto), was faster than competitors by 30% at best (Otter is many times faster) but had [poor hit ratio](https://github.com/dgraph-io/ristretto/issues/336), even though its README says otherwise. This can be a problem in real-world applications, because no one wants to bump into performance of a cache library 🙂. As a result, I wanted to make the fastest, easiest-to-use cache with excellent hit ratio.

## :material-star-shooting: Features

- **Simple API**: Just set the parameters you want in the builder and enjoy
- **Autoconfiguration**: Otter is automatically configured based on the parallelism of your application
- **Generics**: You can safely use any comparable types as keys and any types as values
- **TTL**: Expired values will be automatically deleted from the cache
- **Cost-based eviction**: Otter supports eviction based on the cost of each item
- **Excellent throughput**: Otter is currently the fastest cache library with a huge lead over the [competition](https://github.com/maypok86/otter/blob/main/README.md#throughput)
- **Great hit ratio**: New S3-FIFO algorithm is used, which shows excellent [results](https://github.com/maypok86/otter/blob/main/README.md#hit-ratio)

## :material-pencil: Example

```go
package main

import (
    "fmt"
    "time"

    "github.com/maypok86/otter"
)

func main() {
    // create a cache with capacity equal to 10000 elements
    cache, err := otter.MustBuilder[string, string](10_000).
        CollectStats().
        Cost(func(key string, value string) uint32 {
            return 1
        }).
        WithTTL(time.Hour).
        Build()
    if err != nil {
        panic(err)
    }

    // set item with ttl (1 hour) 
    cache.Set("key", "value")

    // get value from cache
    value, ok := cache.Get("key")
    if !ok {
        panic("not found key")
    }
    fmt.Println(value)

    // delete item from cache
    cache.Delete("key")

    // delete data and stop goroutines
    cache.Close()
}
```

## :material-handshake: Contribute

Contributions are welcome as always, before submitting a new PR please make sure to open a new issue so community members can discuss it.
For more information please see [contribution guidelines](https://github.com/maypok86/otter/blob/main/CONTRIBUTING.md).

Additionally, you might find existing open issues which can help with improvements.

This project follows a standard [code of conduct](https://github.com/maypok86/otter/blob/main/CODE_OF_CONDUCT.md) so that you can understand what actions will and will not be tolerated.

## :material-file-document: License

This project is Apache 2.0 licensed, as found in the [LICENSE](https://github.com/maypok86/otter/blob/main/LICENSE).
