#!/usr/bin/env bash
# aggrega-cli/scripts/lib/third-party-inputs.sh
#
# The one definition of what the committed third-party attribution is derived
# from, and of the digest over it. SOURCED (never executed) by the generator,
# which records the digest in the SBOM, and by the check, which recomputes it.
#
# The inputs, each read from the aggrega-cli module root:
#
#   go.mod, go.sum             the Go module graph, without the lines naming a
#                              first-party module
#   <source>/Cargo.lock        one per embedded Rust row, in list order, without
#                              its first-party [[package]] blocks and without the
#                              first-party entries of the other blocks'
#                              `dependencies` lists
#   the embedded rows          socket, name, source path and toolchain of every
#                              row the binary embeds, in list order; a row that
#                              is not embedded is not an input, so adding one
#                              changes nothing
#   scripts/gen-third-party-licenses.sh and this file
#                              the rules that turn the rest into attribution
#
# First-party code is Aggrega's own and is not attributed, so its version pins
# are not inputs: this file is the one definition of what is first-party, for
# the digest and for the generator, which reads the definitions below. A Go
# module is first-party when its path starts with THIRD_PARTY_FIRST_PARTY_GO_PREFIX;
# a crate is when its name is one of THIRD_PARTY_FIRST_PARTY_CRATES (crates.io has
# no namespaces, so the names are exact, not a prefix).
#
# The digest is sha256:<64 hex> over a serialization with one line per input:
# its label (a path, or `row` and the four row fields), a tab, and the sha256 of
# its content (of the row's fields for a row). A reordered, renamed, added or
# removed input therefore changes it. Nothing here runs go or cargo: it only
# hashes, so it answers in a moment.
#
# Sourcing requires scripts/lib/core-plugins.sh to be sourced first, and defines:
#
#   THIRD_PARTY_DIGEST_PREFIX   the phrase that introduces the digest in the SBOM
#   THIRD_PARTY_FIRST_PARTY_GO_PREFIX     path prefix of a first-party Go module
#   THIRD_PARTY_FIRST_PARTY_CRATES        the exact names of the first-party crates
#   THIRD_PARTY_HOST_TARGETS              the os/arch targets the release builds
#                                         the host binary for
#   THIRD_PARTY_GO_ENV                    the environment every `go list` runs in
#
#   third_party_is_first_party_crate NAME
#     Exit 0 when the crate NAME is first-party.
#
#   third_party_input_lines
#     Prints the serialization, one `label<TAB>sha256` line per input. Refuses,
#     exit 1 naming the file, when an input file is missing, and when the
#     core-plugin list cannot be read.
#
#   third_party_inputs_digest
#     Prints sha256:<hex> over third_party_input_lines. Refuses as it does.
#
#   third_party_input_labels
#     Prints the label of each input, one per line, for a message that names
#     what was read.

THIRD_PARTY_DIGEST_PREFIX="Inputs digest: "

THIRD_PARTY_FIRST_PARTY_GO_PREFIX="github.com/aggrega-ai/"
THIRD_PARTY_FIRST_PARTY_CRATES="aggrega-abi aggrega-pdk-rust"

# The os/arch targets the release builds the host binary (./cmd/aggrega) for. The
# generator attributes the union of the packages `go list -deps` reaches for each,
# since a target-specific dependency (a Windows console helper, say) is compiled
# only into that target's binary. This is a deliberate copy of the targets the
# release workflow (.github/workflows/publish-plugins-sign.yml: the native
# Linux build and each `build.sh --target` step) builds: the workflow cannot be
# read from here, because this file is sourced by scripts that run without it,
# and a script that parsed workflow steps would break on any rewording. Keep the
# two in step when a target is added or removed.
THIRD_PARTY_HOST_TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64"

# Every `go list` runs in this environment, so the dependency set is the
# release's whichever machine generates it: no workspace file maps a module onto
# a sibling checkout, the module graph is read as committed, and cgo is off.
THIRD_PARTY_GO_ENV="GOWORK=off GOFLAGS=-mod=readonly CGO_ENABLED=0"

third_party_is_first_party_crate() {
  case " $THIRD_PARTY_FIRST_PARTY_CRATES " in
    *" $1 "*) return 0 ;;
  esac
  return 1
}

