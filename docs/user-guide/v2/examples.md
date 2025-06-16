---
tags:
  - v2
---

# Examples

For more details, see our [user's guide](./getting-started.md).

## Size-based eviction

### Weight-based eviction

```go
--8<-- "./docs/examples/weight/main.go"
```

### Entry pinning

```go
--8<-- "./docs/examples/entry-pinning/main.go"
```

## Time-based eviction

### Expiration after creation

```go
--8<-- "./docs/examples/expire-after-create/main.go"
```

### Expiration after last write

```go
--8<-- "./docs/examples/expire-after-write/main.go"
```

### Expiration after last access

```go
--8<-- "./docs/examples/expire-after-access/main.go"
```

### Custom ExpiryCalculator

```go
--8<-- "./docs/examples/custom-expiration/main.go"
```

## Loading

### Basic

```go
--8<-- "./docs/examples/basic-loading/main.go"
```

### ErrNotFound

```go
--8<-- "./docs/examples/err-not-found/main.go"
```

### Concurrent loading and invalidation

```go
--8<-- "./docs/examples/loading-linearizability/main.go"
```

### Bulk

```go
--8<-- "./docs/examples/bulk-loading/main.go"
```

### Loading additional keys

```go
--8<-- "./docs/examples/loading-additional-keys/main.go"
```

## Refresh

### Get with refresh

```go
--8<-- "./docs/examples/get-with-refresh/main.go"
```

The same logic applies exactly to `BulkGet` operations.

### Manual refresh

```go
--8<-- "./docs/examples/manual-refresh/main.go"
```

The same logic applies exactly to `BulkRefresh` operations.

## Statistics

### stats.Counter

```go
--8<-- "./docs/examples/stats-counter/main.go"
```

## Event handlers

### OnAtomicDeletion

```go
--8<-- "./docs/examples/on-atomic-deletion/main.go"
```

The same logic applies exactly to `OnDeletion` handler.

## Iteration

### InvalidateByFunc

```go
--8<-- "./docs/examples/invalidate-by-func/main.go"
```

## Extension

```go
--8<-- "./docs/examples/extension/main.go"
```

## "Real-world" examples

### Wrapper

```go
--8<-- "./docs/examples/wrapper/main.go"
```
