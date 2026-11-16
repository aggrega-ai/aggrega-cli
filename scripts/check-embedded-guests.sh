#!/usr/bin/env bash
# aggrega-cli/scripts/check-embedded-guests.sh
#
# Fails when any core guest — under internal/wasm/embedded/plugins/<name>.wasm.gz
# for a plugin the binary embeds, under
# internal/wasm/embedded/published-only/<name>.wasm.gz for one it does not — is
# missing, or older than the newest source file it is built from.
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
# library are excluded (they cannot go stale relative to a rebuild). Each Rust
# guest is checked against every file under its crate directory and the extra
# source roots its manifest row names, excluding the cargo build directory and
# every file the tree's own .gitignore files ignore. Those are what a build writes
# beside the sources — the lockfile cargo creates for a crate that deliberately
# commits none, an editor's temporary — and none is an input to the guest, so one
# written after the guest (by a later cargo build in that crate, say) does not
# make it stale. A file git tracks still counts, and so does a new file not yet
# added to git, since git has not been told to ignore it. Only the .gitignore
# files committed in the tree decide what is ignored, never a clone's own
# exclude file or a user's global one, so every checkout of a commit gets the
# same answer. Outside a git work tree nothing is known to be ignored, and every
# file under a root counts. A root that does not exist is reported, since the
# guest cannot be built without it.
#
# For a plugin the binary does not embed, the binary carries the content digest
# of its guest instead of the guest (internal/wasm/embedded/published-selection/
# <name>.digest), so that guest is also stale when the digest is missing or is
# not the digest of the guest on disk. This half compares the recorded digest
# with the bytes beside it, never with a rebuild, so the provenance stamp cannot
# make it report false staleness.
#
# Exit 0 when every guest is fresh; exit 1 (naming each stale or missing guest
# and the rebuild command) otherwise. Deterministic and offline.
set -euo pipefail
cd "$(dirname "$0")/.."

# The core-plugin list's one reader: core_plugin_rows and the artifact directories.
. scripts/lib/core-plugins.sh
rows="$(core_plugin_rows)" || exit 1
REBUILD_HINT="rebuild with: go generate ./internal/wasm/embedded/... && go build"

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

# check_go_guest NAME PKG DIR — flags NAME when its artifact DIR/NAME.wasm.gz is
# missing or older than any Go source file compiled into the guest built from PKG.
check_go_guest() {
  local name="$1" pkg="$2" dir="$3"
  local artifact="$dir/$name.wasm.gz"
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

# is_guest_source ROOT FILE — true unless FILE, found under ROOT, is a file the
# tree's own .gitignore files ignore. git is asked in ROOT, so a root in another
# repository (a kit beside this module) is judged by that repository's index and
# ignore files. ls-files answers 1 exactly when the path is neither tracked nor
# an untracked file left unignored by --exclude-per-directory=.gitignore, which,
# unlike --exclude-standard, reads neither the clone's .git/info/exclude nor the
# user's global excludes. Any other failure — no git, or no work tree here —
# leaves FILE counted, so a question git cannot answer never hides a source.
is_guest_source() {
  local root="$1" file="$2" rc=0
  git -C "$root" --literal-pathspecs ls-files --error-unmatch --cached --others \
    --exclude-per-directory=.gitignore -- "${file#"$root"/}" > /dev/null 2>&1 || rc=$?
  [ "$rc" -ne 1 ]
}

# check_rust_guest NAME DIR ROOT... — flags NAME when its artifact
# DIR/NAME.wasm.gz is missing or older than any source file under the given
# roots: every file there but those under a cargo build directory and those
# is_guest_source rejects. The files newer than the artifact are found first, so
# git is asked only about those, and in name order, so the one reported is the
# same on every run.
check_rust_guest() {
  local name="$1" dir="$2"; shift 2
  local roots=("$@")
  local artifact="$dir/$name.wasm.gz"
  if [ ! -f "$artifact" ]; then
    echo "STALE  $name — missing artifact $artifact"
    stale+=("$name")
    return
  fi
  local root file newer=""
  local -a candidates=()
  for root in "${roots[@]}"; do
    root="${root%/}"
    if [ ! -d "$root" ]; then
      echo "ERROR  $name — source root $root does not exist" >&2
      stale+=("$name")
      return
    fi
    mapfile -d '' -t candidates < <(find "$root" -type f -not -path '*/target/*' -newer "$artifact" -print0 | LC_ALL=C sort -z)
    if ! wait "$!"; then
      echo "ERROR  $name — could not read every file under the source root $root" >&2
      stale+=("$name")
      return
    fi
    for file in "${candidates[@]}"; do
      if is_guest_source "$root" "$file"; then
        newer="$file"
        break 2
      fi
    done
  done
  if [ -n "$newer" ]; then
    echo "STALE  $name — source newer than artifact: $newer"
    stale+=("$name")
  else
    echo "fresh  $name"
  fi
}

# check_carried_digest NAME DIR — flags NAME, a plugin the binary does not
# embed, when the content digest the binary carries for it is missing or is not
# the digest of its decompressed guest DIR/NAME.wasm.gz. The digest is spelled
# as the guest build writes it: sha256: and the lowercase hex sha256sum prints.
check_carried_digest() {
  local name="$1" dir="$2"
  local carried="$CORE_PLUGIN_SELECTION_DIR/$name.digest" artifact="$dir/$name.wasm.gz"
  local sum want got
  if [ ! -f "$carried" ]; then
    echo "STALE  $name — missing carried guest digest $carried"
    stale+=("$name")
    return
  fi
  sum="$(gunzip -c "$artifact" | sha256sum)"
  want="sha256:${sum%% *}"
  got="$(tr -d '[:space:]' < "$carried")"
  if [ "$got" != "$want" ]; then
    echo "STALE  $name — carried guest digest $got is not the guest's $want"
    stale+=("$name")
  fi
}

# Walk the core-plugin list — the one declarative source of the core plugins,
# where each guest's source lives and whether the binary embeds it — and check
# every entry against its artifact, in its row's directory. A go guest is checked
# against its compiled Go sources; a rust guest against its crate directory, whose
# lockfile pins the kit crates it takes from the registry, plus any extra source
# roots (the crates in this module it depends on by path). A row the binary does not embed
# whose guest is fresh is then checked against the digest the binary carries for
# it. Reading the list here keeps this guard in lockstep with the build script
# and the registry table.
while IFS=$'\x1f' read -r socket name sourcePath toolchain extraRoots embed dir; do
  [ -n "$socket" ] || continue
  flagged="${#stale[@]}"
  case "$toolchain" in
    go)
      check_go_guest "$name" "$sourcePath" "$dir"
      ;;
    rust)
      roots=("$sourcePath")
      if [ -n "${extraRoots:-}" ]; then
        IFS=',' read -ra extra <<< "$extraRoots"
        roots+=("${extra[@]}")
      fi
      check_rust_guest "$name" "$dir" "${roots[@]}"
      ;;
    *)
      echo "check-embedded-guests: unknown toolchain '$toolchain' for $name" >&2
      exit 1
      ;;
  esac
  if [ "$embed" = no ] && [ "${#stale[@]}" -eq "$flagged" ]; then
    check_carried_digest "$name" "$dir"
  fi
done <<< "$rows"

if [ "${#stale[@]}" -gt 0 ]; then
  echo
  echo "core guests stale or missing: ${stale[*]}"
  echo "$REBUILD_HINT"
  exit 1
fi

echo
echo "all core guests fresh"
