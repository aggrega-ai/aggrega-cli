#!/usr/bin/env bash
# aggrega-cli/scripts/check-plugin-notices-compat.sh — prove that a released
# aggrega still installs a plugin published with its third-party notices and its
# bill of materials.
#
# A Plugin published with `aggrega push --plugin-notices` carries its guest's
# notices as a second layer of its manifest, after the .wasm, and one published
# with `--plugin-sbom` has an SPDX bill of materials attached as an
# application/spdx+json referrer. A binary that predates both reads the .wasm as
# the manifest's first layer and ignores whatever follows it. This script holds
# that to the released 0.9.3 binary. It:
#
#   1. builds the aggrega of this checkout, and go-containerregistry's in-memory
#      registry program from the module go.mod requires, and starts the registry
#      on a free port of the loopback address
#   2. downloads the 0.9.3 binary for this machine from the aggrega-ai/aggrega-cli
#      release v0.9.3 and refuses it unless its sha256 is the one that release's
#      SHA256SUMS lists
#   3. builds a small guest (the cli-multi-guest fixture of cmd/aggrega), writes
#      its notices and bill of materials with scripts/gen-third-party-licenses.sh
#      --guest, and pushes it as a Plugin with --plugin-notices and --plugin-sbom
#      using the binary of this checkout; the manifest must list the .wasm and
#      then the notices, and the registry must hold one SPDX referrer
#   4. has the 0.9.3 binary install that plugin as a user does, with
#      `aggrega plugin add cli`, and compares the sha256 of the .wasm it cached with
#      that of the .wasm pushed
#   5. plants the fault the check must be able to see: it pushes a second Plugin
#      from the same blobs whose manifest lists the notices before the .wasm, and
#      requires the 0.9.3 install of that one to fail
#
# It exits 0 only when step 4 installed the pushed bytes and step 5's install
# failed. Otherwise it exits non-zero naming the half that did not hold: "real
# case" or "planted case". A step that cannot run, a download, a checksum, a build
# or the registry, exits non-zero naming why; that is never a pass.
#
# It needs a Go toolchain with the modules go.mod requires, the embedded guests
# in place (scripts/build.sh writes them), curl, jq, and oras to write the planted manifest. It
# fetches nothing but the release from GitHub and what the module proxy serves for
# the builds. Everything it writes is under one temporary directory, removed on
# exit; the registry it starts is stopped on exit. It writes nothing in the module.
#
# Run it by hand, from any directory, under the host's heavy-command guard where
# one is in use:
#
#   scripts/check-plugin-notices-compat.sh
#
# Linux and macOS on amd64 or arm64.
set -euo pipefail

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export GOWORK=off

RELEASE_TAG="v0.9.3"
RELEASE_VERSION="0.9.3"
RELEASE_URL="https://github.com/aggrega-ai/aggrega-cli/releases/download/$RELEASE_TAG"
# A deliberate independent copy of how stage-platform-smoke.sh (REGISTRY_PROGRAM)
# and platform-smoke.sh (`-port 0` and the "serving on port" parse, below) build and
# start this registry: those stage a prebuilt program for another machine, this
# builds and starts it in one run. A change to one is a reason to look at the others.
REGISTRY_PROGRAM="github.com/google/go-containerregistry/cmd/registry"
GUEST_FIXTURE="cmd/aggrega/testdata/cli-multi-guest"
PLUGIN_NAME="notices-compat"
PLANTED_NAME="notices-planted"
PLUGIN_VERSION="1.0.0"

MEDIA_ENTITY="application/vnd.aggrega.wasmplugin.v1beta1+yaml"
MEDIA_WASM="application/vnd.aggrega.wasm.v1beta1+wasm"
MEDIA_NOTICES="application/vnd.aggrega.wasmplugin.notices.v1beta1+markdown"
MEDIA_SPDX="application/spdx+json"

fail() { printf 'check-plugin-notices-compat: %s\n' "$*" >&2; }
cannot() { fail "cannot run the check: $*"; exit 1; }

TMP="$(mktemp -d)"
REGISTRY_PID=""
# shellcheck disable=SC2329 # run by the EXIT trap
cleanup() {
  [ -z "$REGISTRY_PID" ] || kill "$REGISTRY_PID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

for tool in curl jq oras go; do
  command -v "$tool" >/dev/null || cannot "$tool is not installed"
done

sha256_of() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) PLATFORM="linux-amd64" ;;
  Linux-aarch64 | Linux-arm64) PLATFORM="linux-arm64" ;;
  Darwin-x86_64) PLATFORM="darwin-amd64" ;;
  Darwin-arm64) PLATFORM="darwin-arm64" ;;
  *) cannot "$(uname -s) $(uname -m) has no released binary this check runs" ;;
esac

# Nothing below may reach a proxy for the loopback registry, and nothing may read
# this machine's configuration or caches.
export NO_PROXY="localhost,127.0.0.1" no_proxy="localhost,127.0.0.1"
unset AGGREGA_CONFIG AGGREGA_OFFLINE
export HOME="$TMP/home" XDG_CACHE_HOME="$TMP/cache" XDG_CONFIG_HOME="$TMP/config"
mkdir -p "$HOME"

