#!/usr/bin/env bash
# aggrega-cli/scripts/coverage-gate.sh
#
# Enforces per-package coverage floors declared in scripts/coverage-baseline.txt.
# Reads each "<package> <floor>" entry, runs `go test -cover ./...` once, and
# compares the reported coverage to the declared floor. Exits 0 when every
# listed package meets or exceeds its floor; exits 1 with a per-package
# diagnostic when any package is below.
#
# Packages NOT listed in the manifest are skipped silently. New packages cross
# the 90% line and earn a manifest entry through deliberate authoring; the gate
# does not implicitly enforce floors on un-listed packages because the manifest
# is the single source of truth for "this package's floor is intentional".
#
# Usage:
#   scripts/coverage-gate.sh          # standard gate; per-package diagnostic on failure
#   scripts/coverage-gate.sh -v       # also prints `go test` raw output
#   scripts/coverage-gate.sh --cross  # also reports the cross-package combined coverage
#                                     # via `-coverpkg=./...` for the cross-package floor
#                                     # check (≥85%). Slower; off by default.
#
# Exit codes:
#   0 — all listed packages meet their floor (and, if --cross, cross-package ≥85%)
#   1 — at least one package is below its floor, or cross-package check failed
#   2 — invocation error (missing manifest, malformed line)

set -euo pipefail
cd "$(dirname "$0")/.."

MANIFEST="scripts/coverage-baseline.txt"
CROSS_FLOOR=85

print_help() {
    cat <<'EOF'
coverage-gate.sh — per-package coverage floor gate

Reads scripts/coverage-baseline.txt, runs `go test -cover ./...` once, and
compares each listed package's reported coverage to its declared floor.

Usage:
  scripts/coverage-gate.sh          # standard gate; per-package diagnostic on failure
  scripts/coverage-gate.sh -v       # also prints `go test` raw output
  scripts/coverage-gate.sh --cross  # also reports the cross-package combined coverage
                                    # via `-coverpkg=./...` for the cross-package floor
                                    # check (>=85%). Slower; off by default.
  scripts/coverage-gate.sh -h       # this help

Packages with achieved coverage but no manifest entry surface as WARN lines.
The warning does not change exit status; it surfaces drift between the
codebase and the manifest so the manifest stays in sync as packages are
added or renamed.

Exit codes:
  0 — all listed packages meet their floor (and, if --cross, cross-package >=85%)
  1 — at least one package is below its floor, or cross-package check failed
  2 — invocation error (missing manifest, malformed line, unknown flag)
EOF
}

verbose=0
cross=0
for arg in "$@"; do
    case "$arg" in
        -v|--verbose) verbose=1 ;;
        --cross)      cross=1   ;;
        -h|--help)
            print_help
            exit 0
            ;;
        *)
            echo "coverage-gate: unknown argument: $arg" >&2
            exit 2
            ;;
    esac
done

if [[ ! -f "$MANIFEST" ]]; then
    echo "coverage-gate: manifest not found: $MANIFEST" >&2
    exit 2
fi

# Run the test suite once with coverage. Capture both stdout and stderr so
# build failures surface in the diagnostic; if any test fails outright the
# gate fails-fast before per-package floor parsing.
echo "coverage-gate: running test suite..."
if ! test_output=$(go test -cover ./... 2>&1); then
    echo "coverage-gate: test suite failed; coverage gate cannot run." >&2
    echo "$test_output" >&2
    exit 1
fi

if [[ "$verbose" -eq 1 ]]; then
    echo "$test_output"
    echo ""
fi

