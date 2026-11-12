// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package deprecation_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
)

// TestStatedAsNamesTheSeverityAndNeverUnderstatesARetraction pins the wording every
// notice about a recorded severity reads.
//
// The retracted row carries the discriminating assertion. `retracted` and the two
// advisory severities mean different things — excluded from selection against
// discouraged but still selected — so reporting a retraction as "deprecated"
// understates what happened, and "deprecated" is the wording a notice falls into by
// default because it reads as the general word for all three.
func TestStatedAsNamesTheSeverityAndNeverUnderstatesARetraction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		severity deprecation.Severity
		want     string
	}{
		{"a retraction is stated as itself", deprecation.SeverityRetracted, "retracted"},
		{"the advisory tier names which one it is", deprecation.SeverityWarning, "deprecated (warning)"},
		{"and so does the stronger advisory tier", deprecation.SeverityError, "deprecated (error)"},
		{"a value outside the closed set stays legible as itself", deprecation.Severity("unheard-of"), "unheard-of"},
		{"and so does an empty one", deprecation.Severity(""), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.severity.StatedAs(); got != tc.want {
				t.Errorf("StatedAs(%q) = %q, want %q", tc.severity, got, tc.want)
			}
		})
	}

	if deprecation.SeverityRetracted.StatedAs() == deprecation.SeverityError.StatedAs() {
		t.Error("an exclusion and a discouragement must not read the same to a user")
	}
}
