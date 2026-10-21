#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
unformatted=$(gofmt -l cmd internal tests)
if [ -n "$unformatted" ]; then echo "gofmt needed on: $unformatted"; exit 1; fi
go vet ./...
go test ./...
