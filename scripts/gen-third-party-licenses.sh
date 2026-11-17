#!/usr/bin/env bash
#
# gen-third-party-licenses.sh
#
# Regenerates the third-party attribution artifacts for the shipped Aggrega CLI,
# in a REUSE/SPDX-style layout:
#
#   LICENSES/<SPDX>.txt        one canonical license body per unique SPDX id
#                              (deduplicated across dependencies)
#   THIRD-PARTY.spdx           an SPDX 2.3 tag-value SBOM: one package entry per
#                              dependency, carrying the per-package copyright and
#                              the SPDX license identifier
#   THIRD-PARTY-LICENSES.md    a thin index table (component, version, ecosystem,
#                              SPDX id, link into LICENSES/) — no inline bodies
#
# Shipped artifacts covered:
#   - the Go host binary            (./cmd/aggrega)
#   - the Go wasip1 provider guests (./plugins/{helm,compose,helmchart,terraform,
#                                    cloudformation,arm}-provider,
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
#           heuristically from the primary license file.
#   - Rust: the SPDX identifier is read verbatim from the crate's Cargo.toml
#           `license` field (authoritative); the license body comes from the
#           crate's LICENSE/COPYING/NOTICE files in the cargo registry source
#           cache.
#
# License-body deduplication and attribution:
#   - Each unique SPDX id contributes exactly one file under LICENSES/. Bodies
#     are deduplicated: a license shared by many dependencies is stored once.
#   - For license families whose raw file embeds a per-package copyright line
#     (MIT, ISC, BSD-2-Clause, BSD-3-Clause, zlib), LICENSES/ holds the canonical
#     template body with a placeholder copyright. The real per-package copyright
#     is never dropped — it is recorded in the SBOM's PackageCopyrightText for
#     every dependency.
#   - For the remaining licenses the canonical body is copied verbatim from the
#     first authoritative on-disk license file.
#
# Determinism:
#   - Entries are sorted (LC_ALL=C) by ecosystem then module/crate path.
#   - The SBOM carries a fixed creation timestamp, not the wall clock.
#   - No temporary paths or run-specific data appear in any artifact.
#   Two runs are byte-identical (every LICENSES/ file, the SBOM, and the index).
#
# Regenerate with:
#   ./scripts/gen-third-party-licenses.sh    (run from the aggrega-cli module root)
#
# Exit status is 0 on success. A findings summary (dependency counts and any
# non-permissive license) is printed to stderr; a forbidden-license CI gate is a
# separate concern and is intentionally NOT enforced here. A referenced SPDX id
# for which no canonical body can be produced is a hard error (loud, not silent).

set -euo pipefail
export LC_ALL=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
INDEX="$CLI_DIR/THIRD-PARTY-LICENSES.md"
SBOM="$CLI_DIR/THIRD-PARTY.spdx"
LICDIR="$CLI_DIR/LICENSES"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Fixed epoch so SBOM re-runs are byte-identical.
FIXED_TS="1970-01-01T00:00:00Z"

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
	done | sort
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
		*"UNICODE LICENSE"*|*"Unicode License"*)               echo "Unicode-3.0"; return;;
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
	if grep -q "Altered source versions must be plainly marked as such" "$f"; then
		echo "Zlib"; return
	fi
	if grep -q "Redistribution and use in source and binary forms" "$f"; then
		if grep -qi "endorse or promote" "$f"; then echo "BSD-3-Clause"; else echo "BSD-2-Clause"; fi
		return
	fi
	echo "UNKNOWN"
}

# file_id <path> : identify the SPDX id a license FILE carries, preferring the
# conventional LICENSE-<SUFFIX> filename hint (multi-license crates name their
# files this way) before falling back to content classification.
file_id() {
	local f="$1" up
	up="$(basename "$f" | tr '[:lower:]' '[:upper:]')"
	case "$up" in
		*ZLIB*)      echo "Zlib"; return;;
		*UNICODE*)   echo "Unicode-3.0"; return;;
		*APACHE*)    echo "Apache-2.0"; return;;
		*MPL*)       echo "MPL-2.0"; return;;
		*UNLICENSE*) echo "Unlicense"; return;;
		*ISC*)       echo "ISC"; return;;
	esac
	classify_spdx "$f"
}