echo "==> building the aggrega of this checkout and the registry program"
CURRENT="$TMP/aggrega-current"
REGISTRY="$TMP/registry"
( cd "$CLI_ROOT" && go build -o "$CURRENT" ./cmd/aggrega ) || cannot "the build of this checkout failed"
( cd "$CLI_ROOT" && go build -o "$REGISTRY" "$REGISTRY_PROGRAM" ) || cannot "the build of the registry program failed"

echo "==> the released $RELEASE_VERSION binary for $PLATFORM, checked against $RELEASE_TAG's SHA256SUMS"
ASSET="aggrega-$RELEASE_VERSION-$PLATFORM"
RELEASED="$TMP/$ASSET"
curl -fsSL -o "$TMP/SHA256SUMS" "$RELEASE_URL/SHA256SUMS" || cannot "SHA256SUMS of $RELEASE_TAG could not be downloaded"
curl -fsSL -o "$RELEASED" "$RELEASE_URL/$ASSET" || cannot "$ASSET could not be downloaded"
want="$(awk -v a="$ASSET" '$2 == a || $2 == "*" a { print $1 }' "$TMP/SHA256SUMS")"
[ -n "$want" ] && [ "$(printf '%s\n' "$want" | wc -l)" = 1 ] || cannot "SHA256SUMS lists $ASSET not exactly once"
got="$(sha256_of "$RELEASED")"
[ "$got" = "$want" ] || cannot "$ASSET has sha256 $got; $RELEASE_TAG's SHA256SUMS lists $want; refusing it"
chmod +x "$RELEASED"
reported="$("$RELEASED" version | head -n1)"
case "$reported" in
  "aggrega $RELEASE_VERSION "*) echo "  $reported" ;;
  *) cannot "the downloaded binary reports \"$reported\", not $RELEASE_VERSION" ;;
esac

echo "==> the registry"
: > "$TMP/registry.log"
"$REGISTRY" -port 0 >> "$TMP/registry.log" 2>&1 &
REGISTRY_PID=$!
PORT=""
for ((i = 0; i < 200; i++)); do
  if [[ "$(< "$TMP/registry.log")" =~ serving\ on\ port\ ([0-9]+) ]]; then
    PORT="${BASH_REMATCH[1]}"
    break
  fi
  sleep 0.1
done
[ -n "$PORT" ] || { cat "$TMP/registry.log" >&2; cannot "the registry program named no port within 20 seconds"; }
HOST="localhost:$PORT"
echo "  serving on $HOST"

echo "==> a guest with its notices and bill of materials"
SRC="$TMP/guest-src"
mkdir -p "$SRC" "$TMP/gen"
cp "$CLI_ROOT/$GUEST_FIXTURE"/{go.mod,go.sum,main.go} "$SRC/"
# The fixture is a module of its own with no license file, which the generator
# would list as a third-party dependency; as a first-party module it is excluded.
sed -i.bak 's#^module .*#module github.com/aggrega-ai/notices-compat-guest#' "$SRC/go.mod" && rm -f "$SRC/go.mod.bak"
( cd "$SRC" && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o "$TMP/guest.wasm" ./ ) || cannot "the guest did not build"
bash "$CLI_ROOT/scripts/gen-third-party-licenses.sh" --guest "$SRC" --out "$TMP/gen" >"$TMP/gen.log" 2>&1 ||
  { cat "$TMP/gen.log" >&2; cannot "the notices and bill of materials were not generated"; }
NOTICES="$TMP/gen/notices.md"
SBOM="$TMP/gen/sbom.spdx.json"
[ -s "$NOTICES" ] && [ -s "$SBOM" ] || cannot "the generator wrote no notices or no bill of materials"
WASM_SHA="$(sha256_of "$TMP/guest.wasm")"

PUB="$TMP/publish"
mkdir -p "$PUB"
cat > "$PUB/plugin.yaml" <<YAML
apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: $PLUGIN_NAME
  pluginType: cli
  version: "$PLUGIN_VERSION"
spec:
  abiVersion: v1beta1
YAML
"$CURRENT" plugin sync "$PUB/plugin.yaml" --plugin "$TMP/guest.wasm" >"$TMP/sync.log" 2>&1 ||
  { cat "$TMP/sync.log" >&2; cannot "aggrega plugin sync did not write the entity's records"; }

echo "==> the real case: push with --plugin-notices and --plugin-sbom"
"$CURRENT" --offline=false --workspace-root "$PUB" push "$PUB/plugin.yaml" --plugin "$TMP/guest.wasm" \
  --plugin-notices "$NOTICES" --plugin-sbom "$SBOM" --registry "$HOST" >"$TMP/push.log" 2>&1 ||
  { cat "$TMP/push.log" >&2; cannot "the push with notices and bill of materials did not succeed"; }
