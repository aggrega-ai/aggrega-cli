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
#   THIRD-PARTY-NOTICES.txt    the plain-text notices the binary prints with
#                              `aggrega version --licenses`: per dependency its
#                              name, version, license, copyright lines and what it
#                              is compiled into, then each license text once. A
#                              byte-identical copy is written to
#                              internal/thirdparty/THIRD-PARTY-NOTICES.txt, the
#                              package that embeds it (a go:embed pattern cannot
#                              reach above its package).
#   THIRD-PARTY-LICENSES.md    a thin index table (component, version, ecosystem,
#                              SPDX id, link into LICENSES/) — no inline bodies
#
# Shipped artifacts covered:
#   - the Go host binary            (./cmd/aggrega), for every target the release
#                                   builds it for (THIRD_PARTY_HOST_TARGETS)
#   - the Go standard library, compiled into the host and every Go plugin
#   - for the embedded Rust guests, what their wasm32-wasip1 builds link in from
#     the toolchain: the Rust standard library and wasi-libc (with the third-party
#     works it carries), one entry per toolchain channel and per wasi-libc commit
#   - every guest the binary embeds, as the core-plugin list names them: the
#     rows whose embed column is empty (read through core_plugin_rows in
#     scripts/lib/core-plugins.sh), each as <socket>/<name>. A row the binary
#     does not embed contributes nothing of its module and is not covered.
#
# The script is self-contained: it needs only the Go toolchain, cargo's on-disk
# registry cache, and standard shell utilities. No external license tool
# (go-licenses, cargo-license, cargo-about, license-checker) is required.
#
# Enumeration:
#   - Go modules come from `go list -deps` over the host, once per release target
#     (THIRD_PARTY_HOST_TARGETS), and, per embedded Go row, (GOOS=wasip1
#     GOARCH=wasm) that guest's package; the sets are unioned and each entry is
#     labelled with the host and/or the guests that pull it in. Every `go list`
#     runs in THIRD_PARTY_GO_ENV, so the sets do not depend on the machine.
#     A `go list` that fails stops the script.
#   - The Go standard library is one entry: its version from go.mod's toolchain
#     directive (else its go directive), BSD-3-Clause, copyright lines from the
#     LICENSE file of the Go root.
#   - The Rust standard library entries are one per distinct channel a guest's
#     rust-toolchain.toml names: MIT OR Apache-2.0, with the Rust project's
#     COPYRIGHT file at that release tag as copyright source and notice. The
#     wasi-libc entries are one per distinct wasi-libc commit the channels' Rust
#     releases build their libc from: its LICENSE is the notice; musl, cloudlibc
#     and musl-fts, which the default build links, are entries of their own, each
#     with its own license file as copyright source. dlmalloc (CC0-1.0, which asks
#     for no attribution) and emmalloc (not linked by default) are not listed.
#     All of it is read from the pinned texts under third-party/toolchain/
#     (fetched by scripts/fetch-toolchain-notices.sh), never from the network.
#   - Rust crates come from parsing the Cargo.lock in each embedded Rust row's
#     source directory; only crates with a crates.io registry source are
#     third-party, and each entry is labelled with the guests that pull it in.
#   - First-party code is excluded, as our own Apache-2.0 code: the Go modules
#     under github.com/aggrega-ai/, the Rust crates named in
#     THIRD_PARTY_FIRST_PARTY_CRATES, and the in-workspace Rust path crates. The
#     Go prefix and the crate names are defined once, in scripts/lib/third-party-inputs.sh, which
#     the digest of the inputs uses too, so a first-party version pin never makes
#     the attribution stale.
#
# License resolution (from local caches only):
#   - Go:   the module's LICENSE/COPYING/NOTICE files under its module-cache dir
#           (`go list -m -f {{.Dir}}`). The SPDX identifier is classified
#           heuristically from the primary license file.
#   - Rust: the SPDX identifier is read verbatim from the crate's Cargo.toml
#           `license` field (authoritative); the license body comes from the
#           crate's LICENSE/COPYING/NOTICE files in the cargo registry source
#           cache (every registry/src/* directory of CARGO_HOME is searched).
#           A crate that ships no license file is attributed through the
#           canonical body of the SPDX id it declares, with copyright
#           NOASSERTION; the stderr summary counts those crates.
#   - A flat Rust expression `A OR B OR ...` concludes the alternatives that are
#     not GPL-family licenses (a licensee may take any one alternative); the
#     declared expression stays in the SBOM, and LICENSES/ and the index carry
#     the concluded ids. A license-exception id after WITH is not a license id.
#
# Staleness:
#   THIRD-PARTY.spdx records, in its DocumentComment, `Inputs digest: sha256:<hex>`
#   over the inputs this script reads (defined in scripts/lib/third-party-inputs.sh);
#   scripts/check-third-party-licenses.sh recomputes it and fails when it differs.
#
# Loud failures (exit 1; no output file is written or replaced):
#   - a `go list` that fails, naming its packages;
#   - a Rust row with no Cargo.lock, or a toolchain that is neither go nor rust;
#   - a Go module with no license file; a Rust crate with no source directory
#     in any registry, or with neither a license file nor a license field —
#     each named;
#   - a Rust channel with no row in third-party/toolchain/toolchains.tsv, or a
#     pinned text missing under third-party/toolchain/, naming the channel and
#     the command that fetches it;
#   - a referenced SPDX id with neither a template nor an on-disk source file.
#
# License-body deduplication and attribution:
#   - Each unique SPDX id contributes exactly one file under LICENSES/. Bodies
#     are deduplicated: a license shared by many dependencies is stored once.
#   - For license families whose raw file embeds a per-package copyright line
#     (MIT, ISC, BSD-2-Clause, BSD-3-Clause, zlib, 0BSD, MIT-0), LICENSES/ holds
#     the canonical template body with a placeholder copyright; BSL-1.0, which
#     has none, is stored canonical too. The real per-package copyright
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
# With `--out <dir>` the four outputs are written under <dir> in the same layout
# as the module root (LICENSES/, THIRD-PARTY.spdx, THIRD-PARTY-NOTICES.txt,
# THIRD-PARTY-LICENSES.md); <dir> is created when missing and nothing in the
# module is written, the embedded copy of the notices included. Any other
# argument is refused.
#
# Exit status is 0 on success. A findings summary (dependency counts and any
# non-permissive license) is printed to stderr; a forbidden-license CI gate is a
# separate concern and is intentionally NOT enforced here. A referenced SPDX id
# for which no canonical body can be produced is a hard error (loud, not silent).

