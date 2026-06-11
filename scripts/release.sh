#!/usr/bin/env bash
# scripts/release.sh — build and package an aggrega CLI release locally.
#
# Usage:
#   scripts/release.sh --version X.Y.Z
#
# Flow (local, unpublished):
#   1. Verify the working tree is clean.
#   2. Verify the local tag vX.Y.Z exists and points at HEAD. The
#      operator creates it first (git tag vX.Y.Z) — the tag keeps the
#      v prefix (Go module resolution and GoReleaser both expect
#      vX.Y.Z tags); every stamped and displayed version string is the
#      v-less X.Y.Z.
#   3. Drive `goreleaser release --skip=publish --clean`: cross-build
#      the linux/darwin/windows × amd64/arm64 matrix, stamp
#      -X main.version=X.Y.Z -X main.commit=<sha> (v-less), package
#      tar.gz/zip + deb/rpm into dist/.
#   4. Print the tag and the artifact list.
#
# This script NEVER tags and NEVER pushes. Creating the local tag,
# reviewing dist/, pushing the tag, and publishing a release are the
# operator's explicit actions.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

VERSION=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      shift
      VERSION="${1:-}"
      ;;
    -h|--help)
      sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "✗ unknown argument: $1" >&2
      echo "  usage: scripts/release.sh --version X.Y.Z" >&2
      exit 2
      ;;
  esac
  shift
done

if [ -z "$VERSION" ]; then
  echo "✗ missing --version" >&2
  echo "  usage: scripts/release.sh --version X.Y.Z" >&2
  exit 2
fi

# The version argument is the v-less string; reject a v prefix rather
# than silently fixing it, so the operator's input and the stamped
# string never diverge.
if ! echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$'; then
  echo "✗ --version must be a v-less semver (X.Y.Z), got: $VERSION" >&2
  exit 2
fi

TAG="v$VERSION"

if ! command -v goreleaser >/dev/null 2>&1; then
  echo "✗ goreleaser not found on PATH" >&2
  echo "  install: go install github.com/goreleaser/goreleaser/v2@latest" >&2
  exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
  echo "✗ working tree not clean — commit or stash before releasing" >&2
  git status --short >&2
  exit 1
fi

if ! git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  echo "✗ local tag $TAG does not exist" >&2
  echo "  create it on the release commit first: git tag $TAG" >&2
  echo "  (the tag stays local; this script never pushes)" >&2
  exit 1
fi

if [ "$(git rev-parse "$TAG^{commit}")" != "$(git rev-parse HEAD)" ]; then
  echo "✗ tag $TAG does not point at HEAD — check out the release commit" >&2
  exit 1
fi

echo "==> goreleaser release --skip=publish --clean ($TAG → version $VERSION)"
goreleaser release --skip=publish --clean

echo ""
echo "✓ release artifacts built (nothing tagged, nothing pushed, nothing published)"
echo ""
echo "  proposed tag: $TAG (already created locally; push is the operator's action)"
echo "  artifacts in dist/:"
ls -1 dist/ | sed 's/^/    /'
