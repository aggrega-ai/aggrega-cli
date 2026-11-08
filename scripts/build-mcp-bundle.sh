#!/usr/bin/env bash
# aggrega-cli/scripts/build-mcp-bundle.sh — build the installable MCP bundle.
#
# Validates the bundle manifest under extensions/mcpb/ and packs it into one
# installable file, aggrega-<version>.mcpb, written into a directory outside
# this repository's working tree. A client that takes a local Model Context
# Protocol server as an extension package — Claude Desktop among them — adds
# that file from its own extension settings. The bundle carries no Aggrega
# program: its manifest launches the aggrega executable already installed on
# the machine, so one bundle serves every platform the manifest declares.
#
# This script is the whole procedure, run the same way from a terminal and from
# the release workflow, so a local build and a release build cannot differ.
#
# THE VERSION IS STAMPED, never read from the checked-in manifest. The manifest
# in the repository carries a placeholder `version`; this script replaces it in
# a staged copy with the build-provenance version every other stamped output of
# this repository carries, taken from the one shared recipe this script sources
# below: the release tag on a clean checkout of a tagged commit, a development
# form anywhere else. The staged copy lives in a
# temporary directory and holds the stamped manifest and nothing else; that
# directory is what is validated and packed, so the source directory is never
# modified.
#
# THE SAME VERSION IS HANDED TO THE SERVER. The manifest's launch environment
# carries AGGREGA_BUNDLE_VERSION, which the server reads at launch and reports
# beside its own version, saying when the two come from different releases. The
# checked-in entry carries the placeholder the `version` field carries, and this
# script writes the stamped version into it in the same staged copy, so the
# bundle and the variable it sets name one version. A manifest whose launch
# environment carries no such entry is refused, naming it, rather than built
# into a bundle whose server cannot tell which release launched it.
#
# The recipe makes outputs stamped from one tree at one moment agree; it does
# not make a bundle built now agree with a binary built earlier. A caller that
# needs the two to match passes --expect-version with the version the binary
# reports, and the script refuses, naming both, when the stamp differs.
#
# The packer is the bundle format's own, the npm package @anthropic-ai/mcpb,
# run through npx at the exact version pinned below — never whatever is latest
# at the moment of the build. Node.js and npx must be on PATH; the first run
# fetches the packer into the npm cache.
#
# The source directory must hold manifest.json and nothing else. The bundle is
# the manifest alone, so any other entry is refused by name rather than silently
# left out of the built file.
#
# EVERY ${user_config.KEY} THE MANIFEST WRITES MUST NAME A SETTING IT DECLARES
# under user_config. A client fills each such placeholder from the setting of
# that name when it launches the server, so one naming no setting has nothing
# to be filled from. The packer pinned below accepts such a manifest, at
# validation and at pack, so this script checks it itself, before the packer
# runs, and refuses naming each undeclared key and where the manifest writes it.
#
# Usage:
#   scripts/build-mcp-bundle.sh --output-dir <dir> [options]
#
# Options:
#   --output-dir <dir>      Directory the bundle is written into, created when
#                           missing. It must lie outside this repository's
#                           working tree: the bundle is a build product.
#   --expect-version <v>    Refuse unless the stamped version equals <v>
#                           exactly (v-less, as the binary reports it).
#   --source-dir <dir>      Directory holding the bundle manifest (default:
#                           extensions/mcpb under this repository's root).
#   -h, --help              Show this help.
#
# On success the script prints exactly one line on standard output, the path of
# the written bundle, so a caller can capture it; the packer's own report goes
# to standard error. It exits 1 when the manifest fails validation (the
# validator's message is shown), when it writes a ${user_config.KEY} naming a
# setting it does not declare (the key is named), when its launch environment
# carries no AGGREGA_BUNDLE_VERSION entry, when the stamped version differs from
# --expect-version, when the source directory holds anything but the manifest,
# or when packing fails; and 2 on a usage error.
#
# Exit non-zero on the first failing step.
set -euo pipefail

