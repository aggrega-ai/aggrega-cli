// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Pins the composition-keyword sets this reader descends against the copies the
// publishing-linter guest descends.
//
// Two readers answer "which leaves does this schema declare sensitive" and
// neither can call the other: this one reads the compiled schema, where the
// declaration has become an extension keyword, while the guest reads the
// authored entity because it cannot run schema compilation. Which keywords each
// side treats as a composition branch is a CLOSED SET OF LITERALS, and a set of
// literals is pinnable to the other side's text even when the behaviour is not —
// so it is pinned, in both directions.
//
// The direction matters. The guest's own test reads this file's sets and compares
// them to its copies, which catches drift for anyone running the guest's suite.
// This test is the mirror, and it is the one that catches drift for the compiler's
// own suite. Pinning one direction only would be a test that passes while the
// sides drift: a keyword added here and not there leaves a declaration collected
// on one side alone, so the reader that publishes advice and the reader that
// withholds a value disagree about what an author declared.
//
// The per-case behavioural half of the same contract lives at
// internal/lintops/sensitivity_parity_test.go, over the shared case file both
// sides enumerate.

package resolver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// guestSensitivitySource is the guest's copy of the reader.
const guestSensitivitySource = "../../plugins/publishing-linter/src/sensitivity.rs"

// guestKeywordConst matches one of the guest's closed keyword sets: the const
// name, then the bracketed literal list.
var guestKeywordConst = regexp.MustCompile(`(?s)pub const (BRANCH_LIST_KEYWORDS|BRANCH_NODE_KEYWORDS): &\[&str\] = &\[(.*?)\];`)

// guestQuotedLiteral matches one double-quoted literal.
var guestQuotedLiteral = regexp.MustCompile(`"([^"]*)"`)

// TestCompositionKeywordSetsMatchTheGuestCopies compares each of this reader's
// branch-keyword sets to the guest's copy of it. The two sets are pinned
// separately and both are required: one side descending the list-valued keywords
// and not the single-schema ones would pass a pin over their union while
// collecting a different set of declarations.
func TestCompositionKeywordSetsMatchTheGuestCopies(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.FromSlash(guestSensitivitySource))
	if err != nil {
		t.Skipf("guest source not reachable at %s (standalone checkout): %v", guestSensitivitySource, err)
	}

	guest := map[string][]string{}
	for _, m := range guestKeywordConst.FindAllStringSubmatch(string(raw), -1) {
		var literals []string
		for _, line := range strings.Split(m[2], "\n") {
			if i := strings.Index(line, "//"); i >= 0 {
				line = line[:i]
			}
			for _, lit := range guestQuotedLiteral.FindAllStringSubmatch(line, -1) {
				literals = append(literals, lit[1])
			}
		}
		guest[m[1]] = literals
	}
	if len(guest) != 2 {
		t.Fatalf("parsed %d keyword sets out of %s, want 2 (BRANCH_LIST_KEYWORDS and BRANCH_NODE_KEYWORDS); the guest's declaration shape moved and this pin needs re-aiming rather than passing vacuously", len(guest), guestSensitivitySource)
	}

	for _, pair := range []struct {
		guestName string
		ours      []string
	}{
		{"BRANCH_LIST_KEYWORDS", branchListKeywords},
		{"BRANCH_NODE_KEYWORDS", branchNodeKeywords},
	} {
		theirs := guest[pair.guestName]
		if len(theirs) == 0 {
			t.Errorf("%s parsed empty out of the guest; a pin over an empty set holds no matter what this reader descends", pair.guestName)
			continue
		}
		if !sameKeywordSet(pair.ours, theirs) {
			t.Errorf("the %s set drifted: this reader descends %v, the guest descends %v. A keyword one side descends and the other does not leaves a declaration collected on one side only.",
				pair.guestName, sortedCopy(pair.ours), sortedCopy(theirs))
		}
	}
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func sameKeywordSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, k := range a {
		seen[k]++
	}
	for _, k := range b {
		seen[k]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
