#!/usr/bin/env bash
# aggrega-cli/scripts/check-embedded-overlays.sh
#
# Asserts that every core plugin declared in the core-plugin list is consistent
# with the sibling aggrega-hub catalog inputs and outputs, whether or not the
# binary embeds it. For each list entry it checks:
#   (a) a matching authored overlay is present under plugin-overlays/,
#   (b) its built assets (the gzip-compressed guest and its describe()
#       descriptor, in the entry's artifact directory) are present,
#   (c) the committed core-plugin catalog carries a version directory matching
#       the current compiler build version, and
#   (d) the contract the committed catalog publishes for a plugin still says what
#       that plugin's guest declares in its describe() descriptor.
#
# This guard is READ-ONLY on aggrega-hub: it inspects overlay and catalog
# presence and content only and never writes there, and it never regenerates the
# catalog. The catalog regeneration is a separate, deliberate release/maintenance
# step.
#
# Checks (a) and (b) are hard failures in every mode: a manifest entry with no
# overlay or no built artifact is a real drift between the declarative source and
# its inputs/outputs. Check (c) — catalog version currency — is a
# release/publish-time concern: the committed catalog is regenerated deliberately
# at publish points and legitimately trails HEAD during ordinary development, so
# it is a hard failure only under --release and an informational note otherwise.
#
# Check (d) compares CONTENT, which (a)-(c) do not: those assert that files exist
# and that a version directory is named, never that the bytes inside still agree.
# A published entity's contract block is machine-written from the guest's own
# describe() self-report, so once a guest's declaration moves and the catalog has
# not been republished, the two say different things and nothing else reads both.
# Generated reference material projects provider capability from each side of
# that pair, so a disagreement between them makes one projection wrong with
# nothing able to see it.
#
# (d) tiers exactly as (c) does and for the same reason, quoted from (c) above
# because the reasoning carries over unchanged: the committed catalog is
# regenerated deliberately at publish points and legitimately trails HEAD during
# ordinary development. A contract difference is therefore a hard failure only
# under --release and an informational note otherwise. A check that blocked in
# development would fail on every commit between publications; one that never
# blocks is the state that let a published contract and its guest drift apart
# unobserved. Outside --release the comparison is against the highest-versioned
# committed entity, which is what the catalog most recently published; under
# --release it is against the entity for the build being released, whose presence
# (c) has already asserted.
#
# (d) compares NAMED FIELDS, never a whole-file digest. The published entity
# carries a build-provenance version stamp the descriptor does not, so a
# whole-file comparison reports staleness on any tree whose version string moved
# without a contract change — the same reasoning check-embedded-plugin-entities.sh
# records for its own single-field test, where comparing exactly one field "is
# immune to the false staleness a whole-file digest would suffer". Every field the
# contract block declares is walked on both sides rather than matched against a
# written list of field names, so a field added to the contract is compared
# without this script being edited. Neither side writes a field it declares false,
# so both are reduced to their true-valued fields before comparison; a flip in
# either direction still shows up as a field one side declares and the other does
# not.
#
# WHAT (d) DOES NOT COVER, because the gap is structural rather than an omission.
# The well-known annotation support a provider claims is hand-authored on its
# overlay and appears in no guest descriptor at all, so no comparison against a
# guest can verify it and nothing here watches it. (d) also reads the contract
# block alone — not the export list, the runtime limits, or the capability list.
# A clean (d) therefore means the published contract agrees with its guest, and
# does not mean the published entity as a whole has been checked against one.
#
# Usage:
#   check-embedded-overlays.sh            # (a)/(b) hard; (c)/(d) informational
#   check-embedded-overlays.sh --release  # (a)/(b)/(c)/(d) all hard
#
# Exit 0 when consistent; exit 1 (naming each offending entry) otherwise; exit 3
# when a contract carries a shape this script does not model, which is reported
# rather than skipped because a reader that goes quiet on an input it cannot
# parse reports agreement over a comparison that never ran.
# Deterministic and offline.
set -euo pipefail
cd "$(dirname "$0")/.."          # aggrega-cli module root
SIBLINGS_DIR="$(cd ../ && pwd)"
HUB="$SIBLINGS_DIR/aggrega-hub"

RELEASE=0
for arg in "$@"; do
  case "$arg" in
    --release) RELEASE=1 ;;
    *) echo "check-embedded-overlays: unknown argument: $arg" >&2; exit 2 ;;
  esac
done

