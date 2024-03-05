## 1.1.1 - 2024-03-06

### 🐞 Bug Fixes

- Fixed alignment issues on 32-bit archs

## 1.1.0 - 2024-03-04

The main innovation of this release is node code generation. Thanks to it, the cache will no longer consume more memory due to features that it does not use. For example, if you do not need an expiration policy, then otter will not store the expiration time of each entry. It also allows otter to use more effective expiration policies.

Another expected improvement is the correction of minor synchronization problems due to the state machine. Now otter, unlike other contention-free caches in Go, should not have them at all.

### ✨️Features

- Added `DeleteByFunc` function to cache ([#44](https://github.com/maypok86/otter/issues/44))
- Added `InitialCapacity` function to builder ([#47](https://github.com/maypok86/otter/issues/47))
- Added collection of additional statistics ([#57](https://github.com/maypok86/otter/issues/57))

### 🚀 Improvements

- Added proactive queue-based and timer wheel-based expiration policies with O(1) time complexity ([#55](https://github.com/maypok86/otter/issues/55))
- Added node code generation ([#55](https://github.com/maypok86/otter/issues/55))
- Fixed the race condition when changing the order of events ([#59](https://github.com/maypok86/otter/issues/59))
- Reduced memory consumption on small caches

## 1.0.0 - 2024-01-26

### ✨️Features

- Builder pattern support
- Cleaner API compared to other caches ([#40](https://github.com/maypok86/otter/issues/40))
- Added `SetIfAbsent` and `Range` functions ([#27](https://github.com/maypok86/otter/issues/27))
- Statistics collection ([#4](https://github.com/maypok86/otter/issues/4))
- Cost based eviction
- Support for generics and any comparable types as keys
- Support ttl ([#14](https://github.com/maypok86/otter/issues/14))
- Excellent speed ([benchmark results](https://github.com/maypok86/otter?tab=readme-ov-file#-performance-))
- O(1) worst case time complexity for S3-FIFO instead of O(n)
- Improved hit ratio of S3-FIFO on many traces ([simulator results](https://github.com/maypok86/otter?tab=readme-ov-file#-hit-ratio-))