REF="$HOST/plugin/cli/$PLUGIN_NAME:$PLUGIN_VERSION"
oras manifest fetch --plain-http "$REF" > "$TMP/manifest.json" || { fail "real case: the pushed manifest could not be read back"; exit 1; }
layers="$(jq -r '[.layers[].mediaType] | join(" ")' "$TMP/manifest.json")"
[ "$layers" = "$MEDIA_WASM $MEDIA_NOTICES" ] || { fail "real case: the pushed manifest lists layers \"$layers\", not the .wasm then the notices"; exit 1; }
referrers="$(oras discover --plain-http --artifact-type "$MEDIA_SPDX" --format json "$REF" | jq "(.manifests // .referrers // []) | length")"
[ "$referrers" = 1 ] || { fail "real case: the registry holds $referrers $MEDIA_SPDX referrers of the pushed Plugin, not one"; exit 1; }
echo "  manifest layers: .wasm, notices; one $MEDIA_SPDX referrer"

# install_with_released <name> <workspace> — the released binary installs <name>
# the way a user does, from the registry, into a workspace of its own.
install_with_released() {
  local name="$1" ws="$2"
  mkdir -p "$ws"
  printf 'default_registry: "%s"\noffline: false\n' "$HOST" > "$ws/.aggrega-config.yaml"
  "$RELEASED" --offline=false --workspace-root "$ws" plugin add cli "$name:$PLUGIN_VERSION" --workspace --yes \
    >"$ws.log" 2>&1
}

REAL_OK=0 PLANTED_OK=0

echo "==> the real case: $RELEASE_VERSION installs it"
if install_with_released "$PLUGIN_NAME" "$TMP/ws-real"; then
  blob="$(find "$TMP/ws-real/.aggrega/cache" -type f -name blob | head -n1)"
  if [ -n "$blob" ] && [ "$(sha256_of "$blob")" = "$WASM_SHA" ]; then
    echo "  installed .wasm sha256 $(sha256_of "$blob") equals the pushed .wasm's"
    REAL_OK=1
  else
    cat "$TMP/ws-real.log" >&2
    fail "real case: $RELEASE_VERSION installed, but the cached .wasm (${blob:-none}) is not the pushed one, sha256 $WASM_SHA"
  fi
else
  cat "$TMP/ws-real.log" >&2
  fail "real case: $RELEASE_VERSION failed to install the plugin published with notices"
fi

echo "==> the planted case: a manifest listing the notices before the .wasm"
PLANT="$TMP/plant"
mkdir -p "$PLANT"
cp "$TMP/guest.wasm" "$PLANT/entity.wasm"
cp "$NOTICES" "$PLANT/notices.md"
oras blob fetch --plain-http "$HOST/plugin/cli/$PLUGIN_NAME@$(jq -r .config.digest "$TMP/manifest.json")" \
  --output "$PLANT/entity.yaml" >/dev/null || cannot "the entity blob could not be read back"
( cd "$PLANT" && oras push --plain-http "$HOST/plugin/cli/$PLANTED_NAME:$PLUGIN_VERSION" \
    --artifact-type "$MEDIA_ENTITY" --config "entity.yaml:$MEDIA_ENTITY" \
    --annotation ai.aggrega.kind=Plugin --annotation ai.aggrega.plugin-type=cli \
    "notices.md:$MEDIA_NOTICES" "entity.wasm:$MEDIA_WASM" ) >"$TMP/plant.log" 2>&1 ||
  { cat "$TMP/plant.log" >&2; cannot "the planted manifest was not pushed"; }
planted_layers="$(oras manifest fetch --plain-http "$HOST/plugin/cli/$PLANTED_NAME:$PLUGIN_VERSION" | jq -r '[.layers[].mediaType] | join(" ")')"
[ "$planted_layers" = "$MEDIA_NOTICES $MEDIA_WASM" ] || cannot "the planted manifest lists \"$planted_layers\", not the notices then the .wasm"
if install_with_released "$PLANTED_NAME" "$TMP/ws-planted"; then
  fail "planted case: $RELEASE_VERSION installed a manifest listing the notices first, so the real case's pass proves nothing"
elif grep -q 'wasm binary magic mismatch' "$TMP/ws-planted.log"; then
  echo "  $RELEASE_VERSION refused it for the reason a non-wasm first layer gives: $(grep 'wasm binary magic mismatch' "$TMP/ws-planted.log" | tail -n1)"
  PLANTED_OK=1
else
  cat "$TMP/ws-planted.log" >&2
  fail "planted case: the install failed, but not with 0.9.3's \"wasm binary magic mismatch\" for a non-wasm first layer"
fi

if [ "$REAL_OK" = 1 ] && [ "$PLANTED_OK" = 1 ]; then
  echo "ok: $RELEASE_VERSION installs a plugin published with notices and refuses the planted ordering"
  exit 0
fi
[ "$REAL_OK" = 1 ] || fail "the real case did not hold"
[ "$PLANTED_OK" = 1 ] || fail "the planted case did not hold"
exit 1
