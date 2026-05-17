#!/usr/bin/env bash
# aggrega-cli/scripts/verify.sh
set -euo pipefail
cd "$(dirname "$0")/.."

echo "=== build ==="
go build ./...

echo "=== vet ==="
go vet ./...

echo "=== test ==="
go test ./... -count=1 -v

echo "=== coverage gate ==="
scripts/coverage-gate.sh

echo "=== all green ==="
