#!/usr/bin/env bash
# Wrapper that builds the mutation-baseline binary and forwards
# arguments to it. Provides a stable invocation path so the operator
# can allowlist this script rather than re-authorising every direct
# `go run` of the binary.
#
# The binary copies the aggrega-cli source tree into a scratch directory
# under $TMPDIR before applying any mutation; the original source tree
# is never modified. Run `scripts/mutation-baseline.sh --help` (or
# pass --smoke-only for a fast classification-mechanism check) to see
# all flags.
#
# Usage:
#   scripts/mutation-baseline.sh                       # smoke + full baseline; report to stdout
#   scripts/mutation-baseline.sh --smoke-only          # smoke check only
#   scripts/mutation-baseline.sh --packages pkg/labels # single-package run
#   scripts/mutation-baseline.sh --output baseline.md  # write report to file

set -euo pipefail

# Resolve script location to the aggrega-cli root (one level up).
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cli_root="$(cd "$script_dir/.." && pwd)"
cd "$cli_root"

mkdir -p bin
go build -o bin/mutation-baseline ./cmd/mutation-baseline
exec ./bin/mutation-baseline "$@"
