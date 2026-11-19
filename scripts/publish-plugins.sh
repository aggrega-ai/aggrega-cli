#!/usr/bin/env bash
# Publish Wasm plugins to an OCI registry over a discovered work-list. This is
# the plugin-scoped sibling of the catalog-entity publishers: it enumerates a set
# of Plugin entities (each paired with its .wasm guest) and publishes each through
# `aggrega push --plugin`, which pushes the .wasm (addressed by its content
# digest) and the entity (tagged by its version), enforces immutability, and
# cross-checks the guest's describe() against the entity's authored claims. This
# script only enumerates and invokes; every integrity guarantee lives in the CLI.
#
# It is NOT tied to any fixed plugin set. The mode enumerate-embedded
# selects the work-list:
#
#   enumerate-embedded --entities-dir <dir> --wasm-dir <dir>
#       Publish the core plugin entities a release generated under
#       <entities-dir> — `aggrega plugin gen-catalog --from-embedded` for the
#       guests built into the binary, and the same generation per guest for the
#       core plugins built without being embedded — each paired with its
#       decompressed guest in <wasm-dir>.
#
# A guest's .wasm is paired to its entity by the filename the entity's own
# identity derives, looked up in --wasm-dir. That filename is derived by the
# shipped mapping and never spelled here; see the guest-filename section below.
# A discovered YAML whose kind is not Plugin is skipped with a notice —
# non-plugin catalog entities belong to the catalog-entity publisher, not here —
# never guessed at.
#
# Deriving a guest filename needs the Go toolchain and this module's source, so
# the script runs from a checkout rather than against a bare binary.
#
# Signing (--sign) is per item through `aggrega push --plugin --sign`, which signs
# BOTH the entity and the .wasm under one signer. --key selects offline keyed
# signing (no token, the private-registry default); --identity-token (or the
# AGGREGA_IDENTITY_TOKEN / SIGSTORE_ID_TOKEN environment variable) selects
# non-interactive keyless signing. Signing is NON-INTERACTIVE ONLY: the script
# refuses to sign when no key or token source is present, so the keyless flow can
# never fall back to a browser prompt. The publish command, its options and that
# refusal live in scripts/lib/plugin-publish.sh, which this script sources.
#
# --dry-run computes and prints the proposed references without publishing,
# signing or contacting an OIDC issuer. It still runs the notices generator for
# each guest, and that generator's `go list` can fetch a Go module the module
# cache lacks.
#
# Every guest is published with its third-party notices and SBOM. The script
# generates both for each guest, where the guest was built (the module and crate
# caches the generator reads are there), with
# `scripts/gen-third-party-licenses.sh --guest <socket>/<name> --out <dir>`, into
# <wasm-dir>/notices/<socket>/<name>/ as notices.md and sbom.spdx.json; the
# socket and name are read from the entity's place under <entities-dir>
# (<socket>/<name>/<version>/plugin.yaml).
#
# --pairs-out <file> resolves the work-list and writes it as a pairing instead of
# publishing it: one line per Plugin entity: its guest's path, the entity's path,
# the guest's notices file and its SBOM file, separated by tabs, each relative to
# the directory <file> sits in. Every path in a line must lie under that directory, so
# the directory can be handed to another machine whole, and the pairing is written
# only when every entity paired. With --pairs-out nothing is published or signed,
# so --dry-run, --sign, --key and --identity-token are refused beside it. A
# pairing is published by scripts/publish-plugin-pairs.sh, which needs only the
# aggrega binary: that is how a publishing machine is kept free of the toolchain
# the derivation above runs.
#
# Env: AGGREGA_BIN overrides the `aggrega` binary (e.g. a freshly-built one).
set -euo pipefail

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd)"   # aggrega-cli module root

# The publish command and the signing refusal (plugin_publish_one,
# plugin_publish_require_signing_material) have one home, shared with
# scripts/publish-plugin-pairs.sh.
# shellcheck source-path=SCRIPTDIR source=lib/plugin-publish.sh
. "$CLI_ROOT/scripts/lib/plugin-publish.sh"

REGISTRY="127.0.0.1:5000"
DRYRUN=""
SIGN=""
KEY=""
IDTOKEN=""
ENTITIES_DIR=""
WASM_DIR=""
PAIRS_OUT=""
AGGREGA="${AGGREGA_BIN:-aggrega}"

usage() {
  cat >&2 <<'EOF'
usage:
  publish-plugins.sh enumerate-embedded --entities-dir <dir> --wasm-dir <dir> [common]

  common: [--registry <host:port>] [--dry-run]
          [--sign [--key <path>] [--identity-token <token>]]
          | --pairs-out <file>   (write the pairing; publish nothing)
EOF
}

MODE="${1:-}"
case "$MODE" in
  enumerate-embedded) shift ;;
  -h|--help) usage; exit 0 ;;
  *) echo "publish-plugins: unknown mode: $MODE" >&2; usage; exit 2 ;;
