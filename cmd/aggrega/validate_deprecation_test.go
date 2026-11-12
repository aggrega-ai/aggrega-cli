// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The release-scoped validation verb's side of the deprecation report. This verb
// produces a REPORT rather than an artifact, so it enumerates every recorded
// marker beside everything else it has to say and never truncates the report to
// announce one — the posture the halting verbs deliberately do not share.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// retractedPinFindings produces the shared report for a lockfile pinning one
// withdrawn reference. It goes through the producer rather than constructing a
// finding, which is the only way to make one: a surface cannot report a moment its
// lockfile never recorded.
// retractedPinFindings renders the notices for a lockfile pinning one retracted
// reference. It returns the rendered sentences rather than the findings, because
// each store that can hold a marker renders its own sentence with its own
// provenance and the renderer takes the rendered form.
func retractedPinFindings(t *testing.T, stamp string) []string {
	t.Helper()
	lf := &lockfile.Lockfile{
		GeneratedAt: stamp,
		Entities: []lockfile.Entity{{
			URI: "oci://reg.example/components/api",
			Deprecation: &lockfile.DeprecationRecord{
				Severity: "retracted",
				Message:  "withdrawn: bad release",
			},
		}},
	}
	findings := lf.DeprecationFindings()
	if len(findings) == 0 {
		t.Fatal("the fixture produced no findings; every assertion below would pass vacuously")
	}
	notices := make([]string, 0, len(findings))
	for _, f := range findings {
		notices = append(notices, f.Notice())
	}
	return notices
}

// TestValidateReportsARetractedPinAndStaysGreen pins the advisory arm. The verb
// reports the withdrawal — which is the silence this closes — and does not
// manufacture a verdict from it: the halt on a recorded marker is configured on the
// build dial and governs the build, so a workspace whose only finding is a recorded
// marker validates exactly as the same workspace builds.
func TestValidateReportsARetractedPinAndStaysGreen(t *testing.T) {
	const stamp = "2026-08-01T09:30:00Z"
	var out bytes.Buffer

	err := renderReleaseValidation(&compilerops.ValidateResult{}, nil, retractedPinFindings(t, stamp),
		"release.yaml", compilerops.VerbosityDetailed, &out, &out)
	if err != nil {
		t.Fatalf("a recorded marker must not manufacture a validation verdict: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"deprecation: oci://reg.example/components/api is retracted",
		"withdrawn: bad release",
		"as of " + stamp,
		"aggrega pull",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report drops %q — the silence this closes is back:\n%s", want, got)
		}
	}
}

// TestValidateReportsARetractedPinBesideAGateThatRefusesFirst is the
// do-not-truncate assertion, and it is asserted against the EARLIEST-returning
// gate. The extends gate returns before every other class is even computed, so a
// report emitted after it would be lost exactly when the author most needs both
// facts. It also holds the other half: the pre-existing verdict is unchanged — the
// marker neither creates nor suppresses one.
func TestValidateReportsARetractedPinBesideAGateThatRefusesFirst(t *testing.T) {
	const stamp = "2026-08-01T09:30:00Z"
	var out bytes.Buffer

	vr := &compilerops.ValidateResult{
		ClosureExtendsErrors: []error{errors.New("dangling parent component-base")},
	}
	err := renderReleaseValidation(vr, nil, retractedPinFindings(t, stamp),
		"release.yaml", compilerops.VerbosityDetailed, &out, &out)
	if err == nil {
		t.Fatal("a real fault must still yield a non-zero verdict")
	}
	if !strings.Contains(err.Error(), "extends resolution error") {
		t.Errorf("the verdict must name the real fault rather than the advisory: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "deprecation: oci://reg.example/components/api is retracted") {
		t.Errorf("a gate refusing first must not cost the deprecation report:\n%s", got)
	}
	if !strings.Contains(got, "extends: dangling parent component-base") {
		t.Errorf("the deprecation report must not cost the gate's own diagnostic:\n%s", got)
	}
}

// TestValidateConciseFormOmitsTheDeprecationReport holds the verbosity contract:
// volume only, never the verdict. The concise form collapses the
// verdict-affecting classes into a count, and a recorded marker affects no verdict,
// so it belongs with the other advisory lines the concise form drops — and dropping
// it must not change what the verb returns.
func TestValidateConciseFormOmitsTheDeprecationReport(t *testing.T) {
	findings := retractedPinFindings(t, "2026-08-01T09:30:00Z")

	var detailedOut, conciseOut bytes.Buffer
	detailedErr := renderReleaseValidation(&compilerops.ValidateResult{}, nil, findings,
		"release.yaml", compilerops.VerbosityDetailed, &detailedOut, &detailedOut)
	conciseErr := renderReleaseValidation(&compilerops.ValidateResult{}, nil, findings,
		"release.yaml", compilerops.VerbosityConcise, &conciseOut, &conciseOut)

	if detailedErr != nil || conciseErr != nil {
		t.Fatalf("both forms must reach the same verdict: detailed=%v concise=%v", detailedErr, conciseErr)
	}
	if !strings.Contains(detailedOut.String(), "deprecation:") {
		t.Fatalf("the detailed form must carry the report:\n%s", detailedOut.String())
	}
	if strings.Contains(conciseOut.String(), "deprecation:") {
		t.Errorf("the concise form emits no advisory lines:\n%s", conciseOut.String())
	}
}
