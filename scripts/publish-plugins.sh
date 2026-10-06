#!/usr/bin/env bash
# Publish Wasm plugins to an OCI registry over a discovered work-list. This is
# the plugin-scoped sibling of the catalog-entity publishers: it enumerates a set
# of Plugin entities (each paired with its .wasm guest) and publishes each through
# `aggrega push --plugin`, which pushes the .wasm (addressed by its content
# digest) and the entity (tagged by its version), enforces immutability, and
# cross-checks the guest's describe() against the entity's authored claims. This
# script only enumerates and invokes; every integrity guarantee lives in the CLI.
#
# It is NOT tied to any fixed plugin set. Three enumeration modes select the
# work-list:
#
#   enumerate-embedded --entities-dir <dir> --wasm-dir <dir>
#       Publish the entities a `aggrega plugin gen-catalog --from-embedded` run
#       wrote under <entities-dir>, each paired with its decompressed guest in
#       <wasm-dir>. This is exactly the set of guests built into the binary.
#
#   glob-dir <dir> [--wasm-dir <dir>]
#       Publish every Plugin entity YAML found under <dir>. Each entity's guest is
#       resolved from <wasm-dir> when given, else from a co-located .wasm.
#
#   git-diff-range <base>..<head> [--repo <dir>] [--path <subpath>] \
#                  [--wasm-dir <dir>] [--marker <tag>] [--no-advance]
#       Publish the Plugin entities a git diff between two commits touched. With
#       --marker <tag>, a git tag is advanced to HEAD on a fully successful,
#       non-dry-run publish so the next run diffs from where this one finished.
#
# A guest's .wasm is paired to its entity by the filename the entity's own
# identity derives, looked up in --wasm-dir (or beside the entity). That
# filename is derived by the shipped mapping and never spelled here; see the
# guest-filename section below. A discovered YAML whose kind is not Plugin is
# skipped with a notice — non-plugin catalog entities belong to the
# catalog-entity publisher, not here — never guessed at.
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
# --dry-run computes and prints the proposed references for every mode without any
# network call, OIDC contact, or marker advance.
#
# --pairs-out <file> resolves the work-list and writes it as a pairing instead of
# publishing it: one line per Plugin entity, its guest's path, a tab and the
# entity's path, each relative to the directory <file> sits in. Both must lie
# under that directory, so the directory can be handed to another machine whole,
# and the pairing is written only when every entity paired. With --pairs-out
# nothing is published or signed and no marker moves, so --dry-run, --sign, --key,
# --identity-token and --marker are refused beside it. A pairing is published by
# scripts/publish-plugin-pairs.sh, which needs only the aggrega binary: that is
# how a publishing machine is kept free of the toolchain the derivation above
# runs.
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
REPO=""
PATHSPEC=""
MARKER=""
NO_ADVANCE=""
PAIRS_OUT=""
POSITIONAL=""
AGGREGA="${AGGREGA_BIN:-aggrega}"

usage() {
  cat >&2 <<'EOF'
usage:
  publish-plugins.sh enumerate-embedded --entities-dir <dir> --wasm-dir <dir> [common]
  publish-plugins.sh glob-dir <dir> [--wasm-dir <dir>] [common]
  publish-plugins.sh git-diff-range <base>..<head> [--repo <dir>] [--path <subpath>]
                     [--wasm-dir <dir>] [--marker <tag>] [--no-advance] [common]

  common: [--registry <host:port>] [--dry-run]
          [--sign [--key <path>] [--identity-token <token>]]
          | --pairs-out <file>   (write the pairing; publish nothing)
EOF
}

MODE="${1:-}"
case "$MODE" in
  enumerate-embedded|glob-dir|git-diff-range) shift ;;
  -h|--help) usage; exit 0 ;;
  *) usage; exit 2 ;;
esac

while [ $# -gt 0 ]; do
  case "$1" in
    --registry)       REGISTRY="$2"; shift 2 ;;
    --entities-dir)   ENTITIES_DIR="$2"; shift 2 ;;
    --wasm-dir)       WASM_DIR="$2"; shift 2 ;;
    --repo)           REPO="$2"; shift 2 ;;
    --path)           PATHSPEC="$2"; shift 2 ;;
    --marker)         MARKER="$2"; shift 2 ;;
    --no-advance)     NO_ADVANCE="1"; shift ;;
    --dry-run)        DRYRUN="--dry-run"; shift ;;
    --sign)           SIGN="1"; shift ;;
    --key)            KEY="$2"; shift 2 ;;
    --identity-token) IDTOKEN="$2"; shift 2 ;;
    --pairs-out)      PAIRS_OUT="$2"; shift 2 ;;
    -h|--help)        usage; exit 0 ;;
    --*)              usage; exit 2 ;;
    *)
      if [ -z "$POSITIONAL" ]; then POSITIONAL="$1"; shift
      else usage; exit 2; fi
      ;;
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
# the entity's identity derives, looked up in --wasm-dir when given, else beside
# the entity. Returns non-zero (with a diagnostic) when the filename cannot be
# derived or the guest file is missing — a gap is surfaced, never guessed around.
resolve_wasm() {
  local entity="$1" leaf wasm
  if ! leaf="$(guest_filename "$entity")"; then
    echo "publish-plugins: cannot derive the guest filename for $entity" >&2
    return 1
  fi
  if [ -n "$WASM_DIR" ]; then
    wasm="$WASM_DIR/$leaf"
  else
    wasm="$(dirname "$entity")/$leaf"
  fi
  if [ ! -f "$wasm" ]; then
    echo "publish-plugins: guest .wasm for $entity not found at $wasm (set --wasm-dir or co-locate it)" >&2
    return 1
  fi
  printf '%s\n' "$wasm"
}