# is_forbidden <spdx> : exit 0 if the SPDX identifier is a forbidden/copyleft or
# unresolved family, for the findings summary.
is_forbidden() {
	case "$1" in
		GPL|AGPL|LGPL|BUSL|SSPL|UNKNOWN) return 0;;
		*) return 1;;
	esac
}

# atomic_ids <spdx-expression> : split an SPDX expression into its atomic license
# identifiers, dropping operators, parentheses, and non-identifiers.
atomic_ids() {
	printf '%s\n' "$1" | tr '()/' '   ' | tr ' ' '\n' \
		| grep -viE '^(OR|AND|WITH)$' \
		| grep -vE '^(NOASSERTION|NONE|UNKNOWN|)$' || true
}

# copyright_lines <files...> : emit the per-package copyright statements found in
# the given files, de-duplicated in first-seen order. Matches only real
# copyright notices (line contains "copyright" followed by (c)/(C)/a symbol/a
# year, or a bare copyright symbol with a year) — never boilerplate references
# such as "the above copyright notice".
copyright_lines() {
	local f
	{
		for f in "$@"; do
			[ -f "$f" ] || continue
			sed 's/\r$//' "$f"
		done
	} \
		| { grep -iE 'copyright[[:space:]]+(\(c\)|©|[0-9]{4})|(^|[[:space:]])©[[:space:]]*[0-9]{4}' || true; } \
		| sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//' \
		| awk 'NF' | awk '!seen[$0]++'
}

# spdxid <ecosystem> <name> <version> : deterministic SPDX element id for a
# package (allowed characters are letters, digits, '.', '-').
spdxid() {
	local raw="SPDXRef-Package-$1-$2-$3"
	printf '%s' "$raw" | sed -E 's/[^A-Za-z0-9.-]/-/g'
}

# go_escape <string> : Go module-path escaping (each uppercase letter X becomes
# "!x"), for constructing a module-proxy download URL.
go_escape() {
	printf '%s' "$1" | sed -E 's/([A-Z])/!\l\1/g'
}

# --- canonical license templates (copyright-bearing families) ----------------
# LICENSES/ stores these template bodies once; the real per-package copyright is
# preserved in the SBOM. Bodies are the canonical SPDX license texts.

TEMPLATED="MIT ISC BSD-2-Clause BSD-3-Clause Zlib"

template_body() {
	case "$1" in
	MIT) cat <<'EOF'
MIT License

Copyright (c) <year> <copyright holders>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
EOF
		;;
	ISC) cat <<'EOF'
ISC License

Copyright (c) <year> <copyright holders>

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
PERFORMANCE OF THIS SOFTWARE.
EOF
		;;
	BSD-2-Clause) cat <<'EOF'
BSD 2-Clause License

Copyright (c) <year> <copyright holders>

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
EOF
		;;
	BSD-3-Clause) cat <<'EOF'
BSD 3-Clause License

Copyright (c) <year> <copyright holders>

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its contributors
   may be used to endorse or promote products derived from this software
   without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
EOF
		;;
	Zlib) cat <<'EOF'
zlib License

Copyright (c) <year> <copyright holders>

This software is provided 'as-is', without any express or implied warranty. In
no event will the authors be held liable for any damages arising from the use
of this software.

Permission is granted to anyone to use this software for any purpose, including
commercial applications, and to alter it and redistribute it freely, subject to
the following restrictions:

1. The origin of this software must not be misrepresented; you must not claim
   that you wrote the original software. If you use this software in a product,
   an acknowledgment in the product documentation would be appreciated but is
   not required.

2. Altered source versions must be plainly marked as such, and must not be
   misrepresented as being the original software.

3. This notice may not be removed or altered from any source distribution.
EOF
		;;
	*) return 1;;
	esac
}

is_templated() {
	case " $TEMPLATED " in *" $1 "*) return 0;; *) return 1;; esac
}

# add_src <id> <path> : record the first authoritative on-disk file for an SPDX
# id (used to source the canonical body of non-templated licenses).
add_src() {
	awk -F'\t' -v k="$1" '$1==k{f=1} END{exit !f}' "$WORK/srcmap.txt" 2>/dev/null && return 0
	printf '%s\t%s\n' "$1" "$2" >> "$WORK/srcmap.txt"
}