set -euo pipefail
export LC_ALL=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/core-plugins.sh
source "$SCRIPT_DIR/lib/core-plugins.sh"
# shellcheck source=lib/third-party-inputs.sh
source "$SCRIPT_DIR/lib/third-party-inputs.sh"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Output root: the module itself, or the directory --out names.
OUT_ROOT="$CLI_DIR"
NOTICES_EMBED="$CLI_DIR/internal/thirdparty/THIRD-PARTY-NOTICES.txt"
if [ "$#" -gt 0 ]; then
	if [ "$1" = "--out" ] && [ "$#" -eq 2 ] && [ -n "$2" ]; then
		mkdir -p -- "$2"
		OUT_ROOT="$(cd "$2" && pwd)"
		NOTICES_EMBED=""
	else
		echo "gen-third-party-licenses: ERROR: unsupported argument: $1 (the only form is: --out <dir>)" >&2
		exit 2
	fi
fi
INDEX="$OUT_ROOT/THIRD-PARTY-LICENSES.md"
NOTICES="$OUT_ROOT/THIRD-PARTY-NOTICES.txt"
SBOM="$OUT_ROOT/THIRD-PARTY.spdx"
LICDIR="$OUT_ROOT/LICENSES"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Fixed epoch so SBOM re-runs are byte-identical.
FIXED_TS="1970-01-01T00:00:00Z"

cd "$CLI_DIR"

# --- helpers ----------------------------------------------------------------

# module_set <listargs...> : emit unique "path|version|dir" for modules reached
# by `go list -deps` of the given packages (in THIRD_PARTY_GO_ENV, with GOOS and
# GOARCH as the caller sets them), excluding the standard library and first-party
# modules (THIRD_PARTY_FIRST_PARTY_GO_PREFIX).
# A failing `go list` stops the script, naming the packages.
module_set() {
	local out
	if ! out="$(env $THIRD_PARTY_GO_ENV go list -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' "$@")"; then
		echo "gen-third-party-licenses: ERROR: go list failed for: $*" >&2
		exit 1
	fi
	printf '%s\n' "$out" \
		| { grep -v '^$' || true; } \
		| awk -v p="$THIRD_PARTY_FIRST_PARTY_GO_PREFIX" 'index($0, p) != 1' \
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
		| awk 'prev_with { prev_with = 0; next } toupper($0) == "WITH" { prev_with = 1; next } { print }' \
		| grep -viE '^(OR|AND)$' \
		| grep -vE '^(NOASSERTION|NONE|UNKNOWN|)$' || true
}

