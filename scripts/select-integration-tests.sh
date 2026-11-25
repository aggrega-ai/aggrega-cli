#!/usr/bin/env bash
# aggrega-cli/scripts/select-integration-tests.sh — select this module's
# integration-tagged tests: the packages to run with -tags integration and the
# -run filter naming exactly the tests the tag adds, checked against what go test
# itself selects. Every runner of the tagged tests takes its selection from here.
#
# The tag adds test files to a package; it removes none. Running the tagged
# packages with the tag alone would therefore run every untagged test in them
# too, so the filter names the tagged tests and nothing else.
#
# The test files the tag adds are the set difference between the test files
# `go list -f` reports with -tags integration and without it, so the build
# constraint is evaluated by the toolchain itself: a compound expression, a GOOS
# or GOARCH term or a file-name constraint is honoured wherever the integration
# term sits. `go list` does not type-check, so the selection holds on a tree that
# does not compile. It runs without -e: a `go list` that fails yields no file,
# which the first refusal below reports, rather than a partial set.
#
# The packages are the directories holding those files, ./-relative. The filter
# is ^(Name|Name|...)$ over the Test, Fuzz and Example functions the files
# declare, TestMain left out (it is the package's entry point and runs anyway);
# it is anchored because go test matches -run as a substring, and it names
# top-level tests, so each selected test runs with its subtests.
#
# Then go test is asked which of them the filter selects:
#     go test -tags integration -list <filter> <packages>
# which compiles every tagged test binary and runs it to list its tests (so the
# embedded guests must be in place, as for running them). A tagged test the
# listing lacks is refused, naming it: a filter that drops a test reports green
# without running it. An untagged test sharing a tagged test's name is selected
# too, which costs time and hides nothing.
#
# --part K/N keeps every Nth test of the sorted list, starting with the Kth, for
# a caller that runs the tagged tests in N parts side by side; the packages stay
# the whole set and the listing check covers that part's tests.
#
# Usage:
#   scripts/select-integration-tests.sh [--part K/N] <out-dir>
#   scripts/select-integration-tests.sh --help
#
#   <out-dir>  an existing directory; the selection is written there as
#              packages  one ./-relative package per line
#              run       the -run filter, one line
#
# It runs on the module holding it, wherever it is called from, under the Go
# workspace setting of the caller's environment. It prints what it found on
# standard error.
#
# Exit status: 0 the selection is written; 2 no file is added by the integration
# tag, the added files declare no test, the listing lacks a selected test, or an
# argument it cannot use.
set -euo pipefail

usage() { sed -n '2,/^# argument it cannot use\.$/p' "$0" | sed 's/^# \{0,1\}//'; }
refuse() { printf 'select-integration-tests: %s\n' "$*" >&2; exit 2; }
say() { printf 'select-integration-tests: %s\n' "$*" >&2; }

part_k=1 part_n=1 out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --part)
      [ "$#" -ge 2 ] || refuse "--part needs K/N"
      [[ "$2" =~ ^([1-9][0-9]*)/([1-9][0-9]*)$ ]] || refuse "--part takes K/N with 1 <= K <= N, not \"$2\""
      part_k="${BASH_REMATCH[1]}" part_n="${BASH_REMATCH[2]}"
      [ "$part_k" -le "$part_n" ] || refuse "--part takes K/N with 1 <= K <= N, not \"$2\""
      shift 2 ;;
    -*) refuse "unknown option: $1 (see --help)" ;;
    *) [ -z "$out" ] || refuse "takes one directory (see --help)"; out="$1"; shift ;;
  esac
done
[ -n "$out" ] || refuse "name the directory to write the selection to (see --help)"
[ -d "$out" ] || refuse "$out is not a directory"
out="$(cd "$out" && pwd)"

module="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$module"

# One path per test file, in-package (TestGoFiles) and external (XTestGoFiles)
# alike: a template naming only the first would drop every `package x_test` file.
# shellcheck disable=SC2016  # $d is a Go template variable, for go list to expand
tpl='{{$d := .Dir}}{{range .TestGoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}'
mapfile -t files < <(LC_ALL=C comm -13 \
  <(go list -f "$tpl" ./... | LC_ALL=C sort -u) \
  <(go list -tags integration -f "$tpl" ./... | LC_ALL=C sort -u) \
  | sed "s|^$module/|./|")
if [ "${#files[@]}" -eq 0 ]; then
  refuse "no test file is added by the integration tag: either the module carries none, or a go list failed (try: go list -tags integration ./...)"
fi
mapfile -t packages < <(printf '%s\n' "${files[@]}" | xargs -n1 dirname | LC_ALL=C sort -u)
say "${#files[@]} test files added by the integration tag, in ${#packages[@]} packages: ${packages[*]}"

mapfile -t tests < <(grep -h -oE '^func (Test|Fuzz|Example)[[:alnum:]_]*' "${files[@]}" \
  | awk '{print $2}' | grep -vx TestMain | LC_ALL=C sort -u)
[ "${#tests[@]}" -gt 0 ] || refuse "the files the integration tag adds declare no test; a filter would select nothing"
mapfile -t part < <(printf '%s\n' "${tests[@]}" | awk -v k="$part_k" -v n="$part_n" '(NR - 1) % n == k - 1')
[ "${#part[@]}" -gt 0 ] || refuse "part $part_k/$part_n of ${#tests[@]} tests holds none"
run="^($(IFS='|'; printf '%s' "${part[*]}"))\$"

mapfile -t listed < <(go test -tags integration -list "$run" "${packages[@]}" \
  | grep -E '^(Test|Fuzz|Example)[[:alnum:]_]*$' | LC_ALL=C sort -u)
mapfile -t missing < <(LC_ALL=C comm -23 <(printf '%s\n' "${part[@]}") <(printf '%s\n' "${listed[@]}"))
if [ "${#missing[@]}" -gt 0 ]; then
  refuse "go test does not list ${#missing[@]} tagged test(s) the filter names: ${missing[*]}; the files the tag adds and the test binaries disagree about the tests"
fi

printf '%s\n' "${packages[@]}" > "$out/packages"
printf '%s\n' "$run" > "$out/run"
say "${#part[@]} of ${#tests[@]} tagged tests selected (part $part_k/$part_n), each listed by go test"