# The packer, at one exact version. Raise it deliberately: validate
# extensions/mcpb/manifest.json under the new version before changing it here,
# and re-read how the new version's resolver treats a required setting and a
# setting's default. Both bundle settings rely on it: each is not required and
# carries a default, because this version substitutes a default only where
# nothing is stored and builds no launch while a required setting has nothing
# stored.
MCPB_PACKER="@anthropic-ai/mcpb@2.1.2"

# The launch-environment entry the stamped version is written into. The manifest
# spells it as text, and a Go test holds that spelling to the one the binary
# reads (workspaceconfig.EnvBundleVersion); this script refuses a manifest that
# does not carry it, so its own spelling is held to the manifest's in turn.
BUNDLE_VERSION_ENV="AGGREGA_BUNDLE_VERSION"

CLI_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"

die()   { printf 'build-mcp-bundle: %s\n' "$*" >&2; exit 1; }
usage() { printf 'build-mcp-bundle: %s\n' "$*" >&2; exit 2; }

OUTPUT_DIR=""
EXPECT_VERSION=""
SOURCE_DIR="$CLI_ROOT/extensions/mcpb"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-dir)
      [ -n "${2:-}" ] || usage "--output-dir needs a directory"
      OUTPUT_DIR="$2"; shift 2 ;;
    --expect-version)
      [ -n "${2:-}" ] || usage "--expect-version needs a version"
      EXPECT_VERSION="$2"; shift 2 ;;
    --source-dir)
      [ -n "${2:-}" ] || usage "--source-dir needs a directory"
      SOURCE_DIR="$2"; shift 2 ;;
    # Ends at the header's own closing line, the same sentinel and the same
    # two-stage form as scripts/build.sh.
    -h|--help) sed -n '2,/^# Exit non-zero on the first failing step\.$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) usage "unknown argument: $1" ;;
  esac
done

[ -n "$OUTPUT_DIR" ] || usage "--output-dir is required: a directory outside the working tree"
command -v node >/dev/null 2>&1 || die "node is not on PATH; the packer runs on Node.js"
command -v npx  >/dev/null 2>&1 || die "npx is not on PATH; the packer is run through npx"

# Resolve the caller's paths against the directory the caller ran from, before
# anything changes directory.
abspath() { node -e 'process.stdout.write(require("path").resolve(process.argv[1]))' "$1"; }
OUTPUT_DIR="$(abspath "$OUTPUT_DIR")"
SOURCE_DIR="$(abspath "$SOURCE_DIR")"

case "$OUTPUT_DIR/" in
  "$CLI_ROOT/"*) die "--output-dir $OUTPUT_DIR lies inside the working tree $CLI_ROOT; name a directory outside it" ;;
esac

