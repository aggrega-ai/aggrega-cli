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
# A guest's .wasm is paired to its entity by the entity's metadata.name identity:
# the final path segment of metadata.name names the `<segment>.wasm` file looked
# up in --wasm-dir (or beside the entity). A discovered YAML whose kind is not
# Plugin is skipped with a notice — non-plugin catalog entities belong to the
# catalog-entity publisher, not here — never guessed at.
#
# Signing (--sign) is per item through `aggrega push --plugin --sign`, which signs
# BOTH the entity and the .wasm under one signer. --key selects offline keyed
# signing (no token, the private-registry default); --identity-token (or the
# AGGREGA_IDENTITY_TOKEN / SIGSTORE_ID_TOKEN environment variable) selects
# non-interactive keyless signing. Signing is NON-INTERACTIVE ONLY: the script
# refuses to sign when no key or token source is present, so the keyless flow can
# never fall back to a browser prompt.
#
# --dry-run computes and prints the proposed references for every mode without any
# network call, OIDC contact, or marker advance.
#
# Env: AGGREGA_BIN overrides the `aggrega` binary (e.g. a freshly-built one).
set -euo pipefail

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

# peek_metadata_name prints the entity's metadata.name identity (its full value,
# slashes and all), or nothing. Only the `name:` nested directly under the
# top-level `metadata:` block is read — a `name:` anywhere else (under spec, under
# a nested block) is ignored — and surrounding quotes are stripped.
peek_metadata_name() {
  awk '
    /^metadata:[[:space:]]*$/ { inmeta = 1; next }
    /^[^[:space:]#]/          { inmeta = 0 }
    inmeta && /^[[:space:]]+name:[[:space:]]/ {
      sub(/^[[:space:]]*name:[[:space:]]*/, "")
      gsub(/^"|"$/, "")
      gsub(/^'\''|'\''$/, "")
      print; exit
    }' "$1" | tr -d '\r'
}

# resolve_wasm prints the .wasm path paired with an entity: the file named by the
# final path segment of metadata.name, looked up in --wasm-dir when given, else
# beside the entity. Returns non-zero (with a diagnostic) when the identity or the
# guest file is missing — a gap is surfaced, never guessed around.
resolve_wasm() {
  local entity="$1" name leaf wasm
  name="$(peek_metadata_name "$entity")"
  if [ -z "$name" ]; then
    echo "publish-plugins: no metadata.name identity in $entity" >&2
    return 1
  fi
  leaf="${name##*/}"
  if [ -n "$WASM_DIR" ]; then
    wasm="$WASM_DIR/$leaf.wasm"
  else
    wasm="$(dirname "$entity")/$leaf.wasm"
  fi
  if [ ! -f "$wasm" ]; then
    echo "publish-plugins: guest .wasm for $leaf not found at $wasm (set --wasm-dir or co-locate it)" >&2
    return 1
  fi
  printf '%s\n' "$wasm"
}

# extract_reference reads captured `aggrega push` stdout and prints the resolved
# <registry>/<path>:<tag> reference. "Push complete:" and "Already published:"
# carry it as the third field; the "aggrega push (dry-run):" line as the fourth.
extract_reference() {
  awk '
    /^Push complete: /            { print $3; exit }
    /^Already published: /        { print $3; exit }
    /^aggrega push \(dry-run\): / { print $4; exit }
  '
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

# --- Non-interactive signing discipline --------------------------------------
# Refuse to sign a real publish without signing material, so the keyless flow can
# never fall back to an interactive browser prompt. A dry-run signs nothing.
if [ -n "$SIGN" ] && [ -z "$DRYRUN" ]; then
  if [ -z "$KEY" ] && [ -z "$IDTOKEN" ] && [ -z "${AGGREGA_IDENTITY_TOKEN:-}" ] && [ -z "${SIGSTORE_ID_TOKEN:-}" ]; then
    echo "publish-plugins: --sign: no signing material; pass --key <path> for offline keyed signing, or --identity-token / AGGREGA_IDENTITY_TOKEN / SIGSTORE_ID_TOKEN for non-interactive keyless signing" >&2
    exit 2
  fi
fi

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
  cmd=("$AGGREGA" push --plugin "$wasm" "$f" --registry "$REGISTRY")
  [ -n "$DRYRUN" ] && cmd+=(--dry-run)
  if [ -n "$SIGN" ]; then
    cmd+=(--sign)
    [ -n "$KEY" ]     && cmd+=(--key "$KEY")
    [ -n "$IDTOKEN" ] && cmd+=(--identity-token "$IDTOKEN")
  fi
  # Capture stdout to recover the resolved reference, then echo it so the push
  # output stays visible. The `if` guards the command substitution against set -e
  # while testing push's exit status.
  if out="$("${cmd[@]}")"; then
    printf '%s\n' "$out"
    pushed=$((pushed + 1))
    ref="$(printf '%s\n' "$out" | extract_reference)"
    [ -n "$ref" ] && REFS+=("$ref")
  else
    printf '%s\n' "$out"
    echo "publish-plugins: PUSH FAILED: $f" >&2
    failed=$((failed + 1))
  fi
done

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
