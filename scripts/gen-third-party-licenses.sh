#!/usr/bin/env bash
#
# gen-third-party-licenses.sh
#
# Regenerates aggrega-cli/THIRD-PARTY-LICENSES.md: the attribution file listing
# every third-party dependency compiled into the shipped CLI artifacts, together
# with each dependency's license text.
#
# Shipped artifacts covered:
#   - the Go host binary            (./cmd/aggrega)
#   - the Go wasip1 provider guests (./plugins/{helm,compose,helmchart}-provider,
#                                    embedded into the host binary)
#   - the Rust wasm guest           (./plugins/publishing-linter)
#
# The script is self-contained: it needs only the Go toolchain, cargo's on-disk
# registry cache, and standard shell utilities. No external license tool
# (go-licenses, cargo-license, cargo-about, license-checker) is required.
#
# Enumeration:
#   - Go modules come from `go list -deps` over the host and (GOOS=wasip1
#     GOARCH=wasm) the provider guests; the two module sets are unioned and each
#     entry is labelled with the artifact group that pulls it in.
#   - Rust crates come from parsing plugins/publishing-linter/Cargo.lock; only
#     crates with a crates.io registry source are third-party.
#   - First-party modules/crates (the github.com/aggrega-ai/* Go modules and the
#     in-workspace Rust crates) are excluded — they are our own Apache-2.0 code.
#
# License resolution (from local caches only):
#   - Go:   the module's LICENSE/COPYING/NOTICE files under its module-cache dir
#           (`go list -m -f {{.Dir}}`). The SPDX identifier is classified
#           heuristically from the primary license file; the authoritative
#           attribution is the full license text reproduced under each entry.
#   - Rust: the SPDX identifier is read verbatim from the crate's Cargo.toml
#           `license` field (authoritative); the license text comes from the
#           crate's LICENSE/COPYING/NOTICE files in the cargo registry source
#           cache.
#
# The output is deterministic: entries are sorted (LC_ALL=C) by ecosystem then
# module/crate path, no timestamps are emitted, and two runs are byte-identical.
#
# Regenerate with:
#   ./scripts/gen-third-party-licenses.sh    (run from the aggrega-cli module root)
#
# Exit status is 0 on success. A findings summary (dependency counts and any
# non-permissive license) is printed to stderr; a forbidden-license CI gate is a
# separate concern and is intentionally NOT enforced here.

set -euo pipefail
export LC_ALL=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT="$CLI_DIR/THIRD-PARTY-LICENSES.md"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cd "$CLI_DIR"

# --- helpers ----------------------------------------------------------------

# module_set <listargs...> : emit unique "path|version|dir" for modules reached
# by `go list -deps` of the given packages, excluding the standard library and
# first-party (github.com/aggrega-ai/*) modules.
module_set() {
	go list -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' "$@" 2>/dev/null \
		| grep -v '^$' \
		| grep -v '^github.com/aggrega-ai/' \
		| sort -u
}

# license_files <dir> : emit, newline-separated and sorted, the paths of the
# license/notice files at the root of <dir>. Case-insensitive; files only.
license_files() {
	local dir="$1" f
	[ -n "$dir" ] && [ -d "$dir" ] || return 0
	shopt -s nullglob nocaseglob
	for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/NOTICE* "$dir"/UNLICENSE*; do
		[ -f "$f" ] && printf '%s\n' "$f"
	done
	shopt -u nullglob nocaseglob
}

# classify_spdx <file> : best-effort SPDX identifier from a license file's text.
# Keys off the title line first (so the MPL-2.0 body's reference to the GNU
# Affero GPL does not produce a false AGPL hit), then falls back to the
# canonical permissive-license signature phrases.
classify_spdx() {
	local f="$1" head40
	head40="$(head -40 "$f" 2>/dev/null || true)"
	case "$head40" in
		*"Apache License"*)                                    echo "Apache-2.0"; return;;
		*"Mozilla Public License Version 2.0"*|*"Mozilla Public License, version 2.0"*) echo "MPL-2.0"; return;;
		*"GNU AFFERO GENERAL PUBLIC LICENSE"*)                 echo "AGPL"; return;;
		*"GNU LESSER GENERAL PUBLIC LICENSE"*)                 echo "LGPL"; return;;
		*"GNU GENERAL PUBLIC LICENSE"*)                        echo "GPL"; return;;
		*"Business Source License"*)                           echo "BUSL"; return;;
		*"Server Side Public License"*)                        echo "SSPL"; return;;
		*"ISC License"*)                                       echo "ISC"; return;;
	esac
	if grep -q "Permission is hereby granted, free of charge, to any person obtaining" "$f"; then
		echo "MIT"; return
	fi
	if grep -Eq "Permission to use, copy, modify, and(/| and )?or distribute" "$f"; then
		echo "ISC"; return
	fi
	if grep -q "This is free and unencumbered software released into the public domain" "$f"; then
		echo "Unlicense"; return
	fi
	if grep -q "Redistribution and use in source and binary forms" "$f"; then
		if grep -qi "endorse or promote" "$f"; then echo "BSD-3-Clause"; else echo "BSD-2-Clause"; fi
		return
	fi
	echo "UNKNOWN"
}

