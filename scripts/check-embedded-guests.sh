#!/usr/bin/env bash
# aggrega-cli/scripts/check-embedded-guests.sh
#
# Fails when any embedded provider guest under
# internal/wasm/embedded/plugins/<name>.wasm.gz is missing, or older than the
# newest source file it is built from.
#
# The embedded guests are reproducible build outputs regenerated from guest
# source (see the build-embedded-*.sh scripts, run by
# `go generate ./internal/wasm/embedded/...`), not tracked files. A plain
# `go build` never regenerates them: it embeds whatever .wasm.gz already sits on
# disk from an earlier build, which can predate a later change to the guest or to
# the shared render core the guest reuses — silently producing a binary with
# stale provider behavior. This guard makes that state detectable: run it after a
# build to assert the embedded bytes are current.
#
# Freshness is measured by modification time, not content digest. The
# build-embedded scripts stamp a build-provenance version derived from
# `git describe --dirty`, so a guest's bytes differ on any dirty tree even when
# no source changed; a digest comparison would report false staleness there. An
# artifact-older-than-its-source mtime test is immune to the provenance stamp.
#
# The Go provider guests are checked against the full set of locally-editable Go
# files they compile — the provider package plus the render core and the shared
# packages it imports, discovered through `go list` against the guest's wasip1
# build — so a change to the shared render core is detected, not only a change to
# the thin provider entrypoint. The immutable module cache and the standard
# library are excluded (they cannot go stale relative to a rebuild). The Rust
# publishing-linter guest is checked against its crate source and the local
# plugin SDK crate it depends on, excluding the cargo build directory.
#
# Exit 0 when every guest is fresh; exit 1 (naming each stale or missing guest
# and the rebuild command) otherwise. Deterministic and offline.
set -euo pipefail
cd "$(dirname "$0")/.."

MANIFEST="internal/wasm/embedded/embedded-plugins.tsv"
EMBED_DIR="internal/wasm/embedded/plugins"
[ -f "$MANIFEST" ] || { echo "check-embedded-guests: manifest not found: $MANIFEST" >&2; exit 1; }
REBUILD_HINT="rebuild with: go generate ./internal/wasm/embedded/... && go build   (or ./scripts/build.sh from the umbrella root, which always regenerates)"

MODCACHE="$(go env GOMODCACHE)"
GOROOT_DIR="$(go env GOROOT)"

declare -a stale=()

# list_go_sources PKG — prints the locally-editable Go source files compiled
# into the wasip1 guest built from PKG, one per line, excluding the immutable
# module cache and the standard library. GOWORK=off mirrors the build-embedded
# recipe: the go.mod replace directives then resolve the sibling modules from
# their local working copies, offline.
list_go_sources() {
  local pkg="$1"
  GOWORK=off GOOS=wasip1 GOARCH=wasm go list -deps \
    -f '{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}' "$pkg" \
    | grep -v "^$MODCACHE" | grep -v "^$GOROOT_DIR"
}

# check_go_guest NAME PKG — flags NAME when its artifact is missing or older than
# any Go source file compiled into the guest built from PKG.
check_go_guest() {
  local name="$1" pkg="$2"
  local artifact="$EMBED_DIR/$name.wasm.gz"
  if [ ! -f "$artifact" ]; then
    echo "STALE  $name — missing artifact $artifact"
    stale+=("$name")
    return
  fi
  local files=()
  mapfile -t files < <(list_go_sources "$pkg")
  if [ "${#files[@]}" -eq 0 ]; then
    echo "ERROR  $name — no guest source resolved from $pkg (go list failed)" >&2
    stale+=("$name")
    return
  fi
  local newer
  newer="$(find "${files[@]}" -newer "$artifact" -print -quit 2>/dev/null)"
  if [ -n "$newer" ]; then
    echo "STALE  $name — source newer than artifact: $newer"
    stale+=("$name")
  else
    echo "fresh  $name"
  fi
}

# check_rust_guest NAME ROOT... — flags NAME when its artifact is missing or
# older than any source file under the given roots, excluding cargo build
# directories.
check_rust_guest() {
  local name="$1"; shift
  local roots=("$@")
  local artifact="$EMBED_DIR/$name.wasm.gz"
  if [ ! -f "$artifact" ]; then
    echo "STALE  $name — missing artifact $artifact"
    stale+=("$name")
    return
  fi
  local newer
  newer="$(find "${roots[@]}" -type f -not -path '*/target/*' -newer "$artifact" -print -quit 2>/dev/null)"
  if [ -n "$newer" ]; then
    echo "STALE  $name — source newer than artifact: $newer"
    stale+=("$name")
  else
    echo "fresh  $name"
  fi
}

# Walk the embedded-plugin manifest — the one declarative source of which plugins
# ship inside the binary and where each guest's source lives — and check every
# entry against its artifact. A go guest is checked against its compiled Go
# sources; a rust guest against its crate directory plus any extra source roots
# (its local SDK crate). Reading the manifest here keeps this guard in lockstep
# with the build script and the registry table.
while IFS=$'\t' read -r socket name sourcePath toolchain extraRoots; do
  case "$socket" in ''|'#'*) continue;; esac
  case "$toolchain" in
    go)
      check_go_guest "$name" "$sourcePath"
      ;;
    rust)
      roots=("$sourcePath")
      if [ -n "${extraRoots:-}" ]; then
        IFS=',' read -ra extra <<< "$extraRoots"
        roots+=("${extra[@]}")
      fi
      check_rust_guest "$name" "${roots[@]}"
      ;;
    *)
      echo "check-embedded-guests: unknown toolchain '$toolchain' for $name" >&2
      exit 1
      ;;
  esac
done < "$MANIFEST"

if [ "${#stale[@]}" -gt 0 ]; then
  echo
  echo "embedded guests stale or missing: ${stale[*]}"
  echo "$REBUILD_HINT"
  exit 1
fi

echo
echo "all embedded guests fresh"
