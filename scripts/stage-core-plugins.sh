#!/usr/bin/env bash
# aggrega-cli/scripts/stage-core-plugins.sh — generate the core plugin catalog
# entities and stage the core plugins for publishing. It is called by the
# release workflow's build job and by a local check of the staging steps, so
# both stage them the same way. The binary built at the module root, aggrega,
# must exist, and so must the guests the build wrote beside it.
#
#   --overlay DIR --stage DIR
#       Generates, in this order, the entities and the staging directory.
#
#       Entities: one Plugin entity per core guest, from each guest's
#       machine-derived contract fields merged with its authored overlay in the
#       overlay directory, written into <stage>/entities; the entity version is
#       the binary's own build version. The guests the binary embeds are
#       generated from the binary itself; the core plugins the core-plugin list
#       names as not embedded are generated from the guests the build wrote
#       beside the embedded ones, through the list's one generation of them. A
#       guest with no overlay halts this step rather than getting invented
#       entity content.
#
#       Staging: each generated entity is paired with its decompressed guest,
#       written as the pairing the publish steps read. A Rust guest's notices
#       list every crate its Cargo.lock locks, and the generator reads each
#       crate's source from the cargo registry, offline, so cargo fetch --locked
#       runs first in each Rust guest directory the core-plugin list names. Each
#       guest is read from the artifact directory its row of the core-plugin
#       list names, decompressed into <stage>/wasm under the filename the
#       product's own tool, internal/buildtools/pluginguestfile, derives from
#       the entity (a guest is identified by socket and name, so a name is
#       never joined from the entity path here), and
#       publish-plugins.sh enumerate-embedded then writes <stage>/pairs.tsv:
#       for each entity, the guest it pairs with and the guest's notices and
#       SBOM, relative to <stage>, generated into <stage>/wasm/notices. It
#       publishes nothing, and writes the pairing only when every entity paired.
#
#       Relative --overlay and --stage paths are taken from the directory the
#       script is run in. The script itself runs from the module root.
#
# Usage:
#   scripts/stage-core-plugins.sh --overlay DIR --stage DIR
#   scripts/stage-core-plugins.sh --help
#
# Exits 0 when it did what it was asked, 1 when it could not, and 2 for an
# argument it cannot use.
set -euo pipefail

usage_error() { printf 'stage-core-plugins: %s\n' "$*" >&2; exit 2; }

overlay="" stage=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help) sed -n '2,/^# argument it cannot use\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    --overlay|--stage)
      [ "$#" -ge 2 ] && [ -n "$2" ] || usage_error "$1 takes a directory"
      if [ "$1" = --overlay ]; then overlay="$2"; else stage="$2"; fi
      shift 2 ;;
    *) usage_error "unknown argument: $1 (see --help)" ;;
  esac
done
[ -n "$overlay" ] || usage_error "--overlay is required (see --help)"
[ -n "$stage" ] || usage_error "--stage is required (see --help)"

abs() { case "$1" in /*) printf '%s\n' "$1" ;; *) printf '%s/%s\n' "$PWD" "$1" ;; esac; }
overlay="$(abs "$overlay")"
stage="$(abs "$stage")"

module_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$module_root"

# Generate the core plugin catalog entities.
export AGGREGA_BIN="$module_root/aggrega"
mkdir -p "$stage/entities"
"$AGGREGA_BIN" plugin gen-catalog --from-embedded --overlay "$overlay" --output-dir "$stage/entities"
. scripts/lib/core-plugins.sh
core_plugin_generate_unembedded "$AGGREGA_BIN" "$overlay" "$stage/entities"

# Stage the core plugins for publishing.
rust_dirs="$(core_plugin_rust_dirs)"
while IFS= read -r d; do
  [ -n "$d" ] || continue
  ( cd "$CORE_PLUGIN_MODULE_ROOT/$d" && cargo fetch --locked ) \
    || { echo "cargo fetch --locked failed in aggrega-cli/$d; the notices generator reads every locked crate's source" >&2; exit 1; }
done <<< "$rust_dirs"
STAGE="$stage"
WASM_DIR="$STAGE/wasm"
mkdir -p "$WASM_DIR"
staged=0
for entity in "$STAGE"/entities/*/*/*/plugin.yaml; do
  [ -f "$entity" ] || continue
  leaf="$(GOWORK=off go run ./internal/buildtools/pluginguestfile -entity "$entity")"
  # entities/<socket>/<name>/<version>/plugin.yaml -> <socket>, <name>:
  # the row of the core-plugin list the entity was generated for.
  name="$(basename "$(dirname "$(dirname "$entity")")")"
  socket="$(basename "$(dirname "$(dirname "$(dirname "$entity")")")")"
  dir="$(core_plugin_dir_of "$socket" "$name")"
  src="$dir/$name.wasm.gz"
  [ -f "$src" ] || { echo "no built guest for $entity at $src" >&2; exit 1; }
  gunzip -c "$src" > "$WASM_DIR/$leaf"
  staged=$((staged + 1))
done
[ "$staged" -gt 0 ] || { echo "staged no guests — entity generation produced nothing" >&2; exit 1; }
echo "staged $staged guest(s) into $WASM_DIR"
scripts/publish-plugins.sh enumerate-embedded \
  --entities-dir "$STAGE/entities" --wasm-dir "$WASM_DIR" --pairs-out "$STAGE/pairs.tsv"