# The core-plugin list's one reader: core_plugin_rows and the artifact directories.
. scripts/lib/core-plugins.sh
rows="$(core_plugin_rows)" || exit 1
OVERLAY_DIR="$HUB/plugin-overlays"
CATALOG_DIR="$HUB/aggrega-catalog/plugin"
[ -d "$HUB" ] || { echo "check-embedded-overlays: aggrega-hub not found at $HUB (this check reads the overlays and catalog entities of the aggrega-hub repository checked out beside this one; check it out there first)" >&2; exit 1; }

# BUILD_VER is the version check (c) EXPECTS to find as a committed version
# directory. Nothing here stamps anything: it is HEAD's own `git describe`, with
# a leading "v" stripped and no working-tree --dirty marker.
#
# Two things this deliberately does not claim. It is NOT how the catalog
# generator derives a version directory — that is the guest's own describe()
# self-report — so the two diverge whenever the guests were not rebuilt at HEAD.
# And omitting the --dirty marker confers no immunity from an edited tree: a
# guest built from a tree with uncommitted edits reports a "-dirty" version and
# stamps a "-dirty" directory, which this expected value would not match.
#
# Both divergences are why (c) is only an informational note outside --release.
# Under --release the guests are rebuilt from the clean, tagged commit, so the
# descriptor version and HEAD's `git describe` converge on the tag: the two
# readings agree at the one moment the check is a hard failure.
BUILD_VER="$(git describe --tags --always 2>/dev/null || echo dev)"; BUILD_VER="${BUILD_VER#v}"

# --- (d) helpers ------------------------------------------------------------
#
# Both flatteners reduce a contract to sorted "path=value" lines in one dotted,
# snake_case key space, so a descriptor written in JSON and an entity written in
# YAML become directly comparable without either side being transcribed into a
# list of expected field names. A list of scalars is one line, its items quoted
# and kept in the order written — `path=["a", "b"]` — because a contract list is
# an ordered declaration and two sides listing the same items in another order
# say different things. Each flattener refuses loudly, naming the construct, on a
# shape it does not model: a list holding an object or another list.

# contract_fields_from_descriptor DESCRIPTOR — the contract a guest's describe()
# descriptor declares. Emits nothing when the descriptor declares no contract,
# which is the ordinary case for a plugin type that has none.
contract_fields_from_descriptor() {
  awk '
    function bail(msg) {
      print "check-embedded-overlays: cannot read the guest contract in " FILENAME ": " msg > "/dev/stderr"
      exit 3
    }
    { doc = doc $0 }
    END {
      i = index(doc, "\"contract\":")
      if (i == 0) exit 0
      s = substr(doc, i + 11)
      if (substr(s, 1, 1) != "{") bail("the contract is not an object")

      # Balanced, quote- and escape-aware extraction of the contract object.
      depth = 0; inq = 0; esc = 0; last = 0
      n = length(s)
      for (p = 1; p <= n; p++) {
        c = substr(s, p, 1)
        if (esc) { esc = 0; continue }
        if (c == "\\") { esc = 1; continue }
        if (c == "\"") { inq = 1 - inq; continue }
        if (inq) continue
        if (c == "{") depth++
        else if (c == "}") { depth--; if (depth == 0) { last = p; break } }
      }
      if (last == 0) bail("the contract object is unterminated")
      emit(substr(s, 1, last))
    }

    # emit OBJ — tokenize the object, then walk the token stream with a prefix
    # stack. Two passes rather than one so the key/value distinction is decided
    # by lookahead on the next token instead of by nested parser state.
    function emit(o,   p, n, c, inq, esc, buf, nt, i, prefix, key, sp, inlist, items) {
      n = length(o); inq = 0; esc = 0; buf = ""; nt = 0
      for (p = 1; p <= n; p++) {
        c = substr(o, p, 1)
        if (inq) {
          if (esc) { buf = buf c; esc = 0; continue }
          if (c == "\\") { esc = 1; continue }
          if (c == "\"") { inq = 0; nt++; ttype[nt] = "v"; tval[nt] = buf; buf = ""; continue }
          buf = buf c; continue
        }
        if (c == "\"") { inq = 1; buf = ""; continue }
        if (c == "{" || c == "}" || c == ":" || c == "," || c == "[" || c == "]") {
          if (buf != "") { nt++; ttype[nt] = "v"; tval[nt] = buf; buf = "" }
          nt++; ttype[nt] = "p"; tval[nt] = c
          continue
        }
        if (c == " " || c == "\t") continue
        buf = buf c
      }
      if (buf != "") { nt++; ttype[nt] = "v"; tval[nt] = buf }

      sp = 0; prefix = ""; key = ""; inlist = 0; items = ""
      for (i = 1; i <= nt; i++) {
        # A list of scalars is gathered whole and emitted as one line, its items
        # in the order written.
        if (inlist) {
          if (ttype[i] == "v") { items = items (items == "" ? "" : ", ") "\"" tval[i] "\""; continue }
          if (tval[i] == ",") continue
          if (tval[i] == "]") {
            print (prefix == "" ? key : prefix "." key) "=[" items "]"
            inlist = 0; items = ""; key = ""
            continue
          }
          bail("a list holding an object or another list inside the contract is not modelled")
        }
        if (ttype[i] == "p") {
          if (tval[i] == "[") {
            if (key == "") bail("a contract list with no key")
            inlist = 1; items = ""
            continue
          }
          if (tval[i] == "]") bail("a contract list closed that was never opened")
          if (tval[i] == "{") {
            if (key != "") {
              sp++; stack[sp] = prefix
              prefix = (prefix == "" ? key : prefix "." key)
              key = ""
            }
          } else if (tval[i] == "}") {
            if (sp > 0) { prefix = stack[sp]; sp-- }
            key = ""
          }
          continue
        }
        # A token followed by ":" is a key; anything else is a value.
        if (i < nt && ttype[i + 1] == "p" && tval[i + 1] == ":") { key = tval[i]; continue }
        if (key == "") bail("a contract value with no key")
        # A field a side declares false is written by neither side, so it is
        # dropped here and on the entity side alike.
        if (tval[i] != "false") print (prefix == "" ? key : prefix "." key) "=" tval[i]
        key = ""
      }
    }
  ' "$1" | LC_ALL=C sort
}