esac

while [ $# -gt 0 ]; do
  case "$1" in
    --registry)       REGISTRY="$2"; shift 2 ;;
    --entities-dir)   ENTITIES_DIR="$2"; shift 2 ;;
    --wasm-dir)       WASM_DIR="$2"; shift 2 ;;
    --dry-run)        DRYRUN="--dry-run"; shift ;;
    --sign)           SIGN="1"; shift ;;
    --key)            KEY="$2"; shift 2 ;;
    --identity-token) IDTOKEN="$2"; shift 2 ;;
    --pairs-out)      PAIRS_OUT="$2"; shift 2 ;;
    -h|--help)        usage; exit 0 ;;
    --*)              usage; exit 2 ;;
    *)                usage; exit 2 ;;
  esac
done

# peek_kind prints the top-level `kind:` value of a YAML file, or nothing.
peek_kind() {
  awk -F':[[:space:]]*' '/^kind:[[:space:]]/{print $2; exit}' "$1" | tr -d '\r'
}

# --- The guest filename ------------------------------------------------------
# A guest's .wasm is named by the plugin's whole identity — its socket and its
# bare name together, each encoded and then joined.
#
# That rule has ONE home: the pluginpath package, beside the resolver that reads
# the file by it. It is asked for here and never spelled here. The tempting
# shortcut — keep the last path segment of metadata.name — is LOSSY twice over:
# it drops the socket, which the name alone does not carry, and where a name
# carries a separator it discards everything before it. What it discards is the
# plugin's location, a publisher or a namespace, and two publishers shipping a
# same-named plugin are both perfectly legitimate. Distinct identities reach
# distinct filenames because the shipped encoding guarantees it — no encoded
# part can contain the sequence the parts are joined on, and a test over the
# encoding asserts that — so the unambiguity here is inherited, not established
# here and not enforced on anyone. Nothing in this script constrains what a
# plugin may be named.
#
# guest_filename prints the filename an entity's guest is written under, derived
# through that one mapping. The entity path is made absolute first, because the
# derivation runs from the module root.
guest_filename() {
  local entity="$1"
  case "$entity" in /*) : ;; *) entity="$PWD/$entity" ;; esac
  (cd "$CLI_ROOT" && GOWORK=off go run ./internal/buildtools/pluginguestfile -entity "$entity")
}

# resolve_wasm prints the .wasm path paired with an entity: the guest filename
# the entity's identity derives, looked up in --wasm-dir. Returns non-zero (with a diagnostic) when the filename cannot be
# derived or the guest file is missing — a gap is surfaced, never guessed around.
resolve_wasm() {
  local entity="$1" leaf wasm
  if ! leaf="$(guest_filename "$entity")"; then
    echo "publish-plugins: cannot derive the guest filename for $entity" >&2
    return 1
  fi
  wasm="$WASM_DIR/$leaf"
  if [ ! -f "$wasm" ]; then
    echo "publish-plugins: guest .wasm for $entity not found at $wasm (set --wasm-dir)" >&2
    return 1
  fi
  printf '%s\n' "$wasm"
}

# generate_notices <entity> prints, tab-separated, the notices file and the SBOM
# file generated for the guest of a core plugin entity. Fails when the generator
# does.
generate_notices() {
  local entity="$1" name socket out
  name="$(basename "$(dirname "$(dirname "$entity")")")"
  socket="$(basename "$(dirname "$(dirname "$(dirname "$entity")")")")"
  out="$WASM_DIR/notices/$socket/$name"
  mkdir -p "$out"
  "$CLI_ROOT/scripts/gen-third-party-licenses.sh" --guest "$socket/$name" --out "$out" >&2 || return 1
  printf '%s\t%s\n' "$out/notices.md" "$out/sbom.spdx.json"
}

# --- A pairing publishes nothing ---------------------------------------------
# --pairs-out only resolves and writes, so every option that shapes a publish is
# refused beside it rather than silently ignored.
if [ -n "$PAIRS_OUT" ]; then
  if [ -n "$DRYRUN" ] || [ -n "$SIGN" ] || [ -n "$KEY" ] || [ -n "$IDTOKEN" ]; then
    echo "publish-plugins: --pairs-out writes the pairing and publishes nothing, so --dry-run, --sign, --key and --identity-token do not combine with it" >&2
    exit 2
  fi
  PAIRS_DIR="$(dirname "$PAIRS_OUT")"
  [ -d "$PAIRS_DIR" ] || { echo "publish-plugins: --pairs-out: directory not found: $PAIRS_DIR" >&2; exit 1; }
  PAIRS_DIR="$(cd "$PAIRS_DIR" && pwd -P)"
fi

# --- Non-interactive signing discipline --------------------------------------
# Refuse to sign a real publish without signing material, so the keyless flow can
# never fall back to an interactive browser prompt. A dry-run signs nothing.
plugin_publish_require_signing_material publish-plugins || exit 2

# pair_path prints <path> relative to the pairing's directory, or fails when it
# does not lie under it or carries a character the pairing's lines cannot hold.
pair_path() {
  local path="$1" dir abs
  dir="$(cd "$(dirname "$path")" && pwd -P)" || return 1
  abs="$dir/$(basename "$path")"
  case "$abs" in
    "$PAIRS_DIR"/*) ;;
    *) echo "publish-plugins: $path does not lie under $PAIRS_DIR, the directory the pairing is written in" >&2; return 1 ;;
  esac
  abs="${abs#"$PAIRS_DIR"/}"
  case "$abs" in
    *$'\t'*|*$'\n'*) echo "publish-plugins: $path carries a tab or a newline, which a pairing line cannot hold" >&2; return 1 ;;
  esac
  printf '%s\n' "$abs"
}

# --- Select the candidate entity files ---------------------------------------
FILES=()
case "$MODE" in
  enumerate-embedded)
    [ -n "$ENTITIES_DIR" ] || { echo "publish-plugins: enumerate-embedded requires --entities-dir <dir> (the core plugin entities a release generated)" >&2; exit 2; }
    [ -n "$WASM_DIR" ]     || { echo "publish-plugins: enumerate-embedded requires --wasm-dir <dir> (the decompressed guest set)" >&2; exit 2; }
    [ -d "$ENTITIES_DIR" ] || { echo "publish-plugins: entities dir not found: $ENTITIES_DIR" >&2; exit 1; }
    while IFS= read -r f; do FILES+=("$f"); done < <(find "$ENTITIES_DIR" -name '*.yaml' | sort)
    ;;
esac

# --- Push loop ---------------------------------------------------------------
pushed=0; failed=0; skipped=0
REFS=()
PAIRS=()
for f in ${FILES[@]+"${FILES[@]}"}; do
  [ -f "$f" ] || continue
  kind="$(peek_kind "$f")"
  [ -n "$kind" ] || continue          # non-entity YAML (no kind:) — skip
  if [ "$kind" != "Plugin" ]; then
    echo "publish-plugins: skipping non-Plugin ($kind): $f — non-plugin catalog entities belong to the catalog-entity publisher, not this plugin publisher" >&2
    skipped=$((skipped + 1))
    continue
  fi
  if ! wasm="$(resolve_wasm "$f")"; then
    failed=$((failed + 1))
    continue
  fi
  if ! gen="$(generate_notices "$f")"; then
    echo "publish-plugins: cannot generate the notices and SBOM for $f" >&2
    failed=$((failed + 1))
    continue
  fi
  notices="${gen%%$'\t'*}" sbom="${gen#*$'\t'}"
  if [ -n "$PAIRS_OUT" ]; then
    if wasm_rel="$(pair_path "$wasm")" && entity_rel="$(pair_path "$f")" \
      && notices_rel="$(pair_path "$notices")" && sbom_rel="$(pair_path "$sbom")"; then
      PAIRS+=("$wasm_rel"$'\t'"$entity_rel"$'\t'"$notices_rel"$'\t'"$sbom_rel")
    else
      failed=$((failed + 1))
    fi
    continue
  fi
  if plugin_publish_one "$wasm" "$f" "$notices" "$sbom"; then
    pushed=$((pushed + 1))
    [ -n "$PLUGIN_PUBLISHED_REF" ] && REFS+=("$PLUGIN_PUBLISHED_REF")
  else
    echo "publish-plugins: PUSH FAILED: $f" >&2
    failed=$((failed + 1))
  fi
done

# --- Write the pairing, and publish nothing -----------------------------------
if [ -n "$PAIRS_OUT" ]; then
  if [ "$failed" -ne 0 ] || [ "${#PAIRS[@]}" -eq 0 ]; then
    echo "publish-plugins: $MODE: no pairing written: ${#PAIRS[@]} paired, $skipped skipped, $failed failed" >&2
    exit 1
  fi
  tmp="$(mktemp "$PAIRS_DIR/.pairs.XXXXXX")"
  printf '%s\n' "${PAIRS[@]}" > "$tmp"
  mv -f "$tmp" "$PAIRS_DIR/$(basename "$PAIRS_OUT")"
  echo "publish-plugins: $MODE: wrote ${#PAIRS[@]} pair(s) to $PAIRS_OUT ($skipped skipped)"
  exit 0
fi

echo "publish-plugins: $MODE: $pushed pushed, $skipped skipped, $failed failed -> $REGISTRY"
if [ "${#REFS[@]}" -gt 0 ]; then
  echo "publish-plugins: references:"
  printf '  %s\n' "${REFS[@]}"
fi

[ "$failed" -eq 0 ]
