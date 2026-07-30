// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
)

// TestPolicyVerdictErrors_FiredVerdictsBecomeErrors proves the results→errors
// conversion: each fired verdict becomes one AnalysisError carrying the policy
// kind, the ErrPolicyViolation sentinel, and a message naming the plugin plus
// the plugin-supplied text — and that message renders verbatim through
// Error()/ToCompilerError() rather than through the edge-shaped composition.
func TestPolicyVerdictErrors_FiredVerdictsBecomeErrors(t *testing.T) {
	out := abi.PolicyOutput{Results: []abi.Diagnostic{
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
	out := abi.PolicyOutput{Results: nil}
	if errs := policyVerdictErrors("proof://policy", out); len(errs) != 0 {
		t.Fatalf("empty output produced %d errors, want 0", len(errs))
	}
}

// policyVerdictWireFixture is the guest-shaped JSON a policy plugin returns for
// a single fired verdict that populates every field the shared diagnostic
// carries. It is a raw wire document rather than a Go literal deliberately: a
// struct literal proves only that the type has the fields, while this crosses
// the same decode boundary a real guest's bytes cross, which is where the values
// were being discarded.
const policyVerdictWireFixture = `{"results":[{` +
	`"severity":"deny",` +
	`"message":"image must be digest-pinned",` +
	`"code":"PROD-014",` +
	`"rule_id":"no-floating-tags",` +
	`"classification":"MAJOR",` +
	`"source_ref":"payments-api",` +
	`"location":{"key_path":"spec.template.spec.containers[0].image","uri":"payments-api/deployment.yaml","line":42,"kind":"rendered-artifact"},` +
	`"suggest":"replace the tag with a sha256 digest"` +
	`}]}`

// TestPolicyVerdictWire_EveryFieldReachesTheHost proves each field a guest may
// populate on a fired verdict survives the decode the socket runners perform.
// The assertion walks the fields beyond severity and message one at a time and
// names the field it lost, so a regression reports which value stopped arriving
// rather than an opaque struct inequality.
func TestPolicyVerdictWire_EveryFieldReachesTheHost(t *testing.T) {
	var out abi.PolicyOutput
	if err := json.Unmarshal([]byte(policyVerdictWireFixture), &out); err != nil {
		t.Fatalf("decode guest verdict: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("decoded %d verdicts, want 1", len(out.Results))
	}
	got := out.Results[0]

	for _, c := range []struct{ field, got, want string }{
		{"severity", got.Severity, analyzer.SeverityDeny},
		{"message", got.Message, "image must be digest-pinned"},
		{"code", got.Code, "PROD-014"},
		{"rule_id", got.RuleID, "no-floating-tags"},
		{"classification", got.Classification, "MAJOR"},
		{"source_ref", got.SourceRef, "payments-api"},
		{"suggest", got.Suggest, "replace the tag with a sha256 digest"},
	} {
		if c.got != c.want {
			t.Errorf("verdict %s did not reach the host: got %q, want %q", c.field, c.got, c.want)
		}
	}
	if got.Location == nil {
		t.Fatalf("verdict location did not reach the host: got nil")
	}
	for _, c := range []struct{ field, got, want string }{
		{"location.key_path", got.Location.KeyPath, "spec.template.spec.containers[0].image"},
		{"location.uri", got.Location.URI, "payments-api/deployment.yaml"},
		{"location.kind", got.Location.Kind, "rendered-artifact"},
	} {
		if c.got != c.want {
			t.Errorf("verdict %s did not reach the host: got %q, want %q", c.field, c.got, c.want)
		}
	}
	if got.Location.Line != 42 {
		t.Errorf("verdict location.line did not reach the host: got %d, want 42", got.Location.Line)
	}
}

// TestPolicyVerdictWire_EveryFieldIsRendered proves the fields a verdict carries
// beyond severity and message are read rather than merely decoded. A value that
// arrives at the host and is then never surfaced is the same loss one stage
// later, so the deny halt, the warn diagnostic, and the audit record are each
// asserted to carry every one of them.
func TestPolicyVerdictWire_EveryFieldIsRendered(t *testing.T) {
	var out abi.PolicyOutput
	if err := json.Unmarshal([]byte(policyVerdictWireFixture), &out); err != nil {
		t.Fatalf("decode guest verdict: %v", err)
	}
	verdict := out.Results[0]

	errs := policyVerdictErrors("oci://registry.example/policy/house-rules", out)
	if len(errs) != 1 {
		t.Fatalf("expected one error for the fired deny verdict, got %d", len(errs))
	}
	surfaces := map[string]string{
		"deny halt":     errs[0].Error(),
		"warn":          policyWarnMessage("oci://registry.example/policy/house-rules", verdict),
		"audit":         policyVerdictDetail(verdict),
		"detail suffix": policyVerdictDetail(verdict),
	}
	want := []string{
		"PROD-014",
		"no-floating-tags",
		"MAJOR",
		"payments-api",
		"spec.template.spec.containers[0].image",
		"payments-api/deployment.yaml",
		"line 42",
		"rendered-artifact",
		"replace the tag with a sha256 digest",
	}
	for surface, rendered := range surfaces {
		for _, w := range want {
			if !strings.Contains(rendered, w) {
				t.Errorf("%s surface drops %q: %s", surface, w, rendered)
			}
		}
	}

	// A verdict carrying only severity and message renders with no detail suffix,
	// so the existing verdict text is unchanged by the addition.
	lean := abi.Diagnostic{Severity: analyzer.SeverityDeny, Message: "plain"}
	if detail := policyVerdictDetail(lean); detail != "" {
		t.Errorf("a verdict with no optional fields rendered a detail suffix: %q", detail)
	}
}

// TestPolicyVerdictLocation_SpanWinsOverStructured proves a verdict that pins a
// byte span renders the span, which is the exact position, rather than the
// structured document coordinates.
func TestPolicyVerdictLocation_SpanWinsOverStructured(t *testing.T) {
	d := abi.Diagnostic{
		Severity: analyzer.SeverityWarn,
		Message:  "unresolved reference",
		Location: &abi.Location{
			KeyPath: "spec.values.host",
			Span:    &abi.Span{StartLine: 7, StartColumn: 3, EndLine: 7, EndColumn: 19},
		},
	}
	detail := policyVerdictDetail(d)
	if !strings.Contains(detail, "location=7:3-7:19") {
		t.Errorf("span location not rendered: %q", detail)
	}
	if strings.Contains(detail, "spec.values.host") {
		t.Errorf("structured location rendered alongside the span: %q", detail)
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
