// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
)

// TestPolicyVerdictErrors_FiredVerdictsBecomeErrors proves the results→errors
// conversion: each fired verdict becomes one AnalysisError carrying the policy
// kind, the ErrPolicyViolation sentinel, and a message naming the plugin plus
// the plugin-supplied text — and that message renders verbatim through
// Error()/ToCompilerError() rather than through the edge-shaped composition.
func TestPolicyVerdictErrors_FiredVerdictsBecomeErrors(t *testing.T) {
	out := analyzer.PolicyValidatorOutput{Results: []analyzer.PolicyResult{
		{Severity: analyzer.SeverityDeny, Message: "rule B: prod boundary requires an owner label"},
		{Severity: analyzer.SeverityDeny, Message: "rule C: image must be digest-pinned"},
	}}

	errs := policyVerdictErrors("oci://registry.example/policy/house-rules", out)
	if len(errs) != 2 {
		t.Fatalf("expected one error per fired verdict, got %d", len(errs))
	}
	for _, e := range errs {
		if e.Kind != analyzer.AnalysisErrorPolicy {
			t.Errorf("verdict error kind = %q, want %q", e.Kind, analyzer.AnalysisErrorPolicy)
		}
		if !errors.Is(e.Sentinel, analyzer.ErrPolicyViolation) {
			t.Errorf("verdict error sentinel = %v, want ErrPolicyViolation", e.Sentinel)
		}
		// The rendered message names the plugin leaf and carries the plugin text
		// verbatim (no "-> " or "attaches Feature" edge composition).
		if strings.Contains(e.Error(), "attaches Feature") || strings.Contains(e.Error(), " -> ") {
			t.Errorf("policy message rendered through the edge composition: %q", e.Error())
		}
		if !strings.Contains(e.Error(), "house-rules") {
			t.Errorf("policy message does not name the plugin: %q", e.Error())
		}
	}
	if !strings.Contains(errs[0].Error(), "rule B: prod boundary requires an owner label") {
		t.Errorf("first error missing plugin message: %q", errs[0].Error())
	}
}

// TestPolicyVerdictErrors_EmptyResultsIsEmpty proves an output with no fired
// verdicts produces no errors, so the build proceeds.
func TestPolicyVerdictErrors_EmptyResultsIsEmpty(t *testing.T) {
	out := analyzer.PolicyValidatorOutput{Results: nil}
	if errs := policyVerdictErrors("proof://policy", out); len(errs) != 0 {
		t.Fatalf("empty output produced %d errors, want 0", len(errs))
	}
}

// TestPluginRefName proves the diagnostic-label derivation takes the final URI
// segment across the reference shapes a plugin attachment may carry.
func TestPluginRefName(t *testing.T) {
	cases := map[string]string{
		"oci://registry.example/plugin/policy/house-rules": "house-rules",
		"proof://mutator":              "mutator",
		"./plugins/local-mutator.wasm": "local-mutator.wasm",
		"bare-name":                    "bare-name",
	}
	for in, want := range cases {
		if got := pluginRefName(in); got != want {
			t.Errorf("pluginRefName(%q) = %q, want %q", in, got, want)
		}
	}
}
