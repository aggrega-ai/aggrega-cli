#!/usr/bin/env bash
# aggrega-cli/scripts/lib/core-plugins.sh
#
# The one reader of the core-plugin list, internal/wasm/embedded/embedded-plugins.tsv,
# SOURCED (never executed) by every script that derives a set from it: the guest
# build, the guest and entity freshness checks, the overlay check, the entity
# generators, the co-located dev catalog and the release workflow. The list names
# every core plugin the compiler's repository builds, stamps and publishes, and
# says for each whether the binary embeds it; this file is how a script reads
# that, so no script spells the list's layout or the embed rule a second time.
#
# The embed column. A row with no sixth field, or an empty one, is embedded: its
# guest, descriptor and entity are written to CORE_PLUGIN_EMBED_DIR, which the
# binary embeds whole. A row whose sixth field is `no` is built, stamped,
# generated and published exactly as an embedded one, and never embedded: its
# artifacts are written to CORE_PLUGIN_PUBLISHED_DIR, which nothing embeds. Any
# other value is refused, here and by the binary's own reading of the list, so a
# typo can neither embed a plugin nor drop one.
#
# Of a row that is not embedded the binary still carries what selects it, and
# nothing of its module: its generated entity, as <name>.plugin.yaml, and the
# content digest of its decompressed guest, as <name>.digest holding one line,
# sha256:<lowercase hex>. Both are written to CORE_PLUGIN_SELECTION_DIR, which
# the binary embeds whole: the digest by the guest build, the entity by the
# entity generator, each a copy of what it writes beside the guest. The guest and
# entity checks hold each to the guest and the entity it was copied from, and a
# test in internal/wasm/embedded holds the digest to the binary's own derivation.
#
# 🔴 The rows are read with awk -F'\t', never with `IFS=$'\t' read`. A tab is an
# IFS whitespace character, so `read` folds a run of tabs into one separator and
# an empty field vanishes: a row whose extra source roots are empty would hand
# its embed value to the extra-roots field, and the row would be built as an
# embedded guest. awk keeps every empty field where it stands. What this file
# prints is joined by the unit separator, which is not IFS whitespace, so a
# caller's `read` keeps an empty field too.
#
# The binary reads the same list (parseManifest in internal/wasm/embedded), and a
# test there runs core_plugin_rows over shared fixtures and over the list itself,
# so the two readings are held to one answer.
#
# Paths in the list, and every directory printed here, are relative to the
# aggrega-cli module root; CORE_PLUGIN_MODULE_ROOT is that root, absolute, so a
# caller anywhere can reach them.
#
# Sourcing sets five variables and defines three functions:
#
#   CORE_PLUGIN_MODULE_ROOT    the aggrega-cli module root, absolute
#   CORE_PLUGIN_LIST           the list, relative to the module root
#   CORE_PLUGIN_EMBED_DIR      where an embedded row's artifacts are written
#   CORE_PLUGIN_PUBLISHED_DIR  where a row that is not embedded writes them
#   CORE_PLUGIN_SELECTION_DIR  where the entity and guest digest the binary
#                              carries of a row that is not embedded are written
#
#   core_plugin_rows [FILE]
#     Prints every row of FILE (default: the list), one per line, as seven fields
#     joined by the unit separator (\x1f): socket, name, sourcePath, toolchain,
#     extraRoots, embed (`yes` or `no`), and the row's artifact directory. Blank
#     lines and lines beginning with # are skipped. It refuses, exit 1 naming the
#     row, a row with fewer than two fields, an empty socket or name, or an embed
#     value other than empty or `no`, and a list declaring no plugin. Capture it
#     before reading it, so a refusal stops the caller rather than ending a loop
#     early:
#
#       rows="$(core_plugin_rows)" || exit 1
#       while IFS=$'\x1f' read -r socket name src toolchain extra embed dir; do
#         [ -n "$socket" ] || continue
#         …
#       done <<< "$rows"
#
#   core_plugin_dir_of SOCKET NAME
#     Prints the artifact directory of the row naming SOCKET/NAME, or refuses,
#     exit 1, when the list names no such plugin.
#
#   core_plugin_generate_unembedded AGGREGA OVERLAY_DIR OUTPUT_DIR
#     Generates the Plugin entity of every row that is not embedded, from its
#     stamped guest in CORE_PLUGIN_PUBLISHED_DIR and its overlay
#     OVERLAY_DIR/<socket>/<name>.yaml, through `AGGREGA plugin gen-catalog
#     --plugin`, under OUTPUT_DIR/<socket>/<name>/<version>/plugin.yaml — the
#     layout `gen-catalog --from-embedded` writes the embedded rows in. It is the
#     one generation of those entities: the entity generator beside the guests,
#     the core-plugin catalog generator and the release workflow all call it,
#     each after `--from-embedded` has generated the embedded rows. It refuses,
#     exit 1, when a guest or an overlay is missing, when the generation fails,
#     or when the entity it wrote is not the row's plugin.

