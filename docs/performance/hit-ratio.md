# Hit ratio

## Traditional traces

### Zipf

![zipf](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/zipf.png)

### S3

This trace is described as "disk read accesses initiated by a large commercial search engine in response to various web search requests.".

![s3](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/s3.png)

### DS1

This trace is described as "a database server running at a commercial site running an ERP application on top of a commercial database.".

![ds1](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/ds1.png)

### P3

The trace P3 was collected from workstations running Windows NT by using Vtrace
which captures disk operations through the use of device
filters.

![p3](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/p3.png)

### P8

The trace P8 was collected from workstations running Windows NT by using Vtrace
which captures disk operations through the use of device
filters.

![p8](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/p8.png)

### LOOP

This trace demonstrates a looping access pattern.

![loop](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/loop.png)

### OLTP

This trace is described as "references to a CODASYL database for a one-hour period.".

![oltp](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/oltp.png)

## Modern traces

### WikiCDN

This [trace](https://wikitech.wikimedia.org/wiki/Analytics/Data_Lake/Traffic/Caching) was collected from Wikimedia's CDN system in 2019.

![wikicdn](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/wikicdn.png)

### AlibabaBlock

This [trace](https://github.com/alibaba/block-traces) was collected from a cluster in production of the elastic block service of Alibaba Cloud (i.e. storage for virtual disks) in 2020.

![alibabablock](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/alibabablock.png)

### Twitter

This [trace](https://github.com/twitter/cache-trace) was collected from Twitter's in-memory caching ([Twemcache](https://github.com/twitter/twemcache)/[Pelikan](https://github.com/twitter/pelikan)) clusters in 2020.

![twitter](https://raw.githubusercontent.com/maypok86/benchmarks/main/simulator/results/twitter.png)

## Conclusion

`S3-FIFO` (otter) is inferior to `W-TinyLFU` (theine) on lfu friendly traces (databases, search, analytics) and has a greater or equal hit ratio on web traces. But it is still worth recognizing that `W-TinyLFU` is superior to `S3-FIFO` as a general-purpose eviction policy.

In summary, we have that `S3-FIFO` is competitive with `W-TinyLFU` and `ARC`. Also, it provides a substantial improvement to `LRU` across a variety of traces.
