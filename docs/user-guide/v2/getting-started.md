---
tags:
  - v2
---

# Getting started

This chapter is here to help you get started with Otter. It covers all the fundamental features and functionalities of the library, making it easy for new users to familiarise themselves with the basics from initial installation and setup to core functionalities.

## Installation

=== ":fontawesome-brands-golang: Golang"

``` bash
go get -u github.com/maypok86/otter/v2
```

See the [release notes](https://github.com/maypok86/otter/releases) for details of the changes.

Note that otter only supports the two most recent minor versions of Go.

Otter follows semantic versioning for the documented public API on stable releases. `v2` is the latest stable major version.

## Basic Usage

Here's a simple example of how to use Otter:

```go
package main

import (
    "fmt"

    "github.com/maypok86/otter/v2"
)

func main() {
    // Create a cache with basic configuration
    cache := otter.Must(&otter.Options[string, string]{
        MaximumSize:     10_000,
        InitialCapacity: 1_000,
    })

    // Set a value
    cache.Set("key", "value")

    // Get a value
    if value, ok := cache.GetIfPresent("key"); ok {
        fmt.Printf("Value: %s\n", value)
    }

    // Delete a value
    if value, invalidated := cache.Invalidate("key"); invalidated {
        fmt.Printf("Deleted value: %s\n", value)
    }
}
```

You can find more usage examples [here](examples.md).

## Key Features

Otter provides several powerful features:

1. **Size Bounds**: Multiple ways to bound the cache (by entry count or weight)
2. **Expiration**: Flexible expiration policies with TTL support
3. **Refresh**: Automatic refresh of cache entries
4. **Statistics**: Comprehensive cache statistics
5. **Bulk Operations**: Support for bulk get and refresh operations
6. **Event Handlers**: Callbacks for cache events (deletion, atomic deletion)
7. **Loading**: Automatic loading of missing values from external sources
8. **Iteration**: Safe concurrent iteration over cache entries

You can find more detailed examples in the following [chapters](features/index.md) of this section.