# elect_permissive <spdx-expression> : for a flat "A OR B OR C" expression, the
# alternatives that are not a GPL-family license, in the declared order; the
# whole expression when it has no such alternative or is not flat. This is the
# expression the attribution concludes: a licensee may take any one alternative,
# and the declared expression stays in the SBOM.
elect_permissive() {
	case "$1" in *"("*|*" AND "*) printf '%s' "$1"; return;; esac
	printf '%s' "$1" | awk -v orig="$1" '
		BEGIN { RS = " OR " }
		{ t = $0; gsub(/^[ \t\n]+|[ \t\n]+$/, "", t) }
		t != "" && t !~ /^(A|L)?GPL/ { out = out (out == "" ? "" : " OR ") t }
		END { print (out == "" ? orig : out) }' | tr -d '\n'
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

TEMPLATED="MIT ISC BSD-2-Clause BSD-3-Clause Zlib 0BSD MIT-0 BSL-1.0"

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
	0BSD) cat <<'EOF'
Zero-Clause BSD

Copyright (C) <year> <copyright holders>

Permission to use, copy, modify, and/or distribute this software for
any purpose with or without fee is hereby granted.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN
AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT
OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
EOF
		;;
	MIT-0) cat <<'EOF'
MIT No Attribution

Copyright <year> <copyright holders>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
EOF
		;;
	BSL-1.0) cat <<'EOF'
Boost Software License - Version 1.0 - August 17th, 2003

Permission is hereby granted, free of charge, to any person or organization
obtaining a copy of the software and accompanying documentation covered by
this license (the "Software") to use, reproduce, display, distribute,
execute, and transmit the Software, and to prepare derivative works of the
Software, and to permit third-parties to whom the Software is furnished to
do so, all subject to the following:

The copyright notices in the Software and this entire statement, including
the above license grant, this restriction and the following disclaimer,
must be included in all copies of the Software, in whole or in part, and
all derivative works of the Software, unless such copies or derivative
works are solely in the form of machine-executable object code generated by
a source language processor.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE, TITLE AND NON-INFRINGEMENT. IN NO EVENT
SHALL THE COPYRIGHT HOLDERS OR ANYONE DISTRIBUTING THE SOFTWARE BE LIABLE
FOR ANY DAMAGES OR OTHER LIABILITY, WHETHER IN CONTRACT, TORT OR OTHERWISE,
ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
DEALINGS IN THE SOFTWARE.
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

# --- Guest set: the embedded rows of the core-plugin list --------------------

# Captured before it is read, so a refusal stops the script.
rows="$(core_plugin_rows)" || exit 1

# The digest of everything this run reads to decide the attribution, recorded in
# the SBOM so scripts/check-third-party-licenses.sh can tell when it is stale.
inputs_digest="$(third_party_inputs_digest)" || exit 1

mkdir -p "$WORK/sets"
: > "$WORK/go-sets.list"     # "<label><TAB><file>" per Go set, host first
: > "$WORK/rust-sets.list"   # "<label><TAB><file>" per Rust guest
: > "$WORK/rust-src.list"    # "<label><TAB><source dir>" per Rust guest
: > "$WORK/rust-channels.list"  # "<channel><TAB><label>" per Rust guest
: > "$WORK/covered.md"       # the index's list of what is covered
: > "$WORK/failures.txt"     # every dependency with no resolvable license text
: > "$WORK/rust-nofile.txt"  # crates shipping no license file; the Cargo.toml license field stands
embedded_labels=""
n=0

# --- Go enumeration ---------------------------------------------------------

# The host set is the union over every release target.
: > "$WORK/sets/host-all.txt"
for target in $THIRD_PARTY_HOST_TARGETS; do
	GOOS="${target%/*}" GOARCH="${target#*/}" module_set ./cmd/aggrega >> "$WORK/sets/host-all.txt"
done
sort -u "$WORK/sets/host-all.txt" > "$WORK/sets/host.txt"
printf 'aggrega\t%s\n' "$WORK/sets/host.txt" >> "$WORK/go-sets.list"
printf -- '- the Go host binary (`cmd/aggrega`), for %s\n' "${THIRD_PARTY_HOST_TARGETS// /, }" >> "$WORK/covered.md"
printf -- '- the Go standard library, compiled into the host binary and every Go plugin below\n' >> "$WORK/covered.md"
if [ "$(awk -F'\x1f' '$4 == "rust" && $6 == "yes"' <<< "$rows" | wc -l)" -gt 0 ]; then
	printf -- '- the Rust standard library and wasi-libc, compiled into every Rust plugin below\n' >> "$WORK/covered.md"
fi

