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
