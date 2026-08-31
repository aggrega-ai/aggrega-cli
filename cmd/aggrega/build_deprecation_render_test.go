// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The deprecation verdict itself is the shared layer's and is proved there. What
// this file covers is the half that is only observable HERE: the command line's
// own rendering of the halt the verdict returns, through the terminal formatter
// this package owns.
// TestBuildHaltRendersItsWholeMessageToTheTerminal guards the halt's rendered
// form, not just its error value. The CLI renders a multi-error as its wrapped
// errors alone — so a halt message wrapping TWO errors reaches the terminal as
// the sentinel and the policy clause with everything between them dropped: the
// entity, the severity, the message, and the observation moment. Asserting on
// err.Error() cannot see that, because Go renders the format string there
// regardless. This asserts on what the user actually reads.
func TestBuildHaltRendersItsWholeMessageToTheTerminal(t *testing.T) {
	const stamp = "2026-07-01T09:30:00Z"
	lf := deprecationHaltLockfile("oci://reg.example/components/api", "retracted", "withdrawn: bad release", stamp)

	err := compilerops.LockfileDeprecationVerdict(lf, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: "retracted"}, func(string) {})
	if err == nil {
		t.Fatal("a recorded marker at the configured threshold must halt the build")
	}

	rendered := formatCompilerError(err)
	for _, want := range []string{
		"components/api",
		"is retracted",
		"withdrawn: bad release",
		"as of " + stamp,
		"aggrega pull",
		`halt_on_deprecated_severity is "retracted"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered halt drops %q — the message reached the terminal incomplete:\n%s", want, rendered)
		}
	}
}

// deprecationHaltLockfile builds the one lockfile shape this file needs: a row
// carrying a recorded marker at the severity the test configures a halt for.
func deprecationHaltLockfile(uri, severity, message, generatedAt string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		GeneratedAt: generatedAt,
		Entities: []lockfile.Entity{{
			URI:         uri,
			Deprecation: &lockfile.DeprecationRecord{Severity: severity, Message: message},
		}},
	}
}