[ -d "$SOURCE_DIR" ] || die "source directory not found: $SOURCE_DIR"
[ -f "$SOURCE_DIR/manifest.json" ] || die "no manifest.json in $SOURCE_DIR"
extras=()
shopt -s dotglob nullglob
for entry in "$SOURCE_DIR"/*; do
  [ "$(basename "$entry")" = "manifest.json" ] || extras+=("$(basename "$entry")")
done
shopt -u dotglob nullglob
[ "${#extras[@]}" -eq 0 ] || die "$SOURCE_DIR holds more than the manifest (${extras[*]}); the bundle is the manifest alone, so remove the other entries"

# The stamped version (_ver) comes from the one shared recipe, sourced from the
# repository root so it resolves against this working tree.
cd "$CLI_ROOT"
. scripts/lib/build-version.sh

if [ -n "$EXPECT_VERSION" ] && [ "$_ver" != "$EXPECT_VERSION" ]; then
  die "the stamped version '$_ver' does not equal the expected version '$EXPECT_VERSION'"
fi

STAGE="$(mktemp -d "${TMPDIR:-/tmp}/aggrega-mcp-bundle.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT
mkdir "$STAGE/bundle"

# Write the stamped copy: the source manifest with its `version` and its
# launch environment's bundle-version entry both set to the stamped version, and
# every key left in its authored order.
node -e '
  const fs = require("fs");
  const [src, version, dst, envName] = process.argv.slice(1);
  let manifest;
  try {
    manifest = JSON.parse(fs.readFileSync(src, "utf8"));
  } catch (err) {
    process.stderr.write("build-mcp-bundle: " + src + " is not valid JSON: " + err.message + "\n");
    process.exit(1);
  }
  if (manifest === null || typeof manifest !== "object" || Array.isArray(manifest)) {
    process.stderr.write("build-mcp-bundle: " + src + " does not hold a JSON object\n");
    process.exit(1);
  }
  const launch = manifest.server && manifest.server.mcp_config;
  const env = launch && launch.env;
  if (env === null || typeof env !== "object" || Array.isArray(env) ||
      !Object.prototype.hasOwnProperty.call(env, envName)) {
    process.stderr.write("build-mcp-bundle: " + src + " carries no server.mcp_config.env." + envName +
      ", the variable the bundle hands the server its own version through: declare it there, with the " +
      "placeholder the version field carries, and this script writes the stamped version into both\n");
    process.exit(1);
  }
  manifest.version = version;
  env[envName] = version;
  fs.writeFileSync(dst, JSON.stringify(manifest, null, 2) + "\n");
' "$SOURCE_DIR/manifest.json" "$_ver" "$STAGE/bundle/manifest.json" "$BUNDLE_VERSION_ENV" || exit 1

# Refuse a ${user_config.KEY} naming a setting the manifest does not declare.
# Every string the manifest holds is read, wherever it sits, because a client
# fills a placeholder wherever the server's launch carries one; each undeclared
# key is named with the place it is written, in the manifest's own key path.
node -e '
  const fs = require("fs");
  const [src, staged] = process.argv.slice(1);
  const manifest = JSON.parse(fs.readFileSync(staged, "utf8"));
  const declared = manifest.user_config && typeof manifest.user_config === "object" ? manifest.user_config : {};
  const undeclared = [];
  const walk = (value, where) => {
    if (typeof value === "string") {
      for (const m of value.matchAll(/\$\{user_config\.([^}]*)\}/g)) {
        if (!Object.prototype.hasOwnProperty.call(declared, m[1])) undeclared.push([m[1], where]);
      }
    } else if (Array.isArray(value)) {
      value.forEach((v, i) => walk(v, where + "[" + i + "]"));
    } else if (value !== null && typeof value === "object") {
      for (const k of Object.keys(value)) walk(value[k], where === "" ? k : where + "." + k);
    }
  };
  walk(manifest, "");
  for (const [key, where] of undeclared) {
    process.stderr.write("build-mcp-bundle: " + src + " writes ${user_config." + key + "} at " + where +
      ", and its user_config declares no setting with the key " + key +
      ": declare that setting, or name one the manifest declares\n");
  }
  process.exit(undeclared.length === 0 ? 0 : 1);
' "$SOURCE_DIR/manifest.json" "$STAGE/bundle/manifest.json" || exit 1

# The packer runs from the staging directory, never from inside the working
# tree, and reports on standard error so standard output carries only the path.
printf 'build-mcp-bundle: validating the manifest with %s, version %s\n' "$MCPB_PACKER" "$_ver" >&2
if ! (cd "$STAGE" && npx --yes "$MCPB_PACKER" validate bundle/manifest.json) >&2; then
  die "the manifest in $SOURCE_DIR failed validation; the validator's message is above"
fi

mkdir -p "$OUTPUT_DIR"
OUT_FILE="$OUTPUT_DIR/aggrega-$_ver.mcpb"
printf 'build-mcp-bundle: packing %s\n' "$OUT_FILE" >&2
if ! (cd "$STAGE" && npx --yes "$MCPB_PACKER" pack bundle "$OUT_FILE") >&2; then
  die "packing failed; the packer's message is above"
fi
[ -f "$OUT_FILE" ] || die "the packer reported success but wrote no file at $OUT_FILE"

printf '%s\n' "$OUT_FILE"