# --- Go enumeration ---------------------------------------------------------

module_set ./cmd/aggrega > "$WORK/host.txt"
GOOS=wasip1 GOARCH=wasm module_set \
	./plugins/helm-provider ./plugins/compose-provider ./plugins/helmchart-provider \
	./plugins/terraform-provider ./plugins/cloudformation-provider \
	./plugins/arm-provider \
	> "$WORK/guest.txt"
sort -u "$WORK/host.txt" "$WORK/guest.txt" > "$WORK/go-union.txt"

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

# --- shared accumulators ----------------------------------------------------

: > "$WORK/srcmap.txt"
: > "$WORK/ids-raw.txt"
: > "$WORK/sbom-pkgs.txt"
: > "$WORK/rels.txt"
: > "$WORK/index-rows.txt"
: > "$WORK/go-findings.txt"
: > "$WORK/rust-findings.txt"

# index_links <spdx-expression> : render the license column of the index — one
# markdown link per atomic SPDX id into its LICENSES/ file.
index_links() {
	local id out=""
	while read -r id; do
		[ -n "$id" ] || continue
		out="${out:+$out, }[$id](LICENSES/$id.txt)"
	done < <(atomic_ids "$1" | sort -u)
	printf '%s' "${out:-—}"
}

# emit_pkg <ecosystem> <name> <version> <declared> <concluded> <download> <purl> <usedby> <license-files...>
emit_pkg() {
	local eco="$1" name="$2" ver="$3" declared="$4" concluded="$5" dl="$6" purl="$7" usedby="$8"
	shift 8
	local files=("$@")
	local id copyr disp

	id="$(spdxid "$eco" "$name" "$ver")"
	copyr="$(copyright_lines "${files[@]}")"

	# Record source files for later LICENSES/ body sourcing.
	local f fid
	for f in "${files[@]}"; do
		[ -f "$f" ] || continue
		fid="$(file_id "$f")"
		[ "$fid" != "UNKNOWN" ] && add_src "$fid" "$f"
	done

	# Accumulate referenced atomic ids (both declared and concluded).
	atomic_ids "$declared"  >> "$WORK/ids-raw.txt"
	atomic_ids "$concluded" >> "$WORK/ids-raw.txt"

	# SBOM package block.
	{
		printf '\n'
		printf 'PackageName: %s\n' "$name"
		printf 'SPDXID: %s\n' "$id"
		printf 'PackageVersion: %s\n' "$ver"
		printf 'PackageDownloadLocation: %s\n' "$dl"
		printf 'PackageLicenseConcluded: %s\n' "$concluded"
		printf 'PackageLicenseDeclared: %s\n' "$declared"
		if [ -n "$copyr" ]; then
			printf 'PackageCopyrightText: <text>%s</text>\n' "$copyr"
		else
			printf 'PackageCopyrightText: NOASSERTION\n'
		fi
		printf 'ExternalRef: PACKAGE-MANAGER purl %s\n' "$purl"
		printf 'PackageComment: <text>Compiled into: %s</text>\n' "$usedby"
		printf 'FilesAnalyzed: false\n'
	} >> "$WORK/sbom-pkgs.txt"

	printf 'Relationship: SPDXRef-DOCUMENT DESCRIBES %s\n' "$id" >> "$WORK/rels.txt"

	# Index row. The concluded expression drives the displayed identifier and
	# the LICENSES/ links; declared==concluded for Rust.
	disp="$concluded"
	[ "$disp" = "NOASSERTION" ] && disp="$declared"
	printf '| `%s` | `%s` | %s | %s | `%s` | %s |\n' \
		"$name" "$ver" "$eco" "$usedby" "$disp" "$(index_links "$disp")" \
		>> "$WORK/index-rows.txt"
}

# --- Go dependencies --------------------------------------------------------

