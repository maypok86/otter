#!/bin/bash

set -eo pipefail

# The number of runs. benchstat needs at least 6 to report a confidence
# interval, and the charts show the median.
count="${COUNT:-8}"
# The classic benchmark also runs on these core counts to show how the
# caches scale. The other benchmarks run on 8 cores.
cpus="1,2,4,8"
ncpu="$(getconf _NPROCESSORS_ONLN)"
if [ "$ncpu" -gt 8 ]; then
  cpus="$cpus,$ncpu"
fi
cpus="${CPUS:-$cpus}"
result_path="./results/throughput.txt"

echo -n "" > "$result_path"

# Each round runs every cache once, so a slowdown of the machine during
# the run is spread over all caches instead of hitting one of them.
go test -c -o ./results/throughput.test .
for i in $(seq "$count"); do
  ./results/throughput.test -test.run='^$' -test.cpu="$cpus" -test.bench='^BenchmarkCache$' -test.timeout=0 | tee -a "$result_path"
  ./results/throughput.test -test.run='^$' -test.cpu=8 -test.bench='^Benchmark(Eviction|Expiration|Large|Loading)$' -test.timeout=0 | tee -a "$result_path"
done
rm ./results/throughput.test

go tool benchstat -filter '.unit:(ops/s OR hit%)' -col /cache "$result_path"
go run ./cmd/main.go "$result_path"
