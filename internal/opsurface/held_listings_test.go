// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opsurface

import (
	"sort"
	"strings"
	"testing"
)

// TestTheListingsAnsweredOverAHeldConfigurationAreTheTwoListings holds the
// declaration every surface reads to decide what it answers for a workspace whose
// configuration is held: it names operations that exist — an identifier that
// names none would let no method or tool through and hide that it does nothing —
// and exactly the entity index and the Release listing, the two operations that
// name what they did not read. Widening it is a decision about what may run with
// nothing configured, so it fails here rather than passing silently.
func TestTheListingsAnsweredOverAHeldConfigurationAreTheTwoListings(t *testing.T) {
	t.Parallel()
	var declared []string
	for id := range listingsOfWorkspaceContent {
		if _, known := Lookup(id); !known {
			t.Errorf("%q is declared as answered over a held configuration and is not an operation", id)
		}
		declared = append(declared, id)
	}
	sort.Strings(declared)
	if got := strings.Join(declared, ","); got != "release-candidates,workspace-entity-index" {
		t.Errorf("the operations answered over a held configuration are %s; want the Release listing and the entity "+
			"index alone — any other is decided by the configuration the hold keeps unread", got)
	}
	answered := 0
	for _, op := range Operations() {
		if AnswersOverHeldConfiguration(op.ID) {
			answered++
		}
	}
	if answered != len(listingsOfWorkspaceContent) {
		t.Errorf("%d operation(s) are answered over a held configuration; the declaration names %d", answered,
			len(listingsOfWorkspaceContent))
	}
}
