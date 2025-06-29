---
date: 2025-06-29
categories:
  - General
slug: cache-evolution
---

# The Evolution of Caching Libraries in Go

For the last few years, I've been developing caching library, and today I'd like to talk about the evolution of caches in Go and where we stand today. Especially since Go 1.24 was officially supposed to focus on caching improvements, but I haven't heard much news about them - time to fix that :slight_smile:.

## On-heap VS Off-heap

Before we begin, I believe it’s important to mention that in programming languages with GC (such as Go), caching libraries are divided into two main types: on-heap and off-heap. On-heap caches allocate memory in the heap, while off-heap caches allocate memory (for example) using mmap and then manage it manually.

Off-heap caches offer two primary benefits: they eliminate GC pauses regardless of entry count, and they maintain minimal memory overhead for metadata. These advantages, however, are offset by several limitations:

- Poor eviction policy in most cases. Go off-heap caches often use something very similar to FIFO, which typically has a worse hit rate than LRU.
- You have to convert your keys and values into strings/byte slices, as these are used as keys and values. This conversion will likely be much more expensive than any in-memory cache access operation.
- Lack of many useful features, which we’ll discuss later.

Off-heap caches are typically useful when your cache must keep all data in memory permanently and rarely (if ever) evict entries. This becomes essential, for example, when your service's SLA depends on maintaining a hit rate consistently above 99.9%.

On-heap caches, in exchange for some GC overhead, solve all of the above drawbacks.

I should mention right away that in this article, we’ll only discuss on-heap caches, as they evolve independently of off-heap caches—and my knowledge of off-heap cache internals is relatively superficial. Just keep these differences in mind when choosing a cache.

## Early development

For a long time, Go lacked an advanced concurrent cache. All libraries essentially offered a mutex-protected map with LRU or LFU eviction. If you encountered scaling issues across CPU cores, the suggested solution was to shard the entire cache into multiple parts (lock striping). However, this approach comes with several problems:

- While LRU and LFU exhibit non-optimal hit rates, implementing advanced eviction algorithms can yield substantial hit rate improvements. This matters because cache misses (necessitating expensive backend fetches) are typically orders of magnitude costlier than hits.
- In reality, lock striping provides limited benefits because real-world workloads [follow Zipf-like distributions](https://ieeexplore.ieee.org/document/749260). In Zipf distributions, the most frequent items occur exponentially more often than others. This means some stripes will experience high contention while others remain nearly idle.

## Ristretto

[Ristretto](https://github.com/hypermodeinc/ristretto) was developed by Dgraph Labs in 2019 to address a gap in Go's ecosystem. The library was exceptionally well-received by the community and effectively became the undisputed leader among on-heap caches in Go for years (and arguably remains dominant in some respects today).

During development, the Dgraph team drew inspiration from [Caffeine](https://github.com/ben-manes/caffeine), widely regarded as the best caching library in Java and arguably across all programming languages. They used the [BP-Wrapper](https://www.researchgate.net/publication/220966845_BP-Wrapper_A_System_Framework_Making_Any_Replacement_Algorithms_Almost_Lock_Contention_Free) technique to mitigate contention and implemented [TinyLFU](https://dl.acm.org/doi/pdf/10.1145/3149371) (Count-Min Sketch) with Doorkeeper (Bloom filter) as their eviction policy to boost performance.

Now, let's examine Ristretto's advantages and disadvantages in detail.

Advantages:

- Ristretto offers relatively high throughput, especially considering that when it was created, Go lacked synchronization primitives optimized for caching. For instance, Ristretto has to rely on sharded maps to achieve higher throughput.
- Ristretto also delivers decent hit rates, though this claim comes with caveats we’ll discuss when covering its drawbacks.

Now, let’s begin the lengthy (unfortunately) discussion about Ristretto’s disadvantages.

- Before Ristretto, Go caches always required specifying the cache capacity as the maximum number of entries. One of Ristretto’s innovations was introducing `MaxCost` and per-entry **cost** values. This allowed users to indicate that entries could vary in size, enabling the cache to account for it. Typically, this feature is used to approximate the cache size in bytes. If you instead want the cache capacity to equal the number of entries, you can simply set `cost=1` for every `Set` operation. Quite convenient, isn’t it? The core issue is that in Ristretto v0.1.0 (see [commit](https://github.com/hypermodeinc/ristretto/commit/5f615bfe6a1cdc08a719d41946203a886d965ce6)), the `IgnoreInternalCost` option was introduced, and with it came a breaking change: Ristretto started automatically accounting for metadata byte size. This essentially broke **ALL** clients that specified cache capacity based on entry count. Why implement this change when they could have simply handled the byte overhead internally in Dgraph and Badger remains completely baffling to me. Ristretto's own [tests](https://github.com/hypermodeinc/ristretto/blob/main/stress_test.go#L58) remain broken because of this, consistently showing a hit rate of approximately 0.
- Upon closer examination of the eviction policy, it becomes apparent that Ristretto wasn't designed as a general-purpose cache. While TinyLFU performs well on frequency-skewed workloads (search, database page caches, and analytics), it may underperform in other scenarios. For example, benchmarks show suboptimal results on typical [OLTP](https://maypok86.github.io/otter/performance/hit-ratio/#oltp) workloads (very common in Go) or on this [Alibaba Cloud trace](https://github.com/maypok86/benchmarks/blob/main/simulator/results/alibabablock.png). The Ristretto authors further compounded this bias toward frequency-skewed workloads by adding a bloom filter to the eviction policy.
- Their count-min sketch implementation also contains known bugs that were [reported with fixes](https://github.com/hypermodeinc/ristretto/pull/326), yet none of these issues were ever addressed.
- To boost write operation performance, Ristretto employs a rather questionable hack: under high contention, `Set` operations may simply [fail to apply](https://github.com/hypermodeinc/ristretto/blob/main/cache.go#L355) to the cache. While this significantly improves insertion throughput, it can dramatically reduce hit rates during high contention periods. For those interested in analyzing hit rate degradation under contention, [this comment](https://github.com/maypok86/otter/issues/74#issuecomment-2972737418) might prove useful.
- Ristretto employs another questionable hack where it [stores hashes](http://github.com/hypermodeinc/ristretto/blob/main/store.go#L16) instead of actual keys and provides no collision handling whatsoever. While the probability of collisions might be low, users aren't given any choice about whether to accept this risk in exchange for memory savings. To me, this essentially amounts to intentionally introducing a small but real chance of panic in production code.
- Ristretto also lacks several useful features, such as: cache stampede protection (loading) and refreshing (asynchronous loading)

It appears Ristretto was primarily designed for Dgraph and Badger, as evidenced by numerous design choices made by its authors. Due to the issues outlined above, it has lost most of its major open-source adopters over the past two years.

## Theine

The library called [Theine](https://github.com/Yiling-J/theine-go) was created in 2023. I don't know the exact reasons behind its development, but its author quickly noticed that Ristretto had [issues](https://github.com/hypermodeinc/ristretto/issues/336) with hit rate, though received no response.

The key differentiator is that Theine implements some more efficient algorithms from Caffeine (Java), with its main advantage being the first Go implementation of [adaptive W-TinyLFU](https://dl.acm.org/doi/10.1145/3274808.3274816).

Theine never gained widespread popularity for some reason, yet it currently serves as the primary cache for [Vitess](https://github.com/vitessio/vitess) and is unquestionably one of the best caching libraries in Go.

Now let's discuss its advantages and disadvantages.

Advantages:

- After integrating [xsync.RBMutex](https://github.com/puzpuzpuz/xsync/blob/main/rbmutex.go) and [lossy buffers](https://github.com/maypok86/otter/blob/v1/internal/lossy/buffer.go) from Otter v1, Theine became one of the [fastest](https://maypok86.github.io/otter/performance/throughput/) caches in Go.
- Adaptive W-TinyLFU enables Theine to maintain a [high hit rate](https://maypok86.github.io/otter/performance/hit-ratio/) on **any** workload.
- Theine implements an excellent expiration policy based on a [Hierarchical Timer Wheel](https://ieeexplore.ieee.org/document/650142).
- Theine also provides cache stampede protection.

Disadvantages:

- The use of a sharded map prevents Theine from scaling across CPU cores as efficiently as desired.
- Lossy read buffers aren't ideal: they consume noticeable memory for small caches, and more critically, can significantly reduce hit rates under high contention. Again, those interested can refer to [this comment](https://github.com/maypok86/otter/issues/74#issuecomment-2972737418) mentioned during the Ristretto discussion.
- Theine's extensive feature set comes with a memory overhead - you pay for every feature, even unused ones.
- Lacks bulk loading capability.
- May permit dirty data (e.g., during concurrent invalidation and loading).
- Doesn't support refreshing (asynchronous loading).

Conclusion:

If you don't require bulk loading or refreshing, Theine remains a solid choice.

## Otter v1

I started developing [Otter v1](https://github.com/maypok86/otter) in mid 2023 when we discovered a severe hit rate degradation after attempting to update dependencies in a legacy service that used one of Ristretto's earliest versions.

The primary motivation came from several factors:

- The emergence of the [xsync](https://github.com/puzpuzpuz/xsync) library with its collection of efficient synchronization primitives.
- The recent publication about [S3-FIFO](https://dl.acm.org/doi/10.1145/3600006.3613147), which promised an excellent eviction policy that inherently solved throughput issues by design.
- The rather dismal state of Ristretto's development, possibly due to [financial troubles](https://news.ycombinator.com/item?id=30119165) at Dgraph Labs.

My experience with S3-FIFO was problematic from the start – it turned out that their proposed lock-free queue architecture performs rather poorly with even minor increases in write operations. This is likely because a significant portion of entries end up being hot keys, causing the policy to constantly perform O(n) reshuffling between queues.

The use of lock-free queues also introduces numerous complications when adding new features (though that’s a separate discussion). Perhaps someone will eventually prove me wrong by improving upon this approach, but for now, we’ll leave it at that.

It also turned out that the policy hadn't been tested on frequency-skewed workloads, and I found no mention of this in the paper. The most troubling discovery was S3-FIFO's vulnerability to scanning attacks - fortunately, I managed to fix this with relatively simple modifications.

After examining the codebases of Ristretto and Theine more closely, I discovered BP-Wrapper and W-TinyLFU. BP-Wrapper ultimately enabled me to surpass both Ristretto and Theine in terms of speed. I chose not to implement W-TinyLFU simply because I had already invested significant effort into refining S3-FIFO to meet my requirements.

When I began validating the correctness of the implementation again, several issues started surfacing. For instance, the eviction policy could process events out of order (which in Theine led to [memory leaks](https://github.com/centrifugal/centrifuge/pull/405#issuecomment-2288641665)). I managed to devise a solution, but it was particularly frustrating since the problem wasn't at all obvious.

But here's the most fascinating part. When I examined Caffeine's source code, I discovered these problems had already been solved in nearly identical ways... While I do take some pride in having independently arrived at similar solutions, just spending a little time reading code in another language would have saved me weeks of effort...

Alright, let's move on to the advantages and disadvantages.

Advantages:

- Long-standing unbeatable [throughput](https://maypok86.github.io/otter/performance/throughput/). Much of the credit goes to [xsync.Map](https://github.com/puzpuzpuz/xsync/blob/main/map.go).
- High hit rate across most workloads.
- One of the smallest [memory overheads](https://maypok86.github.io/otter/performance/memory-consumption/) among on-heap caches.
- Memory is only allocated for features you actually use.

Disadvantages:

- Otter v1 has one of the most advanced APIs, but many design choices turned out to be flawed. This was largely due to my lack of experience in cache development and not fully understanding user needs.
- As mentioned in the Theine discussion, lossy read buffers aren’t ideal: They consume noticeable memory for small caches and, more critically, can significantly reduce hit rate under high contention. For details, see this [comment](https://github.com/maypok86/otter/issues/74#issuecomment-2972737418) from the Ristretto discussion.
- On frequency-skewed workloads, the hit rate may be noticeably worse than with TinyLFU (Ristretto) and W-TinyLFU (Theine).
- Lacks several useful features, such as: cache stampede protection (loading) and refreshing (asynchronous loading).

Conclusion:

Like Theine, Otter v1 is a strong choice if you don’t need loading or refreshing features and your workload is not frequency-skewed.

## Sturdyc

[Sturdyc](https://github.com/viccon/sturdyc) was created in 2024 and appears to be the first Go caching library to implement advanced features like loading, refreshing, etc.

I wouldn't call Sturdyc very efficient, but I believe it's worth examining due to its implementation of many previously missing features. Let's discuss its advantages and disadvantages.

Advantages:

- Provides cache stampede protection.
- Supports refreshing.
- Capable of bulk loading and refreshing.

Disadvantages:

- Lacks a good eviction policy. Sturdyc's eviction policy has a time complexity of O(n) and often has a [worse hit rate than LRU](https://maypok86.github.io/otter/performance/hit-ratio/).
- Lacks a good expiration policy.
- [Performance lags](https://maypok86.github.io/otter/performance/throughput/) behind Ristretto, Theine and Otter v1.
- Only supports strings as keys (no generic key support).
- Fails to prevent duplicate requests - cannot deduplicate calls [when some keys are batched while others aren't](https://github.com/viccon/sturdyc/blob/main/cache.go#L59).
- May permit dirty data (e.g., during concurrent invalidation and loading). See [example](https://maypok86.github.io/otter/user-guide/v2/examples/#concurrent-loading-and-invalidation).
- Overexposes internal implementation details (though this could be considered subjective based on preferences).

Conclusion:

Sturdyc could be a good choice if its particular advantages and disadvantages align with your requirements.

## Otter v2

Otter v2 was conceived with an ambitious vision – to create a caching library that would:

- Implement all previously discussed features.
- Deliver high throughput.
- Maintain excellent hit rates across all workloads.
- Offer an extensible, intuitive API. For example, finally enabling TTL resets on every access (read/write), not just on every write (create/update) like other caches. This is critical (for example) for caching sessions, API tokens, etc.
- Avoid all the aforementioned issues – and there are many! Each existing library has its own unique shortcomings.

After much work, Otter v2 ([godoc](https://pkg.go.dev/github.com/maypok86/otter/v2), [user guide](https://maypok86.github.io/otter/user-guide/v2/getting-started/), [design](https://maypok86.github.io/otter/performance/design/)) has been released – achieving every one of these goals.

Key Improvements:

- Completely rethought API for greater flexibility.
- Added [loading](https://maypok86.github.io/otter/user-guide/v2/features/loading/) and [refreshing](https://maypok86.github.io/otter/user-guide/v2/features/refresh/) features.
- Added [entry pinning](https://maypok86.github.io/otter/user-guide/v2/features/eviction/#pinning-entries).
- Added Compute* methods
- Replaced eviction policy with adaptive W-TinyLFU, enabling Otter to achieve one of the highest hit rates across **all** workloads.
- Added HashDoS protection against potential attacks.
- The task scheduling mechanism has been completely reworked, allowing users to manage it themselves when needed.
- Added more efficient write buffer.
- Added auto-configurable lossy read buffer.
- Optimized hash table.

It's worth noting that Otter v2 has effectively materialized as a Go adaptation of Caffeine, since I concurred with most of its architectural choices, with only rare instances where they seemed questionable or inapplicable to Go's ecosystem.

The main drawback at the moment is arguably that Otter v2 has seen far less real-world use than other caches, despite passing tests that others fail.

## Acknowledgment

I would like to sincerely thank [Benjamin Manes](https://github.com/ben-manes) for creating Caffeine, for being highly responsive in chats, and for actively participating in discussions. His extremely valuable comments have taught me a great deal. :heart:

Special thanks also go to [Hyeonho Kim](https://github.com/proost) for his highly insightful suggestions that profoundly influenced the final design. :heart:

## Conclusion

Today, we've explored several popular caching libraries. I hope this article helps you make an informed decision when choosing a caching library for your projects.