# --- Marker seam (git-diff-range only) ---------------------------------------
# The only two places the marker mechanism is touched. Default mechanism is a git
# tag on the diffed repository: read = the commit the tag points at; advance =
# move the tag to HEAD. To back the marker with a remote object instead, replace
# the bodies of these two functions and nothing else.
marker_read() {
  git -C "$REPO" rev-parse --verify --quiet "refs/tags/${MARKER}^{commit}" 2>/dev/null
}
marker_advance() {
  git -C "$REPO" tag -f "$MARKER" HEAD >/dev/null
}
# -----------------------------------------------------------------------------

# --- A pairing publishes nothing ---------------------------------------------
# --pairs-out only resolves and writes, so every option that shapes a publish is
# refused beside it rather than silently ignored.
if [ -n "$PAIRS_OUT" ]; then
  if [ -n "$DRYRUN" ] || [ -n "$SIGN" ] || [ -n "$KEY" ] || [ -n "$IDTOKEN" ] || [ -n "$MARKER" ]; then
    echo "publish-plugins: --pairs-out writes the pairing and publishes nothing, so --dry-run, --sign, --key, --identity-token and --marker do not combine with it" >&2
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
    [ -n "$ENTITIES_DIR" ] || { echo "publish-plugins: enumerate-embedded requires --entities-dir <dir> (a 'aggrega plugin gen-catalog --from-embedded' output)" >&2; exit 2; }
    [ -n "$WASM_DIR" ]     || { echo "publish-plugins: enumerate-embedded requires --wasm-dir <dir> (the decompressed guest set)" >&2; exit 2; }
    [ -d "$ENTITIES_DIR" ] || { echo "publish-plugins: entities dir not found: $ENTITIES_DIR" >&2; exit 1; }
    while IFS= read -r f; do FILES+=("$f"); done < <(find "$ENTITIES_DIR" -name '*.yaml' | sort)
    ;;
  glob-dir)
    DIR="${POSITIONAL:-$ENTITIES_DIR}"
    [ -n "$DIR" ] || { echo "publish-plugins: glob-dir requires a <dir> operand" >&2; exit 2; }
    [ -d "$DIR" ] || { echo "publish-plugins: glob-dir: directory not found: $DIR" >&2; exit 1; }
    while IFS= read -r f; do FILES+=("$f"); done < <(find "$DIR" -name '*.yaml' | sort)
    ;;
  git-diff-range)
    RANGE="$POSITIONAL"
    [ -n "$RANGE" ] || { echo "publish-plugins: git-diff-range requires a <base>..<head> operand" >&2; exit 2; }
    [ -n "$REPO" ] || REPO="$(git rev-parse --show-toplevel)"
    range_args=(diff --name-only --diff-filter=ACMR "$RANGE")
    [ -n "$PATHSPEC" ] && range_args+=(-- "$PATHSPEC")
    while IFS= read -r rel; do
      [ -n "$rel" ] || continue
      FILES+=("$REPO/$rel")
    done < <(git -C "$REPO" "${range_args[@]}" | sort)
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
  if [ -n "$PAIRS_OUT" ]; then
    if wasm_rel="$(pair_path "$wasm")" && entity_rel="$(pair_path "$f")"; then
      PAIRS+=("$wasm_rel"$'\t'"$entity_rel")
    else
      failed=$((failed + 1))
    fi
    continue
  fi
  if plugin_publish_one "$wasm" "$f"; then
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

# --- Advance the marker on a fully successful, real publish -------------------
if [ "$MODE" = "git-diff-range" ] && [ -n "$MARKER" ] && [ "$failed" -eq 0 ]; then
  if [ -n "$DRYRUN" ]; then
    :  # dry-run mutates nothing, including the marker.
  elif [ -n "$NO_ADVANCE" ]; then
    echo "publish-plugins: --no-advance: marker '$MARKER' left unchanged"
  else
    marker_advance
    echo "publish-plugins: marker '$MARKER' advanced to $(git -C "$REPO" rev-parse --short HEAD)"
  fi
fi

[ "$failed" -eq 0 ]
