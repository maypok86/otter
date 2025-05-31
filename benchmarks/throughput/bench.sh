#!/bin/bash

set -e

result_path="./results/throughput.txt"

echo -n "" > "$result_path"

go test -run='^$' -cpu=8 -bench . -timeout=0 | tee "$result_path"
go run ./cmd/main.go "$result_path"