# contract_fields_from_entity ENTITY — the contract a committed catalog entity
# publishes. The entity is a generated file with one contract block at a fixed
# indent, so the block is read directly rather than through a YAML parser the
# other build scripts do not need.
contract_fields_from_entity() {
  awk '
    function bail(msg) {
      print "check-embedded-overlays: cannot read the published contract in " FILENAME ": " msg > "/dev/stderr"
      exit 3
    }
    # snake S — the entity spells a contract field in camelCase and the
    # descriptor spells the same field in snake_case. The transform is
    # mechanical, so no per-field mapping is kept anywhere.
    function snake(s,   i, c, o) {
      o = ""
      for (i = 1; i <= length(s); i++) {
        c = substr(s, i, 1)
        if (c ~ /[A-Z]/) o = o "_" tolower(c); else o = o c
      }
      return o
    }
    # unquote S — a YAML scalar with its surrounding quotes, if any, removed.
    function unquote(s,   q) {
      if (length(s) > 1) {
        q = substr(s, 1, 1)
        if ((q == "\"" || q == "'"'"'") && substr(s, length(s), 1) == q) s = substr(s, 2, length(s) - 2)
      }
      return s
    }
    { if (done) next }
    !inblock {
      if ($0 ~ /^[ ]+contract:[ ]*$/) { base = match($0, /[^ ]/) - 1; inblock = 1; sp = 0 }
      next
    }
    /^[ ]*$/ { next }
    {
      ind = match($0, /[^ ]/) - 1
      if (ind <= base) { done = 1; next }
      line = substr($0, ind + 1)

      # A list item belongs to the key the nearest enclosing level opened. Its
      # items are gathered in the order written and emitted as one line at the
      # end, in the shape the descriptor side emits.
      if (substr(line, 1, 2) == "- ") {
        # An item may sit at the indent of the key holding it, which YAML allows,
        # so only a level indented deeper than the item is closed by it.
        while (sp > 0 && ind < lvlind[sp]) sp--
        if (sp == 0) bail("a published contract list item with no key: " line)
        path = ""
        for (i = 1; i <= sp; i++) path = (path == "" ? lvlkey[i] : path "." lvlkey[i])
        item = substr(line, 3)
        sub(/^[ ]+/, "", item)
        if (item ~ /^[A-Za-z_][A-Za-z0-9_]*:( |$)/ || substr(item, 1, 2) == "- " || substr(item, 1, 1) == "[" || substr(item, 1, 1) == "{")
          bail("a list holding an object or another list inside the published contract is not modelled: " line)
        if (!(path in listed)) { listed[path] = 1; lists[++nlists] = path }
        listitems[path] = listitems[path] (listitems[path] == "" ? "" : ", ") "\"" unquote(item) "\""
        next
      }

      while (sp > 0 && ind <= lvlind[sp]) sp--
      prefix = ""
      for (i = 1; i <= sp; i++) prefix = (prefix == "" ? lvlkey[i] : prefix "." lvlkey[i])

      ci = index(line, ":")
      if (ci == 0) bail("a published contract line with no key: " line)
      key = snake(substr(line, 1, ci - 1))
      val = substr(line, ci + 1)
      sub(/^[ ]+/, "", val)

      if (val == "") { sp++; lvlind[sp] = ind; lvlkey[sp] = key; next }
      if (val == "[]") { print (prefix == "" ? key : prefix "." key) "=[]"; next }
      if (val == "|" || val == ">" || substr(val, 1, 1) == "[" || substr(val, 1, 1) == "{")
        bail("a published contract value shape that is not a scalar: " line)

      val = unquote(val)
      # A field a side declares false is written by neither side, so it is
      # dropped here and on the descriptor side alike.
      if (val != "false") print (prefix == "" ? key : prefix "." key) "=" val
    }
    END {
      for (i = 1; i <= nlists; i++) print lists[i] "=[" listitems[lists[i]] "]"
    }
  ' "$1" | LC_ALL=C sort
}

# newest_published_entity SOCKET NAME — the highest-versioned committed catalog
# entity for one plugin, which is what the catalog most recently published for
# it. Nothing is echoed when the plugin has never been published.
newest_published_entity() {
  local dir="$CATALOG_DIR/$1/$2" v
  [ -d "$dir" ] || return 0
  v="$(for d in "$dir"/*/; do if [ -f "$d/plugin.yaml" ]; then basename "$d"; fi; done | LC_ALL=C sort -V | tail -1)"
  [ -n "$v" ] || return 0
  printf '%s\n' "$dir/$v/plugin.yaml"
}

# report_contract_diff GUEST_FIELDS ENTITY_FIELDS — one line per field the two
# sides disagree on, naming the field and what each side says. A field only one
# side carries reads "not declared", which is what a side writes for a field it
# declares false or omits entirely.
report_contract_diff() {
  awk -v guestf="$1" '
    FILENAME == guestf { g[substr($0, 1, index($0, "=") - 1)] = substr($0, index($0, "=") + 1); next }
    { e[substr($0, 1, index($0, "=") - 1)] = substr($0, index($0, "=") + 1) }
    END {
      for (k in g) all[k] = 1
      for (k in e) all[k] = 1
      for (k in all) {
        gv = (k in g) ? g[k] : "not declared"
        ev = (k in e) ? e[k] : "not declared"
        if (gv != ev) print "           " k ": catalog says " ev ", guest says " gv
      }
    }
  ' "$1" "$2" | LC_ALL=C sort
}

declare -a missing=()
declare -a stale_catalog=()
declare -a contract_diverged=()
declare -a contract_uncompared=()
contract_declared=0
contract_compared=0

while IFS=$'\x1f' read -r socket name sourcePath toolchain extraRoots embed dir; do
  [ -n "$socket" ] || continue
  entry_ok=1

  # (a) authored overlay present.
  overlay="$OVERLAY_DIR/$socket/$name.yaml"
  if [ ! -f "$overlay" ]; then
    echo "MISSING  $socket/$name — no overlay at plugin-overlays/$socket/$name.yaml"
    missing+=("$socket/$name"); entry_ok=0
  fi

  # (b) built assets present, in the entry's artifact directory.
  wasmgz="$dir/$name.wasm.gz"
  desc="$dir/$name.describe.json"
  if [ ! -f "$wasmgz" ]; then
    echo "MISSING  $socket/$name — no built artifact $wasmgz"
    missing+=("$socket/$name"); entry_ok=0
  fi
  if [ ! -f "$desc" ]; then
    echo "MISSING  $socket/$name — no describe() descriptor $desc"
    missing+=("$socket/$name"); entry_ok=0
  fi

  # (c) committed catalog carries a version directory for the current build.
  # Absence is loud only under --release; between releases it is expected.
  catver="$CATALOG_DIR/$socket/$name/$BUILD_VER/plugin.yaml"
  if [ ! -f "$catver" ]; then
    stale_catalog+=("$socket/$name")
    [ "$RELEASE" = 1 ] && { echo "STALE    $socket/$name — no committed catalog entry for version $BUILD_VER"; entry_ok=0; }
  fi

  # (d) the published contract still says what the guest declares.
  if [ -f "$desc" ]; then
    if ! guest_fields="$(contract_fields_from_descriptor "$desc")"; then
      echo "check-embedded-overlays: $socket/$name — the guest contract could not be read" >&2
      exit 3
    fi
    if [ -n "$guest_fields" ]; then
      contract_declared=$((contract_declared + 1))
      if [ "$RELEASE" = 1 ]; then
        published="$catver"; [ -f "$published" ] || published=""
      else
        published="$(newest_published_entity "$socket" "$name")"
      fi
      if [ -z "$published" ]; then
        # Reported, never silent. The guest declares a contract and no committed
        # entity carries one to compare it against, so no comparison ran — and a
        # check that goes quiet when an input is absent reports clean over a
        # comparison that never ran.
        echo "NOCOMPARE $socket/$name — the guest declares a contract; the committed catalog publishes no entity to compare it against"
        contract_uncompared+=("$socket/$name")
      else
        if ! entity_fields="$(contract_fields_from_entity "$published")"; then
          echo "check-embedded-overlays: $socket/$name — the published contract could not be read" >&2
          exit 3
        fi
        if [ -z "$entity_fields" ]; then
          echo "NOCOMPARE $socket/$name — the guest declares a contract; ${published#"$HUB"/} publishes no contract block to compare it against"
          contract_uncompared+=("$socket/$name")
        else
          contract_compared=$((contract_compared + 1))
          gf="$(mktemp)"; ef="$(mktemp)"
          printf '%s\n' "$guest_fields" > "$gf"
          printf '%s\n' "$entity_fields" > "$ef"
          if ! cmp -s "$gf" "$ef"; then
            echo "DIVERGED $socket/$name — ${published#"$HUB"/} publishes a contract its guest no longer declares:"
            report_contract_diff "$gf" "$ef"
            contract_diverged+=("$socket/$name")
            [ "$RELEASE" = 1 ] && entry_ok=0
          fi
          rm -f "$gf" "$ef"
        fi
      fi
    fi
  fi

  [ "$entry_ok" = 1 ] && echo "ok       $socket/$name"
done <<< "$rows"

status=0
if [ "${#missing[@]}" -gt 0 ]; then
  echo
  echo "core plugins missing an overlay or artifact: ${missing[*]}"
  echo "author the overlay under aggrega-hub/plugin-overlays/, and rebuild the"
  echo "core guests (go generate ./internal/wasm/embedded/... or ./scripts/build.sh)"
  status=1
fi

if [ "${#stale_catalog[@]}" -gt 0 ]; then
  echo
  if [ "$RELEASE" = 1 ]; then
    echo "committed core-plugin catalog is not current for build $BUILD_VER: ${stale_catalog[*]}"
    echo "regenerate the catalog before publishing: scripts/gen-core-plugin-catalog.sh"
    status=1
  else
    echo "note: committed core-plugin catalog trails build $BUILD_VER (${stale_catalog[*]})"
    echo "note: this is expected between releases; the catalog is regenerated at publish time."
  fi
fi

if [ "${#contract_diverged[@]}" -gt 0 ]; then
  echo
  if [ "$RELEASE" = 1 ]; then
    echo "committed core-plugin catalog publishes a contract its guest no longer declares: ${contract_diverged[*]}"
    echo "regenerate the catalog before publishing: scripts/gen-core-plugin-catalog.sh"
    status=1
  else
    echo "note: committed core-plugin catalog publishes a contract its guest no longer declares (${contract_diverged[*]})"
    echo "note: this is expected between releases; the catalog is regenerated at publish time."
  fi
fi

# Coverage, always reported. A comparison count is what separates "every
# published contract agrees with its guest" from "no comparison was attempted",
# which a bare verdict cannot distinguish.
echo
echo "published contract comparison: $contract_compared of $contract_declared guest-declared contract(s) compared, ${#contract_diverged[@]} diverged, ${#contract_uncompared[@]} with nothing to compare against"
echo "published contract comparison: reads the contract block only; a provider's well-known annotation support is authored on its overlay, is in no guest descriptor, and is not compared here"

if [ "$status" -eq 0 ]; then
  echo
  echo "embedded plugins consistent with overlays and catalog"
fi
exit "$status"
