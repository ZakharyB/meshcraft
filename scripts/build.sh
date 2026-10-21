#!/bin/sh
# Build all MeshCraft binaries into dist/ with their canonical names.
set -eu
cd "$(dirname "$0")/.."
mkdir -p dist
go build -o dist/meshcraft          ./cmd/meshcraft
go build -o dist/meshcraft-agent    ./cmd/peer-agent
go build -o dist/meshcraft-coordinator ./cmd/coordinator
go build -o dist/meshcraft-gateway  ./cmd/gateway
echo "built: dist/meshcraft dist/meshcraft-agent dist/meshcraft-coordinator dist/meshcraft-gateway"
