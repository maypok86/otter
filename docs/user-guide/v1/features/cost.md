---
tags:
  - v1
---

# Cost-based eviction

Otter supports eviction based on the cost of each entry.
For example, if the entries in your cache have radically different memory footprints,
you can specify the cost in the `NewBuilder` function and
pass the entry cost calculation function to the `Cost` function in the builder.

??? example
    This code creates a cache in which the total size of key-value pairs is limited to 100 MB.
    ```go
    cache, err := otter.MustBuilder[string, string](100 * 1024 * 1024).
        Cost(func(key string, value string) uint32 {
            return uint32(len(key) + len(value))
        }).
        Build()
    ```

??? failure "Wrong way"
    Unfortunately, this code will cause the cache capacity to be limited to 104857600 **entries**,
    resulting in huge memory consumption.
    ```go
    cache, err := otter.MustBuilder[string, string](100 * 1024 * 1024).
        Cost(func(key string, value string) uint32 {
            return 1
        }).
        Build()
    ```

!!! warning
    Do not pass in `Cost` to a function that always returns 1.
    If you do, otter will have no way of determining that
    you don't really need cost-based eviction and will store cost for each entry.
    This will end up consuming more memory than it could.
    ```go
    cache, err := otter.MustBuilder[string, string](10_000).
        // don't do that.
        Cost(func(key string, value string) uint32 {
            return 1
        }).
        Build()
    ```
