#!/bin/sh
# Run the agent from dist/ (build first with scripts/build.sh).
set -eu
cd "$(dirname "$0")/.."
exec ./dist/meshcraft-agent --data "${MESHCRAFT_DATA:-$HOME/.meshcraft}"