while IFS='|' read -r path ver dir; do
	[ -n "$path" ] || continue

	used_host=no; used_guest=no
	grep -Fxq "$path|$ver|$dir" "$WORK/host.txt" && used_host=yes
	grep -Fxq "$path|$ver|$dir" "$WORK/guest.txt" && used_guest=yes
	if [ "$used_host" = yes ] && [ "$used_guest" = yes ]; then
		usedby="cli-host, wasm-provider-guests"
	elif [ "$used_host" = yes ]; then
		usedby="cli-host"
	else
		usedby="wasm-provider-guests"
	fi

	mapfile -t lfiles < <(license_files "$dir")

	concluded="UNKNOWN"
	if [ "${#lfiles[@]}" -gt 0 ]; then
		concluded="$(classify_spdx "${lfiles[0]}")"
		# RED-scan every license file's title, not just the primary, so a
		# dual-licensed module cannot hide a copyleft file.
		for lf in "${lfiles[@]}"; do
			s="$(classify_spdx "$lf")"
			if is_forbidden "$s" && [ "$s" != "UNKNOWN" ]; then concluded="$s"; break; fi
		done
	fi
	[ "$concluded" = "UNKNOWN" ] && concluded="NOASSERTION"

	if [ "$concluded" = "NOASSERTION" ] || is_forbidden "$concluded"; then
		printf '%s@%s: %s\n' "$path" "$ver" "$concluded" >> "$WORK/go-findings.txt"
	fi

	dl="https://proxy.golang.org/$(go_escape "$path")/@v/$(go_escape "$ver").zip"
	purl="pkg:golang/$path@$ver"

	emit_pkg "go" "$path" "$ver" "NOASSERTION" "$concluded" "$dl" "$purl" "$usedby" "${lfiles[@]}"
done < "$WORK/go-union.txt"

# --- Rust dependencies ------------------------------------------------------

while read -r name ver; do
	[ -n "$name" ] || continue
	cdir="${CARGO_REG}${name}-${ver}"

	declared="NOASSERTION"
	if [ -f "$cdir/Cargo.toml" ]; then
		declared="$(grep -m1 '^license = ' "$cdir/Cargo.toml" | sed -E 's/^license = "(.*)"$/\1/')"
		[ -n "$declared" ] || declared="NOASSERTION"
	fi

	# A Rust SPDX expression is permissive unless every disjunctive option is
	# copyleft; flag only when no permissive option exists.
	if ! printf '%s' "$declared" | grep -Eqi 'MIT|Apache|BSD|ISC|Zlib|Unlicense|MPL|0BSD|BSL|CC0|Unicode'; then
		printf '%s@%s: %s\n' "$name" "$ver" "$declared" >> "$WORK/rust-findings.txt"
	fi

	mapfile -t lfiles < <(license_files "$cdir")

	dl="https://static.crates.io/crates/$name/$name-$ver.crate"
	purl="pkg:cargo/$name@$ver"

	emit_pkg "rust" "$name" "$ver" "$declared" "$declared" "$dl" "$purl" \
		"wasm-provider-guest (publishing-linter)" "${lfiles[@]}"
done < "$WORK/rust.txt"

# --- LICENSES/ : one canonical body per unique referenced SPDX id -----------

sort -u "$WORK/ids-raw.txt" | grep -v '^$' > "$WORK/ids.txt"

rm -rf "$LICDIR"
mkdir -p "$LICDIR"

missing=""
while read -r id; do
	[ -n "$id" ] || continue
	out="$LICDIR/$id.txt"
	if is_templated "$id"; then
		template_body "$id" > "$out"
	else
		src="$(awk -F'\t' -v k="$id" '$1==k{print $2; exit}' "$WORK/srcmap.txt")"
		if [ -z "$src" ] || [ ! -f "$src" ]; then
			missing="$missing $id"
			continue
		fi
		# Verbatim canonical body, CR-stripped, single trailing newline.
		printf '%s\n' "$(sed 's/\r$//' "$src")" > "$out"
	fi
done < "$WORK/ids.txt"

if [ -n "$missing" ]; then
	echo "gen-third-party-licenses: ERROR: no canonical license body found for:$missing" >&2
	echo "  (a referenced SPDX id has neither a template nor an on-disk source file)" >&2
	exit 1
fi

# --- SBOM (SPDX 2.3 tag-value) ----------------------------------------------

