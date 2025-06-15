# Design

This page outlines key implementation details of Otter’s internals.

## Hash table

A core advantage of Otter lies in its optimized hash table implementation. Much of Otter’s performance stems from leveraging [xsync.Map](https://github.com/puzpuzpuz/xsync/blob/main/map.go), which provides superior efficiency for concurrent access patterns.

## Concurrency

The page replacement algorithms are kept eventually consistent with the map. An update to the map and recording of reads may not be immediately reflected in the policy's data structures.
These structures are guarded by a lock, and operations are [applied in batches](https://www.researchgate.net/publication/220966845_BP-Wrapper_A_System_Framework_Making_Any_Replacement_Algorithms_Almost_Lock_Contention_Free) to avoid lock contention.
The penalty of applying the batches is spread across goroutines, so that the amortized cost is slightly higher than performing just the `xsync.Map` operation.

### Event Tracking

Read/write operations are recorded in buffers, which drain:

- Immediately after writes
- When read buffers reach capacity
- Asynchronously to minimize latency (managed via a state machine)

Read buffers may reject entries when contended or full. While strict policy ordering isn’t guaranteed under concurrent access, single-threaded execution maintains observable consistency.

### State Management

Entries transition through three states:

- **Alive**: Present in both hash table and page replacement policy
- **Retired**: Deleted from hash table, pending policy deletion
- **Dead**: Absent from both structures

### Read buffer

Typical caches lock on each operation to safely reorder the entry in the access queue. An alternative is to store each reorder operation in a buffer and apply the changes in batches. This could be viewed as a write-ahead log for the page replacement policy. When the buffer is full an attempt is made to acquire the lock and perform the pending operations, but if it is already held then the goroutine can return immediately.

The read buffer is implemented as a striped ring buffer. The stripes are used to reduce contention and a stripe is selected by a thread specific hash (using `sync.Pool`). The ring buffer is a fixed size array, making it efficient and minimizes garbage collection overhead. The number of stripes can grow dynamically based on a contention detecting algorithm.

### Write buffer

Similar to the read buffer, this buffer is used to replay write events. The read buffer is allowed to be lossy, as those events are used for optimizing the hit rate of the eviction policy. Writes cannot be lost, so it must be implemented as an efficient bounded queue. Due to the priority of draining the write buffer whenever populated, it typically stays empty or very small.

The buffer is implemented as a growable circular array that resizes up to a maximum. When resizing a new array is allocated and produced into. The previous array includes a forwarding link for the consumer to traverse, which then allows the old array to be deallocated. By using this chunking mechanism the buffer has a small initial size, low cost for reading and writing, and creates minimal garbage. When the buffer is full and cannot be grown, producers continuously spin retrying and attempting to schedule the maintenance work, before yielding after a short duration. This allows the consumer goroutine to be prioritized and drain the buffer by replaying the writes on the eviction policy.

In scenarios where the writing goroutines cannot make progress then they attempt to provide assistance by performing the eviction work directly. This can resolve cases where the maintenance task is scheduled but not running. That might occur due to all the executor's goroutines being busy (perhaps writing into this cache), the write rate greatly exceeds the consuming rate, priority inversion, or if the executor silently discarded the maintenance task.

## Expiration (Time-based eviction)

Expiration is implemented in O(1) time complexity (using [hierarchical timer wheel](http://www.cs.columbia.edu/~nahum/w6998/papers/ton97-timing-wheels.pdf)).
The expiration policy uses hashing and cascading in a manner that amortizes the penalty of sorting to achieve a similar algorithmic cost.

The expiration updates are applied in a best effort fashion.
The reordering of expiration may be discarded by the read buffer if it is full or contended.

## Eviction (Size-based eviction)

Otter uses the [adaptive W-TinyLfu](https://dl.acm.org/citation.cfm?id=3274816) policy to provide a [near optimal hit rate](https://maypok86.github.io/otter/performance/hit-ratio/). The access queue is split into two spaces: an admission window that evicts to the main spaces if accepted by the TinyLfu policy. TinyLfu estimates the frequency of the window's victim and the main's victim, choosing to retain the entry with the highest historic usage. The frequency counts are stored in a 4-bit CountMinSketch, which requires 8 bytes per cache entry to be accurate. This configuration enables the cache to evict based on frequency and recency in O(1) time and with a small footprint.

### Adaptivity

The size of the admission window and main space are dynamically determined based on the workload characteristics. A large window is favored if recency-biased and a smaller one by frequency-biased. Otter uses hill climbing to sample the hit rate, adjust, and configure itself to the optimal balance.

### Fastpath

When the cache is below 50% of its maximum capacity the eviction policy is not yet fully enabled. The frequency sketch is not initialized to reduce the memory footprint, as the cache might be given an artificially high threshold. Access is not recorded, unless required by another feature, to avoid contention on the read buffer and replaying the accesses when draining.

### HashDoS protection

When the keys have the same hash code, or hash to the same locations, the collisions could be exploited to degrade performance. An attack on TinyLFU is to use a collision to artificially raise the estimated frequency of the eviction policy's victim. This results in all new arrivals being rejected by the frequency filter, thereby making the cache ineffective. A solution is to introduce a small amount of jitter so that the decision is non-deterministic. This is done by randomly admitting ~1% of the rejected candidates that have a moderate frequency.

## Code generation

There are many different configuration options resulting in most fields being required only if a certain subset of features is enabled. If all fields are present by default, this can be wasteful by increasing per entry overhead. By code generating the optimal entry implementation, the runtime memory overhead is reduced at the cost of a larger binary on disk.
