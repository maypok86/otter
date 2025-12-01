#!/usr/bin/env bash

set -euo pipefail

echo 'mode: atomic' > coverage.txt
GOEXPERIMENT=synctest go test -covermode=atomic -coverprofile=coverage.txt.tmp -coverpkg=./... -v -race ./...
cat coverage.txt.tmp | grep -v -E "/generated/|/cmd/" > coverage.txt
rm coverage.txt.tmp
COVERAGE=$(go tool cover -func=coverage.txt | tail -1 | grep -Eo '[0-9]+\.[0-9]')
echo "coverage: $COVERAGE% of statements"
