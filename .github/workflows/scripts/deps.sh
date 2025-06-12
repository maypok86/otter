#!/bin/bash

set -e

(which golangci-lint > /dev/null) || curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh |
bash -s -- -b "$(go env GOPATH)"/bin v2.1.6

go install github.com/daixiang0/gci@latest
GO111MODULE=on go install mvdan.cc/gofumpt@latest

go mod download
