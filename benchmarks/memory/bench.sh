#!/bin/bash

set -eo pipefail

caches=(
  "otter"
  "theine"
  "ristretto"
  "sturdyc"
  "ccache"
  "gcache"
  "ttlcache"
  "golang-lru"
)

capacities=(1000 10000 25000 100000 1000000)

result_path="./results/memory.txt"

echo -n "" > "$result_path"

# Every measurement runs in a fresh process, so that caches don't share
# a heap.
go build -o ./results/memory.bin .
for capacity in "${capacities[@]}"
do
  for cache in "${caches[@]}"
  do
    ./results/memory.bin "$cache" "$capacity" | tee -a "$result_path"
  done
done
rm ./results/memory.bin

go run ./cmd/main.go "$result_path"