# _third_party_go_lines FILE: go.mod or go.sum without the lines whose module
# path starts with THIRD_PARTY_FIRST_PARTY_GO_PREFIX. In go.sum that is the first
# field. In go.mod it is the `module` line's path, the first field inside a
# require or exclude block, the field after require or exclude on a one-line
# directive, and a replace line only when both the path it replaces and the path
# it points at are first-party. A path elsewhere in a line (a comment, say) does
# not make the line first-party.
_third_party_go_lines() {
  case "$1" in
    *go.sum) awk -v p="$THIRD_PARTY_FIRST_PARTY_GO_PREFIX" 'index($1, p) != 1' "$1" ;;
    *) awk -v p="$THIRD_PARTY_FIRST_PARTY_GO_PREFIX" '
      function fp(s) { return index(s, p) == 1 }
      function newpath(   i) { for (i = 1; i <= NF; i++) if ($i == "=>") return $(i + 1); return "" }
      {
        drop = 0
        if (block != "") {
          if ($1 == ")") { block = "" }
          else if (block == "replace") { drop = fp($1) && fp(newpath()) }
          else { drop = fp($1) }
        } else if ($1 == "module") { drop = fp($2) }
        else if (($1 == "require" || $1 == "exclude") && $2 == "(") { block = $1 }
        else if ($1 == "replace" && $2 == "(") { block = "replace" }
        else if ($1 == "require" || $1 == "exclude") { drop = fp($2) }
        else if ($1 == "replace") { drop = fp($2) && fp(newpath()) }
        if (!drop) print
      }' "$1" ;;
  esac
}

# _third_party_lock_lines FILE: a Cargo.lock without its first-party [[package]]
# blocks and without first-party entries in the other blocks' dependencies lists
# (an entry is `"name",` or `"name version",`, one per line; the name is matched
# exactly). A block ends at the
# next table header, [[...]] or [...], so a table after a first-party block is kept.
_third_party_lock_lines() {
  awk -v list="$THIRD_PARTY_FIRST_PARTY_CRATES" '
    function fp(n) { return index(" " list " ", " " n " ") > 0 }
    function flush() { if (n > 0 && !skip) printf "%s", buf; buf = ""; n = 0; skip = 0 }
    /^\[/ { flush() }
    {
      if ($0 ~ /^name = "/) { nm = $0; sub(/^name = "/, "", nm); sub(/".*/, "", nm); if (fp(nm)) skip = 1 }
      if ($0 ~ /^ "/) { e = $0; sub(/^ "/, "", e); sub(/[" ].*/, "", e); if (fp(e)) next }
      buf = buf $0 "\n"; n++
    }
    END { flush() }' "$1"
}

_third_party_sha256() {
  sha256sum | awk '{ print $1 }'
}

third_party_input_lines() {
  local root="$CORE_PLUGIN_MODULE_ROOT" rows socket name src toolchain extra embed dir f lock body
  rows="$(core_plugin_rows)" || return 1
  for f in go.mod go.sum scripts/gen-third-party-licenses.sh scripts/lib/third-party-inputs.sh; do
    if [ ! -f "$root/$f" ]; then
      echo "third-party-inputs: the input $f is missing (looked in $root)" >&2
      return 1
    fi
    case "$f" in
      go.mod|go.sum)
        body="$(_third_party_go_lines "$root/$f")" || return 1
        printf '%s\t%s\n' "$f" "$(printf '%s\n' "$body" | _third_party_sha256)" ;;
      *) printf '%s\t%s\n' "$f" "$(_third_party_sha256 < "$root/$f")" ;;
    esac
  done
  while IFS=$'\x1f' read -r socket name src toolchain extra embed dir; do
    [ -n "$socket" ] || continue
    [ "$embed" = yes ] || continue
    printf 'row %s %s %s %s\t%s\n' "$socket" "$name" "$src" "$toolchain" \
      "$(printf '%s\n%s\n%s\n%s\n' "$socket" "$name" "$src" "$toolchain" | _third_party_sha256)"
    if [ "$toolchain" = rust ]; then
      lock="${src#./}/Cargo.lock"
      if [ ! -f "$root/$lock" ]; then
        echo "third-party-inputs: the input $lock is missing (looked in $root)" >&2
        return 1
      fi
      body="$(_third_party_lock_lines "$root/$lock")" || return 1
      printf '%s\t%s\n' "$lock" "$(printf '%s\n' "$body" | _third_party_sha256)"
    fi
  done <<< "$rows"
}

third_party_inputs_digest() {
  local lines
  lines="$(third_party_input_lines)" || return 1
  printf 'sha256:%s\n' "$(printf '%s\n' "$lines" | _third_party_sha256)"
}

third_party_input_labels() {
  local lines
  lines="$(third_party_input_lines)" || return 1
  printf '%s\n' "$lines" | awk -F'\t' '{ print $1 }'
}
