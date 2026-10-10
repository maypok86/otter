#!/bin/bash

set -eo pipefail

# The number of runs. benchstat needs at least 6 to report a confidence
# interval, and the charts show the median.
count="${COUNT:-8}"
result_path="./results/throughput.txt"

echo -n "" > "$result_path"

# Each round runs every cache once, so a slowdown of the machine during
# the run is spread over all caches instead of hitting one of them.
go test -c -o ./results/throughput.test .
for i in $(seq "$count"); do
  ./results/throughput.test -test.run='^$' -test.cpu=8 -test.bench=. -test.timeout=0 | tee -a "$result_path"
done
rm ./results/throughput.test

go tool benchstat -col /cache -row /reads "$result_path"
go run ./cmd/main.go "$result_path"
