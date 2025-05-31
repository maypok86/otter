#!/bin/bash

set -e

caches=(
  "otter"
  "theine"
  "ristretto"
  "ccache"
  "gcache"
  "ttlcache"
  "golang-lru"
)

capacities=(1000 10000 25000 100000 1000000)

result_path="./results/memory.txt"

echo -n "" > "$result_path"

for capacity in "${capacities[@]}"
do
  for cache in "${caches[@]}"
  do
    go run main.go "$cache" "$capacity" >> "$result_path"
  done
done

go run ./cmd/main.go "$result_path"
