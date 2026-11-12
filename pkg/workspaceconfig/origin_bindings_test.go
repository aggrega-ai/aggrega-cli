// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import "testing"

// TestEveryBindingNamesAnAttributedSetting holds the environment and
// command-line tables to the provenance report's rows.
//
// A variable or a flag sets the setting its row names, and the report names the
// layer by looking that setting up among its rows. So a row naming a setting the
// report does not carry — a typo, or a setting renamed on one side only — would
// have its layer attributed nowhere: the variable or flag would go on changing
// the effective value while the report named whatever layer sat beneath it,
// and nothing else would notice.
func TestEveryBindingNamesAnAttributedSetting(t *testing.T) {
	t.Parallel()
	attributed := make(map[string]bool)
	for _, f := range originFields() {
		attributed[f.name] = true
	}
	if len(attributed) == 0 {
		t.Fatal("the report attributes no setting, so this check would pass over nothing")
	}

	for _, b := range envBindings {
		if !attributed[b.configPath] {
			t.Errorf("$%s sets %q, which the provenance report has no row for, so the variable is never named "+
				"as where a value came from", b.envVar, b.configPath)
		}
	}
	for _, b := range flagBindings {
		if !attributed[b.configPath] {
			t.Errorf("%s sets %q, which the provenance report has no row for, so the flag is never named as "+
				"where a value came from", b.label, b.configPath)
		}
	}
	if len(flagBindings) == 0 {
		t.Fatal("the command-line table is empty, so its half of this check measured nothing")
	}
}
