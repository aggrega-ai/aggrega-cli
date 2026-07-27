// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Guards the two-home rule list: the rule identifiers this package dispatches
// and the identifiers the publishing-linter guest answers to. The two lists live
// in different languages and cannot import each other, so they were kept in step
// by a comment. A comment is unobservable to every gate: a rule registered on one
// side only produced either a dispatched identifier the guest reports as
// unimplemented, or a guest rule nothing ever asks for. This reads the guest's
// dispatch table as text and compares the sets.

package lintops

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// guestDispatchSource is the guest's rule-dispatch table.
const guestDispatchSource = "../../plugins/publishing-linter/src/lib.rs"

// guestDispatchArm matches one arm of the guest's dispatch table: a quoted rule
// identifier followed by the match arrow.
var guestDispatchArm = regexp.MustCompile(`"([a-z0-9-]+)"\s*=>`)

// TestRuleIDsMatchGuestDispatchTable pins the rule set this package dispatches
// against the set the guest implements. The pre-publish list is the full one
// (default mode omits only the rule that needs previously-published bytes), so it
// is the set compared.
func TestRuleIDsMatchGuestDispatchTable(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.FromSlash(guestDispatchSource))
	if err != nil {
		t.Skipf("guest source not reachable at %s (standalone checkout): %v", guestDispatchSource, err)
	}
	source := string(raw)

	entry := strings.Index(source, "pub fn lint_rules(")
	if entry < 0 {
		t.Fatalf("the guest's dispatch entry point was not found in %s; the guest's shape moved and this guard needs re-aiming", guestDispatchSource)
	}
	guest := map[string]bool{}
	for _, m := range guestDispatchArm.FindAllStringSubmatch(source[entry:], -1) {
		guest[m[1]] = true
	}
	if len(guest) == 0 {
		t.Fatalf("no dispatch arms parsed out of %s; the guard would pass vacuously", guestDispatchSource)
	}

	host := map[string]bool{}
	for _, id := range defaultRuleIDs(true) {
		host[id] = true
	}

	var hostOnly, guestOnly []string
	for id := range host {
		if !guest[id] {
			hostOnly = append(hostOnly, id)
		}
	}
	for id := range guest {
		if !host[id] {
			guestOnly = append(guestOnly, id)
		}
	}
	sort.Strings(hostOnly)
	sort.Strings(guestOnly)

	if len(hostOnly) > 0 {
		t.Errorf("dispatched but not implemented by the guest: %v — the guest answers an unknown identifier with an unimplemented finding, so the rule silently never runs", hostOnly)
	}
	if len(guestOnly) > 0 {
		t.Errorf("implemented by the guest but never dispatched: %v — add the identifier to defaultRuleIDs or the rule is dead", guestOnly)
	}
}
