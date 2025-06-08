---
tags:
  - v2
---

# Getting started

This chapter is here to help you get started with Otter. It covers all the fundamental features and functionalities of the library, making it easy for new users to familiarise themselves with the basics from initial installation and setup to core functionalities.

## Installing Otter

=== ":fontawesome-brands-golang: Golang"

``` bash
go get -u github.com/maypok86/otter
```

## Basic Usage

Otter uses an options pattern that allows you to conveniently create a cache instance with different parameters.

??? example
    This code creates a cache with the same ttl (applied automatically) for each entry, with cache access statistics collection enabled, and demonstrates some basic operations.

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

You can find more detailed examples in the following [chapters](features/index.md) of this section.
