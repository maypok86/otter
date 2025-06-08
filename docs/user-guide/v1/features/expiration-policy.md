---
tags:
  - v1
---

# Expiration policy

Otter supports two types of expiration policies

1. Expiration policy where all cache entries have the same ttl.
2. Expiration policy, when ttl is not known in advance and is specified for each entry separately.

!!! info "Tip"
    Try to use an expiration policy with constant ttl whenever possible,
    as this allows otter to use more efficient algorithms and helps you write cleaner code.

### WithTTL

The `WithTTL` function in the builder specifies the ttl to be used for all entries in the cache. In fact, calling `Set(key, value)` is equivalent to calling `SetWithTTL(key, value, ttl)` in other libraries.

??? example
    This code creates a cache with constant ttl = time.Hour
    ```go
    cache, err := otter.MustBuilder[string, string](10_000).
        WithTTL(time.Hour).
        Build()
    if err != nil {
        panic(err)
    }
    
    // set item with ttl (1 hour) 
    cache.Set("key1", "value1")
    
    // set item if absent with ttl (1 hour)
    cache.SetIfAbsent("key2", "value2")
    ```

### WithVariableTTL

The `WithVariableTTL` function in the builder tells otter that you want to specify ttl for each entry separately.

??? example
    This code creates a cache with a variable ttl and several entries with unique ttl.
    ```go
    cache, err := otter.MustBuilder[string, string](10_000).
        WithVariableTTL().
        Build()
    if err != nil {
        panic(err)
    }
    
    // set item with ttl (1 hour) 
    cache.Set("key1", "value1", time.Hour)
    
    // set item if absent with ttl (1 minute)
    cache.SetIfAbsent("key2", "value2", time.Minute)
    ```
