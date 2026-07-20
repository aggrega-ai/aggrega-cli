// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"strings"
	"testing"
)

// TestRenderValidateText_VerdictInvariantAcrossVerbosity proves the release-scope
// render honors the same detailed/concise vocabulary the CLI uses and that
// verbosity governs volume only: for one diagnostic-bearing payload the detailed
// form lists every diagnostic and then the advisory notices, while the concise
// form reports the count and the first diagnostic and no advisories. The payload's
// verdict (Valid) is the same input to both, so the verdict is verbosity-invariant.
func TestRenderValidateText_VerdictInvariantAcrossVerbosity(t *testing.T) {
	p := validateResultPayload{
		Scope:       validateScopeRelease,
		Target:      "release.yaml",
		Release:     "release.yaml",
		Valid:       false,
		Diagnostics: []string{"first diagnostic", "second diagnostic"},
		Notices:     []string{"schema-pillar overlap advisory", "narrowing-on-extend advisory"},
	}

	detailed := renderValidateText(p, responseFormatDetailed)
	concise := renderValidateText(p, responseFormatConcise)

	for _, want := range []string{
		"first diagnostic",
		"second diagnostic",
		"notice: schema-pillar overlap advisory",
		"notice: narrowing-on-extend advisory",
	} {
		if !strings.Contains(detailed, want) {
			t.Errorf("detailed render missing %q\nfull:\n%s", want, detailed)
		}
	}

	if !strings.Contains(concise, "First: first diagnostic") {
		t.Errorf("concise render must carry the first diagnostic\nfull:\n%s", concise)
	}
	for _, absent := range []string{"notice:", "second diagnostic"} {
		if strings.Contains(concise, absent) {
			t.Errorf("concise render must not contain %q\nfull:\n%s", absent, concise)
		}
	}
}