while IFS=$'\x1f' read -r socket name src toolchain extra embed dir; do
	[ -n "$socket" ] || continue
	[ "$embed" = yes ] || continue
	n=$((n + 1))
	label="$socket/$name"
	embedded_labels="${embedded_labels:+$embedded_labels, }$label"
	src="${src#./}"
	case "$toolchain" in
	go)
		GOOS=wasip1 GOARCH=wasm module_set "./$src" > "$WORK/sets/$n.txt"
		printf '%s\t%s\n' "$label" "$WORK/sets/$n.txt" >> "$WORK/go-sets.list"
		printf -- '- `%s`, a Go WebAssembly plugin (`%s`), embedded into the host binary\n' "$label" "$src" >> "$WORK/covered.md"
		;;
	rust)
		lock="$src/Cargo.lock"
		if [ ! -f "$lock" ]; then
			echo "gen-third-party-licenses: ERROR: $label has no $lock" >&2
			exit 1
		fi
		# "name version" for every crate with a crates.io registry source
		# (registry crates are third-party; path crates are first-party).
		awk '
			/^\[\[package\]\]/ { name=""; ver=""; src=0 }
			/^name = /    { gsub(/"/,""); name=$3 }
			/^version = / { gsub(/"/,""); ver=$3 }
			/^source = "registry/ { src=1 }
			/^$/          { if (src && name && ver) print name, ver }
			END           { if (src && name && ver) print name, ver }
		' "$lock" | sort -u > "$WORK/sets/$n.txt"
		printf '%s\t%s\n' "$label" "$WORK/sets/$n.txt" >> "$WORK/rust-sets.list"
		printf '%s\t%s\n' "$label" "$src" >> "$WORK/rust-src.list"
		printf '%s\t%s\n' "$(core_plugin_rust_toolchain "$src")" "$label" >> "$WORK/rust-channels.list"
		printf -- '- `%s`, a Rust WebAssembly plugin (`%s`), embedded into the host binary\n' "$label" "$src" >> "$WORK/covered.md"
		;;
	*)
		echo "gen-third-party-licenses: ERROR: $label names the toolchain \"$toolchain\", not go or rust" >&2
		exit 1
		;;
	esac
done <<< "$rows"

sort -u $(awk -F'\t' '{print $2}' "$WORK/go-sets.list") > "$WORK/go-union.txt"
if [ -s "$WORK/rust-sets.list" ]; then
	sort -u $(awk -F'\t' '{print $2}' "$WORK/rust-sets.list") > "$WORK/rust.txt"
else
	: > "$WORK/rust.txt"
fi

# every cargo registry source directory (each ends in /)
mapfile -t CARGO_REGS < <(ls -d "${CARGO_HOME:-$HOME/.cargo}"/registry/src/*/ 2>/dev/null || true)

# used_by <sets-list> <line> : the labels of the sets holding <line>, joined by ", ".
used_by() {
	local label file out=""
	while IFS=$'\t' read -r label file; do
		if grep -Fxq -- "$2" "$file"; then out="${out:+$out, }$label"; fi
	done < "$1"
	printf '%s' "$out"
}

# fetch_dirs <labels> : the source directories of the Rust guests named in the
# ", "-joined <labels>, space-joined.
fetch_dirs() {
	local label out=""
	while IFS= read -r label; do
		[ -n "$label" ] || continue
		out="${out:+$out }$(awk -F'\t' -v l="$label" '$1 == l { print $2; exit }' "$WORK/rust-src.list")"
	done < <(printf '%s' "$1" | sed 's/, /\n/g')
	printf '%s' "$out"
}

# --- shared accumulators ----------------------------------------------------

: > "$WORK/srcmap.txt"
: > "$WORK/ids-raw.txt"
: > "$WORK/sbom-pkgs.txt"
: > "$WORK/rels.txt"
: > "$WORK/index-rows.txt"
: > "$WORK/notices-pkgs.txt"
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

EMIT_NOTICES=()

# emit_pkg <ecosystem> <name> <version> <declared> <concluded> <download> <purl> <usedby> <license-files...>
#
# Three optional globals, which the caller sets before the call and clears after:
#   EMIT_NOTICES  an array of files reproduced as the entry's Notice, besides any
#                 license-file named NOTICE*
#   EMIT_NOTE     one line of context printed with the entry and kept in the SBOM
#   EMIT_NO_SRC   when set, the files are not offered as the canonical body of the
#                 SPDX id they resemble (a text that combines several licenses
#                 is not that license's body)
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
		[ -z "${EMIT_NO_SRC:-}" ] || continue
		fid="$(file_id "$f")"
		[ "$fid" != "UNKNOWN" ] && add_src "$fid" "$f"
	done

	# Accumulate referenced atomic ids: those of the concluded expression, which
	# has LICENSES/ text and an index link. A declared id with no body that the
	# conclusion leaves out (a GPL-family alternative) is not referenced.
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
		printf 'PackageComment: <text>Compiled into: %s%s</text>\n' "$usedby" "${EMIT_NOTE:+. $EMIT_NOTE}"
		printf 'FilesAnalyzed: false\n'
	} >> "$WORK/sbom-pkgs.txt"

	# Notices block: what the binary prints for this dependency.
	{
		printf '\n%s %s\n' "$name" "$ver"
		printf '  License: %s\n' "$concluded"
		printf '  Compiled into: %s\n' "$usedby"
		[ -z "${EMIT_NOTE:-}" ] || printf '  Note: %s\n' "$EMIT_NOTE"
		if [ -n "$copyr" ]; then
			printf '  Copyright:\n'
			printf '%s\n' "$copyr" | sed 's/^/    /'
		else
			printf '  Copyright: none is stated in its license or notice files\n'
		fi
		for f in "${files[@]}"; do
			[ -f "$f" ] || continue
			case "$(basename "$f" | tr '[:lower:]' '[:upper:]')" in
			NOTICE*)
				printf '  Notice:\n'
				sed 's/\r$//; s/^\(.\)/    \1/' "$f"
				;;
			esac
		done
		if [ "${#EMIT_NOTICES[@]}" -gt 0 ]; then
			for f in "${EMIT_NOTICES[@]}"; do
				printf '  Notice:\n'
				sed 's/\r$//; s/^\(.\)/    \1/' "$f"
			done
		fi
	} >> "$WORK/notices-pkgs.txt"

	printf 'Relationship: SPDXRef-DOCUMENT DESCRIBES %s\n' "$id" >> "$WORK/rels.txt"

	# Index row. The concluded expression drives the displayed identifier and
	# the LICENSES/ links; declared==concluded for Rust.
	disp="$concluded"
	[ "$disp" = "NOASSERTION" ] && disp="$declared"
	printf '| `%s` | `%s` | %s | %s | `%s` | %s |\n' \
		"$name" "$ver" "$eco" "$usedby" "$disp" "$(index_links "$disp")" \
		>> "$WORK/index-rows.txt"
}

# Crates named like Aggrega's own (the shared first-party rule) are first-party.
: > "$WORK/rust-third.txt"
while read -r name ver; do
	[ -n "$name" ] || continue
	third_party_is_first_party_crate "$name" || printf '%s %s\n' "$name" "$ver" >> "$WORK/rust-third.txt"
done < "$WORK/rust.txt"
mv "$WORK/rust-third.txt" "$WORK/rust.txt"

# --- Go standard library -----------------------------------------------------

# One entry: the toolchain that compiles the host and every Go plugin. Its
# version is go.mod's toolchain directive, else its go directive; its license
# and copyright lines are the LICENSE file at the Go root.
go_std_ver="$(awk '$1 == "toolchain" { print $2; exit }' go.mod)"
[ -n "$go_std_ver" ] || go_std_ver="$(awk '$1 == "go" { print $2; exit }' go.mod)"
go_std_ver="go${go_std_ver#go}"
go_root="$(env $THIRD_PARTY_GO_ENV go env GOROOT)"
if [ ! -f "$go_root/LICENSE" ]; then
	echo "gen-third-party-licenses: ERROR: the Go standard library has no LICENSE file under $go_root" >&2
	exit 1
fi
go_std_usedby="$(awk -F'\t' '{ printf "%s%s", (NR > 1 ? ", " : ""), $1 }' "$WORK/go-sets.list")"
emit_pkg "go" "Go standard library" "$go_std_ver" "NOASSERTION" "BSD-3-Clause" \
	"https://go.dev/dl/" "pkg:golang/stdlib@$go_std_ver" "$go_std_usedby" "$go_root/LICENSE"

# --- Rust standard library and wasi-libc ------------------------------------

# What each embedded Rust guest's wasm32-wasip1 build links in from its
# toolchain. One standard-library entry per channel a guest pins, and one
# wasi-libc entry (with the works it carries that the default build links) per
# commit the channels' Rust releases build their libc from. The texts are the
# pinned copies under third-party/toolchain/, so this stays offline.
TOOLCHAIN_DIR="third-party/toolchain"

# toolchain_missing <channel> <what> : stop, naming the channel and the fetch command.
toolchain_missing() {
	echo "gen-third-party-licenses: ERROR: Rust $1: $2 (nothing written); fetch it with: bash scripts/fetch-toolchain-notices.sh $1" >&2
	exit 1
}

: > "$WORK/wasi-libc.list"   # "<commit><TAB><wasi-sdk><TAB><channel><TAB><labels>" per channel
if [ -s "$WORK/rust-channels.list" ]; then
	while read -r channel; do
		[ -n "$channel" ] || continue
		usedby="$(awk -F'\t' -v c="$channel" '$1 == c { printf "%s%s", (n++ ? ", " : ""), $2 }' "$WORK/rust-channels.list")"
		row="$(awk -F'\t' -v c="$channel" '$1 == c { print $2 "\t" $3; exit }' "$TOOLCHAIN_DIR/toolchains.tsv" 2>/dev/null || true)"
		[ -n "$row" ] || toolchain_missing "$channel" "$TOOLCHAIN_DIR/toolchains.tsv has no row for it"
		sdk="${row%%$'\t'*}"
		commit="${row#*$'\t'}"
		rust_copyright="$TOOLCHAIN_DIR/rust-$channel/COPYRIGHT"
		[ -f "$rust_copyright" ] || toolchain_missing "$channel" "$rust_copyright is missing"
		for f in LICENSE LICENSE-MIT LICENSE-APACHE-LLVM libc-top-half/musl/COPYRIGHT \
			libc-bottom-half/cloudlibc/LICENSE fts/musl-fts/COPYING; do
			[ -f "$TOOLCHAIN_DIR/wasi-libc-$commit/$f" ] || toolchain_missing "$channel" "$TOOLCHAIN_DIR/wasi-libc-$commit/$f is missing"
		done
		EMIT_NO_SRC=1
		EMIT_NOTICES=("$rust_copyright")
		emit_pkg "rust" "Rust standard library" "$channel" "MIT OR Apache-2.0" "MIT OR Apache-2.0" \
			"https://github.com/rust-lang/rust/tree/$channel/library" "pkg:generic/rust-standard-library@$channel" \
			"$usedby" "$rust_copyright"
		EMIT_NOTICES=(); unset EMIT_NO_SRC
		printf '%s\t%s\t%s\t%s\n' "$commit" "$sdk" "$channel" "$usedby" >> "$WORK/wasi-libc.list"
	done < <(cut -f1 "$WORK/rust-channels.list" | sort -uV)

	while read -r commit; do
		[ -n "$commit" ] || continue
		sdk="$(awk -F'\t' -v c="$commit" '$1 == c { print $2; exit }' "$WORK/wasi-libc.list")"
		channels="$(awk -F'\t' -v c="$commit" '$1 == c { printf "%s%s", (n++ ? ", " : ""), $3 }' "$WORK/wasi-libc.list")"
		usedby="$(awk -F'\t' -v c="$commit" '$1 == c { printf "%s%s", (n++ ? ", " : ""), $4 }' "$WORK/wasi-libc.list")"
		wl="$TOOLCHAIN_DIR/wasi-libc-$commit"
		wl_url="https://github.com/WebAssembly/wasi-libc/tree/$commit"
		wl_purl="pkg:github/webassembly/wasi-libc@$commit"
		note="the libc of the wasm32-wasip1 target of Rust $channels, built from $sdk"
		EMIT_NO_SRC=1
		EMIT_NOTE="$note"
		EMIT_NOTICES=("$wl/LICENSE")
		emit_pkg "c" "wasi-libc" "$commit" "Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT" \
			"Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT" "$wl_url" "$wl_purl" "$usedby" "$wl/LICENSE"
		EMIT_NOTE="part of wasi-libc $commit; $note"
		EMIT_NOTICES=("$wl/libc-top-half/musl/COPYRIGHT")
		emit_pkg "c" "musl (in wasi-libc)" "$commit" "MIT" "MIT" "$wl_url/libc-top-half/musl" \
			"$wl_purl#libc-top-half/musl" "$usedby" "$wl/libc-top-half/musl/COPYRIGHT"
		EMIT_NOTICES=()
		emit_pkg "c" "cloudlibc (in wasi-libc)" "$commit" "BSD-2-Clause" "BSD-2-Clause" "$wl_url/libc-bottom-half/cloudlibc" \
			"$wl_purl#libc-bottom-half/cloudlibc" "$usedby" "$wl/libc-bottom-half/cloudlibc/LICENSE"
		emit_pkg "c" "musl-fts (in wasi-libc)" "$commit" "BSD-3-Clause" "BSD-3-Clause" "$wl_url/fts/musl-fts" \
			"$wl_purl#fts/musl-fts" "$usedby" "$wl/fts/musl-fts/COPYING"
		EMIT_NOTICES=(); unset EMIT_NOTE EMIT_NO_SRC
	done < <(cut -f1 "$WORK/wasi-libc.list" | sort -u)
fi

# --- Go dependencies --------------------------------------------------------

while IFS='|' read -r path ver dir; do
	[ -n "$path" ] || continue

	usedby="$(used_by "$WORK/go-sets.list" "$path|$ver|$dir")"

	mapfile -t lfiles < <(license_files "$dir")
	if [ "${#lfiles[@]}" -eq 0 ]; then
		printf 'Go module %s@%s: no license file (directory: %s)\n' "$path" "$ver" "${dir:-none}" >> "$WORK/failures.txt"
	fi

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
	cdir=""
	for reg in "${CARGO_REGS[@]}"; do
		if [ -d "$reg$name-$ver" ]; then cdir="$reg$name-$ver"; break; fi
	done
	if [ -z "$cdir" ]; then
		printf 'Rust crate %s-%s: no source directory in any cargo registry under %s; run `cargo fetch --locked` in %s\n' "$name" "$ver" "${CARGO_HOME:-$HOME/.cargo}" "$(fetch_dirs "$(used_by "$WORK/rust-sets.list" "$name $ver")")" >> "$WORK/failures.txt"
	fi

	declared="NOASSERTION"
	if [ -f "$cdir/Cargo.toml" ]; then
		declared="$({ grep -m1 '^license = ' "$cdir/Cargo.toml" || true; } | sed -E 's/^license = "(.*)"$/\1/')"
		[ -n "$declared" ] || declared="NOASSERTION"
	fi

	# A Rust SPDX expression is permissive unless every disjunctive option is
	# copyleft; flag only when no permissive option exists.
	if ! printf '%s' "$declared" | grep -Eqi 'MIT|Apache|BSD|ISC|Zlib|Unlicense|MPL|0BSD|BSL|CC0|Unicode'; then
		printf '%s@%s: %s\n' "$name" "$ver" "$declared" >> "$WORK/rust-findings.txt"
	fi

	mapfile -t lfiles < <(license_files "$cdir")
	if [ -n "$cdir" ] && [ "${#lfiles[@]}" -eq 0 ]; then
		if [ "$declared" = "NOASSERTION" ]; then
			printf 'Rust crate %s-%s: no license file and no license field in %s\n' "$name" "$ver" "$cdir" >> "$WORK/failures.txt"
		else
			printf '%s-%s (%s)\n' "$name" "$ver" "$declared" >> "$WORK/rust-nofile.txt"
		fi
	fi

	dl="https://static.crates.io/crates/$name/$name-$ver.crate"
	purl="pkg:cargo/$name@$ver"

	emit_pkg "rust" "$name" "$ver" "$declared" "$(elect_permissive "$declared")" "$dl" "$purl" \
		"$(used_by "$WORK/rust-sets.list" "$name $ver")" "${lfiles[@]}"
done < "$WORK/rust.txt"

if [ -s "$WORK/failures.txt" ]; then
	echo "gen-third-party-licenses: ERROR: dependencies with no resolvable license text (nothing written):" >&2
	sed 's/^/  /' "$WORK/failures.txt" >&2
	exit 1
fi

# --- LICENSES/ : one canonical body per unique referenced SPDX id -----------

sort -u "$WORK/ids-raw.txt" | grep -v '^$' > "$WORK/ids.txt"

# Built beside the other work files and moved into place only once every
# referenced id has a body, so a failure leaves LICENSES/ as it was.
mkdir -p "$WORK/LICENSES"

missing=""
while read -r id; do
	[ -n "$id" ] || continue
	out="$WORK/LICENSES/$id.txt"
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

rm -rf "$LICDIR"
mv "$WORK/LICENSES" "$LICDIR"

# --- SBOM (SPDX 2.3 tag-value) ----------------------------------------------

{
	printf 'SPDXVersion: SPDX-2.3\n'
	printf 'DataLicense: CC0-1.0\n'
	printf 'SPDXID: SPDXRef-DOCUMENT\n'
	printf 'DocumentName: aggrega-cli-third-party\n'
	printf 'DocumentNamespace: https://aggrega.ai/spdx/aggrega-cli-third-party\n'
	printf 'Creator: Tool: gen-third-party-licenses.sh\n'
	printf 'Created: %s\n' "$FIXED_TS"
	printf 'DocumentComment: <text>Third-party dependencies compiled into the shipped Aggrega CLI (the Go host binary and the WebAssembly plugins it embeds: %s). Generated deterministically; the creation timestamp is fixed so re-runs are byte-identical. License bodies are in the sibling LICENSES/ directory. %s%s.</text>\n' "$embedded_labels" "$THIRD_PARTY_DIGEST_PREFIX" "$inputs_digest"
	cat "$WORK/sbom-pkgs.txt"
	printf '\n'
	cat "$WORK/rels.txt"
} > "$SBOM"

# --- notices (plain text, embedded in the binary) ----------------------------

# Built beside the other work files and moved into place with its embedded copy.
{
	printf 'Third-party notices for the aggrega binary\n\n'
	printf 'This file lists every third-party dependency compiled into the aggrega binary:\n'
	printf 'the host (aggrega) and each embedded plugin, named <socket>/<name> (%s).\n' "$embedded_labels"
	printf 'It is generated from the dependency set the binary is built from; do not edit it.\n\n'
	printf 'Each dependency below names its version, its license, the copyright lines found in\n'
	printf 'its package, any notice file it ships and what it is compiled into. The text of\n'
	printf 'each license follows once, under its SPDX identifier.\n\n'
	printf '================================================================\n'
	printf 'Dependencies\n'
	printf '================================================================\n'
	cat "$WORK/notices-pkgs.txt"
	printf '\n================================================================\n'
	printf 'License texts\n'
	printf '================================================================\n'
	while read -r id; do
		[ -n "$id" ] || continue
		printf '\n--- %s ---\n\n' "$id"
		cat "$LICDIR/$id.txt"
	done < "$WORK/ids.txt"
} > "$WORK/notices.txt"
cp "$WORK/notices.txt" "$NOTICES.new"
if [ -n "$NOTICES_EMBED" ]; then
	mkdir -p "$(dirname "$NOTICES_EMBED")"
	cp "$WORK/notices.txt" "$NOTICES_EMBED.new"
	mv "$NOTICES_EMBED.new" "$NOTICES_EMBED"
fi
mv "$NOTICES.new" "$NOTICES"

# --- thin index -------------------------------------------------------------

go_count="$(( $(wc -l < "$WORK/go-union.txt" | tr -d ' ') + 1 ))"
rust_count="$(wc -l < "$WORK/rust.txt" | tr -d ' ')"
lic_count="$(wc -l < "$WORK/ids.txt" | tr -d ' ')"

{
	cat <<'HEADER'
# Third-Party Licenses

This is the attribution index for every third-party dependency compiled into
the shipped Aggrega CLI artifacts.

Shipped artifacts covered:

HEADER
	cat "$WORK/covered.md"
	templated_text=""
	for t in $TEMPLATED; do
		[ "$t" = "BSL-1.0" ] || templated_text="${templated_text:+$templated_text, }$t"
	done
	cat <<'HEADER' | sed -e "s/HEADER_TEMPLATED/$templated_text/" -e "s|TARGETS_PLACEHOLDER|${THIRD_PARTY_HOST_TARGETS// /, }|"

**These files are generated. Do not edit them by hand.** Regenerate with:

```sh
./scripts/gen-third-party-licenses.sh
```

run from the `aggrega-cli` module root.

Attribution is split across four artifacts, in a REUSE/SPDX-style layout:

- `LICENSES/<SPDX>.txt` — the full text of each distinct license, stored once
  per SPDX identifier. For license families that embed a per-package copyright
  line (HEADER_TEMPLATED) the stored body is the canonical
  template with a placeholder copyright line; the real per-package copyright is
  recorded in the SBOM and in the notices below.
- `THIRD-PARTY.spdx` — a machine-readable SPDX 2.3 SBOM with one entry per
  dependency: name, version, SPDX license identifier, per-package copyright, and
  package locator.
- `THIRD-PARTY-NOTICES.txt` — the plain-text notices `aggrega version --licenses`
  prints: per dependency its version, license, copyright lines, any notice file
  it ships and what it is compiled into, then each license text once.
- this file — a human-readable index linking each dependency to its license
  text.

The generator enumerates the dependency set from two sources: for Go, the
packages `go list -deps` reaches from `cmd/aggrega` for each of the release's
targets (TARGETS_PLACEHOLDER) and from each embedded Go plugin; for Rust, every
crate in each embedded plugin's `Cargo.lock`. It resolves each license from the
local module and registry caches, and emits all four artifacts deterministically
(stable sort, fixed SBOM timestamp; two runs are byte-identical). First-party
Aggrega modules and crates are excluded.

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
	echo "  $NOTICES${NOTICES_EMBED:+ (and $NOTICES_EMBED)}"
	echo "  $LICDIR/ ($lic_count license files)"
	echo "  Go third-party modules : $go_count"
	echo "  Rust third-party crates: $rust_count"
	echo "  TypeScript             : 0 (nothing compiled into the CLI)"
	echo "  Non-permissive/unresolved findings: Go=$go_forbidden Rust=$rust_forbidden"
	if [ "$go_forbidden" != 0 ]; then
		echo "  Go findings:"
		sed 's/^/    /' "$WORK/go-findings.txt"
	fi
	if [ -s "$WORK/rust-nofile.txt" ]; then
		echo "  Rust crates shipping no license file (license field of Cargo.toml stands, copyright NOASSERTION): $(wc -l < "$WORK/rust-nofile.txt" | tr -d ' ')"
	fi
	if [ "$rust_forbidden" != 0 ]; then
		echo "  Rust findings:"
		sed 's/^/    /' "$WORK/rust-findings.txt"
	fi
} >&2