# is_forbidden <spdx> : exit 0 if the SPDX identifier is a forbidden/copyleft or
# unresolved family, for the findings summary.
is_forbidden() {
	case "$1" in
		GPL|AGPL|LGPL|BUSL|SSPL|UNKNOWN) return 0;;
		*) return 1;;
	esac
}

# emit_text_block <file> : reproduce a license file inside a fenced text block.
emit_text_block() {
	printf '```text\n'
	# Trailing-newline-normalised so the block always closes on its own line.
	sed -e 's/\r$//' "$1"
	printf '\n```\n'
}

# --- Go enumeration ---------------------------------------------------------

module_set ./cmd/aggrega > "$WORK/host.txt"
GOOS=wasip1 GOARCH=wasm module_set \
	./plugins/helm-provider ./plugins/compose-provider ./plugins/helmchart-provider \
	> "$WORK/guest.txt"
sort -u "$WORK/host.txt" "$WORK/guest.txt" > "$WORK/go-union.txt"

go_count=0
go_forbidden=0
: > "$WORK/go-findings.txt"

# --- Rust enumeration -------------------------------------------------------
# Parse Cargo.lock: emit "name version" for every crate with a crates.io
# registry source (registry crates are third-party; path crates are first-party).
awk '
	/^\[\[package\]\]/ { name=""; ver=""; src=0 }
	/^name = /    { gsub(/"/,""); name=$3 }
	/^version = / { gsub(/"/,""); ver=$3 }
	/^source = "registry/ { src=1 }
	/^$/          { if (src && name && ver) print name, ver }
	END           { if (src && name && ver) print name, ver }
' plugins/publishing-linter/Cargo.lock | sort -u > "$WORK/rust.txt"

CARGO_REG="$(ls -d "${CARGO_HOME:-$HOME/.cargo}"/registry/src/*/ 2>/dev/null | head -1 || true)"

rust_count=0
rust_forbidden=0
: > "$WORK/rust-findings.txt"

# --- emit -------------------------------------------------------------------

{
	cat <<'HEADER'
# Third-Party Licenses

This file lists every third-party dependency compiled into the shipped Aggrega
CLI artifacts, together with each dependency's license text, as attribution.

Shipped artifacts covered:

- the Go host binary (`cmd/aggrega`)
- the Go WebAssembly provider guests (`plugins/helm-provider`,
  `plugins/compose-provider`, `plugins/helmchart-provider`), embedded into the
  host binary
- the Rust WebAssembly guest (`plugins/publishing-linter`)

**This file is generated. Do not edit it by hand.** Regenerate it with:

```sh
./scripts/gen-third-party-licenses.sh
```

run from the `aggrega-cli` module root. The generator enumerates the actual
compiled dependency set (via `go list -deps` for Go and `Cargo.lock` for Rust),
resolves each dependency's license from the local module and registry caches,
and emits this file deterministically (stable sort, no timestamps; two runs are
byte-identical). First-party Aggrega modules and crates are excluded.

For Go modules the SPDX identifier is classified from the license text and is
best-effort; the reproduced license text below each entry is the authoritative
attribution. For Rust crates the SPDX identifier is read verbatim from the
crate's `Cargo.toml` `license` field.

HEADER

	# ---- Go section (buffered to a temp so counts can be back-filled) -------
	{
		while IFS='|' read -r path ver dir; do
			[ -n "$path" ] || continue
			go_count=$((go_count + 1))

			used_host=no; used_guest=no
			grep -Fxq "$path|$ver|$dir" "$WORK/host.txt" && used_host=yes
			grep -Fxq "$path|$ver|$dir" "$WORK/guest.txt" && used_guest=yes
			if [ "$used_host" = yes ] && [ "$used_guest" = yes ]; then
				used="cli-host, wasm-provider-guests"
			elif [ "$used_host" = yes ]; then
				used="cli-host"
			else
				used="wasm-provider-guests"
			fi

			mapfile -t lfiles < <(license_files "$dir")

			spdx="UNKNOWN"
			if [ "${#lfiles[@]}" -gt 0 ]; then
				spdx="$(classify_spdx "${lfiles[0]}")"
				# RED-scan every license file's title, not just the primary,
				# so a dual-licensed module cannot hide a copyleft file.
				for lf in "${lfiles[@]}"; do
					s="$(classify_spdx "$lf")"
					if is_forbidden "$s" && [ "$s" != "UNKNOWN" ]; then spdx="$s"; break; fi
				done
			fi

			if is_forbidden "$spdx"; then
				go_forbidden=$((go_forbidden + 1))
				printf '%s@%s: %s\n' "$path" "$ver" "$spdx" >> "$WORK/go-findings.txt"
			fi

			printf '### %s\n\n' "$path"
			printf -- '- Version: `%s`\n' "$ver"
			printf -- '- SPDX: `%s`\n' "$spdx"
			printf -- '- Used by: %s\n\n' "$used"

			if [ "${#lfiles[@]}" -eq 0 ]; then
				printf 'No license file was found in the module cache for this dependency.\n\n'
			else
				for lf in "${lfiles[@]}"; do
					printf '#### %s\n\n' "$(basename "$lf")"
					emit_text_block "$lf"
					printf '\n'
				done
			fi
		done < "$WORK/go-union.txt"
	} > "$WORK/go-section.txt"

	# ---- Rust section ------------------------------------------------------
	{
		while read -r name ver; do
			[ -n "$name" ] || continue
			rust_count=$((rust_count + 1))
			cdir="${CARGO_REG}${name}-${ver}"

			spdx="UNKNOWN"
			if [ -f "$cdir/Cargo.toml" ]; then
				spdx="$(grep -m1 '^license = ' "$cdir/Cargo.toml" | sed -E 's/^license = "(.*)"$/\1/')"
				[ -n "$spdx" ] || spdx="UNKNOWN"
			fi

			# A Rust SPDX expression is permissive unless every disjunctive
			# option is copyleft; flag only when no permissive option exists.
			if ! printf '%s' "$spdx" | grep -Eqi 'MIT|Apache|BSD|ISC|Zlib|Unlicense|MPL|0BSD|BSL|CC0|Unicode'; then
				rust_forbidden=$((rust_forbidden + 1))
				printf '%s@%s: %s\n' "$name" "$ver" "$spdx" >> "$WORK/rust-findings.txt"
			fi

			mapfile -t lfiles < <(license_files "$cdir")

			printf '### %s\n\n' "$name"
			printf -- '- Version: `%s`\n' "$ver"
			printf -- '- SPDX: `%s`\n' "$spdx"
			printf -- '- Used by: wasm-provider-guest (publishing-linter)\n\n'

			if [ "${#lfiles[@]}" -eq 0 ]; then
				printf 'No license file is bundled in the crate; the SPDX identifier above is the crate manifest declaration.\n\n'
			else
				for lf in "${lfiles[@]}"; do
					printf '#### %s\n\n' "$(basename "$lf")"
					emit_text_block "$lf"
					printf '\n'
				done
			fi
		done < "$WORK/rust.txt"
	} > "$WORK/rust-section.txt"

	# ---- counts recomputed (subshell counters do not escape the pipe) ------
	go_count="$(wc -l < "$WORK/go-union.txt" | tr -d ' ')"
	rust_count="$(wc -l < "$WORK/rust.txt" | tr -d ' ')"

	# ---- summary + sections -----------------------------------------------
	printf '## Summary\n\n'
	printf '| Ecosystem | Third-party dependencies |\n'
	printf '| --- | --- |\n'
	printf '| Go | %s |\n' "$go_count"
	printf '| Rust | %s |\n' "$rust_count"
	printf '| TypeScript | 0 |\n\n'

	printf '## Go\n\n'
	cat "$WORK/go-section.txt"

	printf '## Rust\n\n'
	printf 'Dependencies of the `publishing-linter` Rust WebAssembly guest.\n\n'
	cat "$WORK/rust-section.txt"

	printf '## TypeScript\n\n'
	printf 'No TypeScript dependencies are compiled into the CLI. The CLI ships no\n'
	printf 'TypeScript-derived artifact; TypeScript plugin code in the plugin\n'
	printf 'development kit is example material, not part of any shipped CLI artifact.\n'
} > "$OUT"

# --- findings summary (stderr) ----------------------------------------------

go_count="$(wc -l < "$WORK/go-union.txt" | tr -d ' ')"
rust_count="$(wc -l < "$WORK/rust.txt" | tr -d ' ')"
go_forbidden="$(wc -l < "$WORK/go-findings.txt" | tr -d ' ')"
rust_forbidden="$(wc -l < "$WORK/rust-findings.txt" | tr -d ' ')"

{
	echo "gen-third-party-licenses: wrote $OUT"
	echo "  Go third-party modules : $go_count"
	echo "  Rust third-party crates: $rust_count"
	echo "  TypeScript             : 0 (nothing compiled into the CLI)"
	echo "  Non-permissive/unresolved findings: Go=$go_forbidden Rust=$rust_forbidden"
	if [ "$go_forbidden" != 0 ]; then
		echo "  Go findings:"
		sed 's/^/    /' "$WORK/go-findings.txt"
	fi
	if [ "$rust_forbidden" != 0 ]; then
		echo "  Rust findings:"
		sed 's/^/    /' "$WORK/rust-findings.txt"
	fi
} >&2
