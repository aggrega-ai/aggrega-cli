// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestRenderReleaseValidation_VerdictInvariantAcrossVerbosity proves the
// output-shaping verbosity governs volume only: for the same diagnostic-bearing
// result, the detailed and concise renders return the identical verdict error and
// differ only in how much they print — detailed carries every diagnostic and the
// advisory notice lines; concise carries the count and the first diagnostic and no
// advisories.
func TestRenderReleaseValidation_VerdictInvariantAcrossVerbosity(t *testing.T) {
	vr := &compilerops.ValidateResult{
		ResolutionFindings: []error{
			errors.New("first pipeline diagnostic"),
			errors.New("second pipeline diagnostic"),
		},
		LintWarnings:      []string{"schema-pillar overlap advisory"},
		NarrowingWarnings: []string{"narrowing-on-extend advisory"},
	}

	var detailedOut, conciseOut bytes.Buffer
	detailedErr := renderReleaseValidation(vr, nil, nil, "release.yaml", compilerops.VerbosityDetailed, &detailedOut, &detailedOut)
	conciseErr := renderReleaseValidation(vr, nil, nil, "release.yaml", compilerops.VerbosityConcise, &conciseOut, &conciseOut)

	// Verdict invariant: the returned error is identical across verbosity.
	if detailedErr == nil || conciseErr == nil {
		t.Fatalf("expected a non-nil verdict from both forms; detailed=%v concise=%v", detailedErr, conciseErr)
	}
	if detailedErr.Error() != conciseErr.Error() {
		t.Fatalf("verdict must be identical across verbosity:\n detailed: %v\n concise:  %v", detailedErr, conciseErr)
	}
	if !strings.Contains(detailedErr.Error(), "2 diagnostic(s)") {
		t.Errorf("verdict must carry the diagnostic count; got %v", detailedErr)
	}

	// Detailed volume: every diagnostic and both advisory notice lines.
	d := detailedOut.String()
	for _, want := range []string{
		"first pipeline diagnostic",
		"second pipeline diagnostic",
		"notice: schema-pillar overlap advisory",
		"notice: narrowing-on-extend advisory",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("detailed output missing %q\nfull output:\n%s", want, d)
		}
	}

	// Concise volume: the count and the first diagnostic; no advisories, no second
	// diagnostic.
	c := conciseOut.String()
	if !strings.Contains(c, "2 diagnostic(s)") || !strings.Contains(c, "first: first pipeline diagnostic") {
		t.Errorf("concise output must carry the count and the first diagnostic\nfull output:\n%s", c)
	}
	for _, absent := range []string{"notice:", "second pipeline diagnostic"} {
		if strings.Contains(c, absent) {
			t.Errorf("concise output must not contain %q\nfull output:\n%s", absent, c)
		}
	}
}

// TestRenderReleaseValidation_AssertionMessageSurvivesFlattening pins the one
// property an assertion has: the author's message reaches the operator. A
// categorised envelope renders a single frame, so flattening a diagnostic with
// Error alone reports the phase that failed ("Component/Feature assertions") and
// drops the frame beneath it — the entity, the assertion name, and the
// author-written fix instruction, which is the only actionable part an assertion
// carries. This test fails a regression that reverts the flattening to Error.
//
// The chain shape mirrors what the assertion evaluator actually produces: a
// phase-context envelope wrapping the per-entity attribution wrapping the
// sentinel and the fail-call message.
func TestRenderReleaseValidation_AssertionMessageSurvivesFlattening(t *testing.T) {
	const authored = "set values.port to a value at or below 1023 in the Aggregate"

	failure := fmt.Errorf("assertion %q: %w: %s",
		"Component/core/server/only/port_must_be_privileged",
		renderer.ErrAssertionFailed(), authored)
	attributed := fmt.Errorf("Component %q instance %q assertion %q: %w",
		"core/server", "only", "port_must_be_privileged", failure)
	vr := &compilerops.ValidateResult{
		RunErr: cerrors.Wrap(attributed, cerrors.DeveloperError, cerrors.SourceLocation{},
			"Component/Feature assertions"),
	}

	for _, tc := range []struct {
		name      string
		verbosity string
	}{
		{"detailed", compilerops.VerbosityDetailed},
		{"concise", compilerops.VerbosityConcise},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := renderReleaseValidation(vr, nil, nil, "release.yaml", tc.verbosity, &out, &out)
			if err == nil {
				t.Fatal("expected a non-nil verdict for a failing assertion")
			}
			got := out.String()
			// The phase label alone is the collapsed form; every one of these is a
			// frame beneath it that Error would have dropped.
			for _, want := range []string{
				"Component/Feature assertions",
				`Component "core/server"`,
				`instance "only"`,
				`assertion "port_must_be_privileged"`,
				authored,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("rendered diagnostic must carry %q\nfull output:\n%s", want, got)
				}
			}
		})
	}
}

// TestRenderReleaseValidation_CleanVerdictInvariant proves a clean result yields
// a nil verdict under both verbosities, and that a clean-but-advisory result
// surfaces the advisory only under detailed.
func TestRenderReleaseValidation_CleanVerdictInvariant(t *testing.T) {
	vr := &compilerops.ValidateResult{
		NarrowingWarnings: []string{"narrowing-on-extend advisory"},
	}

	var detailedOut, conciseOut bytes.Buffer
	if err := renderReleaseValidation(vr, nil, nil, "release.yaml", compilerops.VerbosityDetailed, &detailedOut, &detailedOut); err != nil {
		t.Fatalf("detailed clean pass must return nil; got %v", err)
	}
	if err := renderReleaseValidation(vr, nil, nil, "release.yaml", compilerops.VerbosityConcise, &conciseOut, &conciseOut); err != nil {
		t.Fatalf("concise clean pass must return nil; got %v", err)
	}

	if !strings.Contains(detailedOut.String(), "notice: narrowing-on-extend advisory") {
		t.Errorf("detailed clean output must surface the advisory\nfull output:\n%s", detailedOut.String())
	}
	if strings.Contains(conciseOut.String(), "notice:") {
		t.Errorf("concise clean output must not surface advisories\nfull output:\n%s", conciseOut.String())
	}
}

// TestParseVerbosity confirms the shared parser folds case, trims, and defaults
// to the detailed form for absent or unrecognized input.
func TestParseVerbosity(t *testing.T) {
	cases := map[string]string{
		"":            compilerops.VerbosityDetailed,
		"detailed":    compilerops.VerbosityDetailed,
		"  CONCISE  ": compilerops.VerbosityConcise,
		"Concise":     compilerops.VerbosityConcise,
		"nonsense":    compilerops.VerbosityDetailed,
	}
	for in, want := range cases {
		if got := compilerops.ParseVerbosity(in); got != want {
			t.Errorf("ParseVerbosity(%q) = %q, want %q", in, got, want)
		}
	}
}