{
	printf 'SPDXVersion: SPDX-2.3\n'
	printf 'DataLicense: CC0-1.0\n'
	printf 'SPDXID: SPDXRef-DOCUMENT\n'
	printf 'DocumentName: aggrega-cli-third-party\n'
	printf 'DocumentNamespace: https://aggrega.ai/spdx/aggrega-cli-third-party\n'
	printf 'Creator: Tool: gen-third-party-licenses.sh\n'
	printf 'Created: %s\n' "$FIXED_TS"
	printf 'DocumentComment: <text>Third-party dependencies compiled into the shipped Aggrega CLI (Go host binary, Go WebAssembly provider guests, and the Rust WebAssembly guest). Generated deterministically; the creation timestamp is fixed so re-runs are byte-identical. License bodies are in the sibling LICENSES/ directory.</text>\n'
	cat "$WORK/sbom-pkgs.txt"
	printf '\n'
	cat "$WORK/rels.txt"
} > "$SBOM"

# --- thin index -------------------------------------------------------------

go_count="$(wc -l < "$WORK/go-union.txt" | tr -d ' ')"
rust_count="$(wc -l < "$WORK/rust.txt" | tr -d ' ')"
lic_count="$(wc -l < "$WORK/ids.txt" | tr -d ' ')"

{
	cat <<'HEADER'
# Third-Party Licenses

This is the attribution index for every third-party dependency compiled into
the shipped Aggrega CLI artifacts.

Shipped artifacts covered:

- the Go host binary (`cmd/aggrega`)
- the Go WebAssembly provider guests (`plugins/helm-provider`,
  `plugins/compose-provider`, `plugins/helmchart-provider`,
  `plugins/terraform-provider`, `plugins/cloudformation-provider`,
  `plugins/arm-provider`), embedded into the host binary
- the Rust WebAssembly guest (`plugins/publishing-linter`)

**These files are generated. Do not edit them by hand.** Regenerate with:

```sh
./scripts/gen-third-party-licenses.sh
```

run from the `aggrega-cli` module root.

Attribution is split across three artifacts, in a REUSE/SPDX-style layout:

- `LICENSES/<SPDX>.txt` — the full text of each distinct license, stored once
  per SPDX identifier. For license families that embed a per-package copyright
  line (MIT, ISC, BSD, zlib) the stored body is the canonical template; the
  real per-package copyright is recorded in the SBOM below.
- `THIRD-PARTY.spdx` — a machine-readable SPDX 2.3 SBOM with one entry per
  dependency: name, version, SPDX license identifier, per-package copyright, and
  package locator.
- this file — a human-readable index linking each dependency to its license
  text.

The generator enumerates the actual compiled dependency set (`go list -deps`
for Go, `Cargo.lock` for Rust), resolves each license from the local module and
registry caches, and emits all three artifacts deterministically (stable sort,
fixed SBOM timestamp; two runs are byte-identical). First-party Aggrega modules
and crates are excluded.

For Go modules the SPDX identifier is classified from the license text and is
best-effort. For Rust crates it is read verbatim from the crate's `Cargo.toml`
`license` field.

HEADER

	printf '## Summary\n\n'
	printf '| Ecosystem | Third-party dependencies |\n'
	printf '| --- | --- |\n'
	printf '| Go | %s |\n' "$go_count"
	printf '| Rust | %s |\n' "$rust_count"
	printf '| TypeScript | 0 |\n\n'
	printf 'Distinct licenses: %s (see `LICENSES/`).\n\n' "$lic_count"

	printf '## Dependencies\n\n'
	printf '| Component | Version | Ecosystem | Compiled into | SPDX | License text |\n'
	printf '| --- | --- | --- | --- | --- | --- |\n'
	cat "$WORK/index-rows.txt"
	printf '\n'

	printf '## TypeScript\n\n'
	printf 'No TypeScript dependencies are compiled into the CLI. The CLI ships no\n'
	printf 'TypeScript-derived artifact; TypeScript plugin code in the plugin\n'
	printf 'development kit is example material, not part of any shipped CLI artifact.\n'
} > "$INDEX"

# --- findings summary (stderr) ----------------------------------------------

go_forbidden="$(wc -l < "$WORK/go-findings.txt" | tr -d ' ')"
rust_forbidden="$(wc -l < "$WORK/rust-findings.txt" | tr -d ' ')"

{
	echo "gen-third-party-licenses: wrote"
	echo "  $INDEX"
	echo "  $SBOM"
	echo "  $LICDIR/ ($lic_count license files)"
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