fails=0
warns=0
checked=0
listed_packages=""
while IFS= read -r line; do
    # Skip blank lines and comments.
    [[ -z "$line" ]] && continue
    [[ "$line" =~ ^[[:space:]]*# ]] && continue

    # Parse "<package> <floor>".
    pkg=$(echo "$line" | awk '{print $1}')
    floor=$(echo "$line" | awk '{print $2}')
    if [[ -z "$pkg" || -z "$floor" ]]; then
        echo "coverage-gate: malformed manifest line: $line" >&2
        exit 2
    fi
    if ! [[ "$floor" =~ ^[0-9]+$ ]]; then
        echo "coverage-gate: floor must be an integer (got $floor on $pkg)" >&2
        exit 2
    fi
    listed_packages="$listed_packages $pkg"

    # Find the matching go-test output line. `go test -cover` emits:
    #   ok  github.com/.../pkg  0.012s  coverage: NN.N% of statements
    # The leading "ok" can also be "FAIL"; the test-suite-failed early-exit
    # above caught that case, so here we expect ok-prefixed lines only.
    pct_line=$(echo "$test_output" | awk -v p="$pkg" '$1=="ok" && $2==p { print }')
    if [[ -z "$pct_line" ]]; then
        echo "FAIL  $pkg: not present in test output (package missing or has no tests)"
        fails=$((fails + 1))
        continue
    fi

    # Extract the numeric percentage. The string "coverage: NN.N%" is the
    # canonical shape; defensive against go's "[no statements]" fallback for
    # doc-only packages by treating it as 100% (the package has no testable
    # surface).
    if echo "$pct_line" | grep -q '\[no statements\]'; then
        pct=100.0
    else
        pct=$(echo "$pct_line" | grep -oE 'coverage:[[:space:]]+[0-9]+\.[0-9]+%' | grep -oE '[0-9]+\.[0-9]+')
    fi
    if [[ -z "$pct" ]]; then
        echo "FAIL  $pkg: could not parse coverage from: $pct_line"
        fails=$((fails + 1))
        continue
    fi

    # Float comparison via awk (bash's [[ ]] doesn't do floats).
    ok=$(awk -v p="$pct" -v f="$floor" 'BEGIN { print (p + 0 >= f + 0) ? "ok" : "fail" }')
    if [[ "$ok" != "ok" ]]; then
        printf "FAIL  %-65s coverage %5s%% < floor %s%%\n" "$pkg" "$pct" "$floor"
        fails=$((fails + 1))
    else
        printf "OK    %-65s coverage %5s%% >= floor %s%%\n" "$pkg" "$pct" "$floor"
    fi
    checked=$((checked + 1))
done < "$MANIFEST"

# Drift detection: walk every package in the test output that reported a
# numeric coverage value, and warn when one is NOT in the manifest. The
# manifest is the SSOT for "this package's floor is intentional"; an
# unlisted package with achieved coverage is either freshly added (needs
# manifest entry) or accidentally dropped (manifest needs update). Either
# case is contributor signal, not a build failure — the WARN surfaces in
# CI logs without changing exit status.
while IFS= read -r out_line; do
    # Match only well-formed `ok <pkg> ... coverage: NN.N% of statements`
    # lines; skip [no statements] (doc-only packages) and the [no test
    # files] notices Go emits for testless packages.
    if [[ "$out_line" =~ ^ok[[:space:]]+([^[:space:]]+).*coverage:[[:space:]]+([0-9]+\.[0-9]+)% ]]; then
        out_pkg="${BASH_REMATCH[1]}"
        out_pct="${BASH_REMATCH[2]}"
        # Skip packages already in the manifest.
        if [[ " $listed_packages " == *" $out_pkg "* ]]; then
            continue
        fi
        # Skip 0.0% — packages with no statements but go test reports them
        # via the [no statements] fallback already handled above; this
        # arm catches packages with statements but no tests yet (rare —
        # `go test` usually reports `[no test files]` for these).
        zero=$(awk -v p="$out_pct" 'BEGIN { print (p + 0 == 0) ? "yes" : "no" }')
        if [[ "$zero" == "yes" ]]; then
            continue
        fi
        printf "WARN  %-65s coverage %5s%% — not in manifest (add to scripts/coverage-baseline.txt if intentional)\n" "$out_pkg" "$out_pct"
        warns=$((warns + 1))
    fi
done <<< "$test_output"

echo ""
if [[ $warns -gt 0 ]]; then
    echo "coverage-gate: checked $checked package(s); $fails below floor; $warns unlisted"
else
    echo "coverage-gate: checked $checked package(s); $fails below floor"
fi

# Cross-package coverage check (optional, --cross flag). Combines per-test
# coverage of every test binary against every package via -coverpkg=./..., so
# that a test in pkg A covering code in pkg B is credited correctly. The
# combined number is the load-bearing coverage aggregate for the repository;
# the per-package floors above are the per-package complement.
if [[ "$cross" -eq 1 ]]; then
    echo ""
    echo "coverage-gate: computing cross-package coverage (-coverpkg=./...)..."
    tmp_profile=$(mktemp)
    trap 'rm -f "$tmp_profile"' EXIT
    if ! cross_output=$(go test -coverprofile="$tmp_profile" -coverpkg=./... ./... 2>&1); then
        echo "coverage-gate: cross-package run failed:" >&2
        echo "$cross_output" >&2
        exit 1
    fi
    cross_pct=$(go tool cover -func="$tmp_profile" | awk '$1=="total:" { print $NF }' | tr -d '%')
    if [[ -z "$cross_pct" ]]; then
        echo "coverage-gate: could not parse cross-package coverage from `go tool cover` output" >&2
        exit 1
    fi
    cross_ok=$(awk -v p="$cross_pct" -v f="$CROSS_FLOOR" 'BEGIN { print (p + 0 >= f + 0) ? "ok" : "fail" }')
    if [[ "$cross_ok" != "ok" ]]; then
        printf "FAIL  cross-package coverage %5s%% < floor %s%%\n" "$cross_pct" "$CROSS_FLOOR"
        fails=$((fails + 1))
    else
        printf "OK    cross-package coverage %5s%% >= floor %s%%\n" "$cross_pct" "$CROSS_FLOOR"
    fi
fi

if [[ $fails -gt 0 ]]; then
    exit 1
fi
exit 0