CORE_PLUGIN_MODULE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CORE_PLUGIN_LIST="internal/wasm/embedded/embedded-plugins.tsv"
CORE_PLUGIN_EMBED_DIR="internal/wasm/embedded/plugins"
CORE_PLUGIN_PUBLISHED_DIR="internal/wasm/embedded/published-only"
CORE_PLUGIN_SELECTION_DIR="internal/wasm/embedded/published-selection"

core_plugin_rows() {
  local file="${1:-$CORE_PLUGIN_MODULE_ROOT/$CORE_PLUGIN_LIST}"
  if [ ! -f "$file" ]; then
    echo "core-plugins: the core-plugin list is not at $file" >&2
    return 1
  fi
  awk -F'\t' -v OFS=$'\x1f' -v embed_dir="$CORE_PLUGIN_EMBED_DIR" -v published_dir="$CORE_PLUGIN_PUBLISHED_DIR" '
    function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
    function refuse(msg) {
      print "core-plugins: " FILENAME ":" FNR ": " msg ": " $0 > "/dev/stderr"
      refused = 1
      exit 1
    }
    {
      sub(/\r$/, "")
      line = trim($0)
      if (line == "" || substr(line, 1, 1) == "#") next
      if (NF < 2) refuse("a row needs at least a socket and a name")
      socket = trim($1); name = trim($2)
      if (socket == "" || name == "") refuse("a row is missing its socket or its name")
      embed = trim($6)
      if (embed == "") { embed = "yes"; dir = embed_dir }
      else if (embed == "no") { dir = published_dir }
      else refuse("the embed column is empty or no, never \"" embed "\"")
      print socket, name, trim($3), trim($4), trim($5), embed, dir
      rows++
    }
    END {
      if (refused) exit 1
      if (rows == 0) {
        print "core-plugins: " FILENAME ": the core-plugin list declares no plugin" > "/dev/stderr"
        exit 1
      }
    }
  ' "$file"
}

core_plugin_dir_of() {
  local want_socket="$1" want_name="$2" rows socket name src toolchain extra embed dir
  rows="$(core_plugin_rows)" || return 1
  while IFS=$'\x1f' read -r socket name src toolchain extra embed dir; do
    if [ "$socket" = "$want_socket" ] && [ "$name" = "$want_name" ]; then
      printf '%s\n' "$dir"
      return 0
    fi
  done <<< "$rows"
  echo "core-plugins: the core-plugin list names no plugin $want_socket/$want_name" >&2
  return 1
}

core_plugin_generate_unembedded() {
  local aggrega="$1" overlay_dir="$2" output_dir="$3"
  local rows socket name src toolchain extra embed dir guest overlay tmp out path generated
  rows="$(core_plugin_rows)" || return 1
  tmp="$(mktemp -d)" || return 1
  while IFS=$'\x1f' read -r socket name src toolchain extra embed dir; do
    [ -n "$socket" ] || continue
    [ "$embed" = no ] || continue
    guest="$CORE_PLUGIN_MODULE_ROOT/$dir/$name.wasm.gz"
    overlay="$overlay_dir/$socket/$name.yaml"
    if [ ! -f "$guest" ]; then
      echo "core-plugins: no built guest for $socket/$name at $guest (run scripts/build-embedded-plugins.sh first)" >&2
      rm -rf "$tmp"; return 1
    fi
    if [ ! -f "$overlay" ]; then
      echo "core-plugins: no overlay for $socket/$name at $overlay" >&2
      rm -rf "$tmp"; return 1
    fi
    if ! gunzip -c "$guest" > "$tmp/$name.wasm"; then
      echo "core-plugins: could not decompress $guest" >&2
      rm -rf "$tmp"; return 1
    fi
    if ! out="$("$aggrega" plugin gen-catalog --plugin "$tmp/$name.wasm" --overlay "$overlay" --output-dir "$output_dir")"; then
      printf '%s\n' "$out" >&2
      echo "core-plugins: generating the entity of $socket/$name failed" >&2
      rm -rf "$tmp"; return 1
    fi
    printf '%s\n' "$out"
    # gen-catalog names the one entity it wrote as `generated <path>`, at
    # <output-dir>/<pluginType>/<metadata.name>/<version>/plugin.yaml. That the
    # path sits under this row's socket and name is what holds the guest's plugin
    # type and the overlay's name to the row; -ef compares the directories
    # themselves, whatever spelling of the output directory either side used.
    generated=0
    while IFS= read -r path; do
      case "$path" in "generated "*) ;; *) continue ;; esac
      path="${path#generated }"
      generated=$((generated + 1))
      if ! [ "$(dirname "$(dirname "$path")")" -ef "$output_dir/$socket/$name" ]; then
        echo "core-plugins: the entity generated for $socket/$name was written at $path, not under $output_dir/$socket/$name: the guest's plugin type or the overlay's metadata.name is not the row's" >&2
        rm -rf "$tmp"; return 1
      fi
    done <<< "$out"
    if [ "$generated" -ne 1 ]; then
      echo "core-plugins: generating $socket/$name wrote $generated entities, not one" >&2
      rm -rf "$tmp"; return 1
    fi
  done <<< "$rows"
  rm -rf "$tmp"
  return 0
}
