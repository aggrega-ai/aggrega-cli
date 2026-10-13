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
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestPolicyVerdictErrors_FiredVerdictsBecomeErrors proves the results→errors
// conversion: each fired verdict becomes one AnalysisError carrying the policy
// kind, the ErrPolicyViolation sentinel, and a message naming the plugin plus
// the plugin-supplied text — and that message renders verbatim through
// Error()/ToCompilerError() rather than through the edge-shaped composition.
func TestPolicyVerdictErrors_FiredVerdictsBecomeErrors(t *testing.T) {
	out := abi.PolicyOutput{Results: []abi.Diagnostic{
		{Severity: analyzer.SeverityHalt, Message: "rule B: prod boundary requires an owner label"},
		{Severity: analyzer.SeverityHalt, Message: "rule C: image must be digest-pinned"},
	}}

	errs := policyVerdictErrors("oci://registry.example/policy/house-rules", out)
	if len(errs) != 2 {
		t.Fatalf("expected one error per fired verdict, got %d", len(errs))
	}
	for _, e := range errs {
		if e.Kind != analyzer.AnalysisErrorPolicy {
			t.Errorf("verdict error kind = %q, want %q", e.Kind, analyzer.AnalysisErrorPolicy)
		}
		if !errors.Is(e.Sentinel, analyzer.ErrPolicyViolation()) {
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

// policyVerdictWireFixture is the guest-shaped document a policy plugin returns
// for a single fired verdict that populates every field the shared diagnostic
// carries. It is a document rather than a Go literal deliberately: a struct
// literal proves only that the type has the fields, while this is read the way a
// guest's own output is read — encoded as the guest writes it and decoded through
// the entry point the socket runners use — which is where the values were being
// discarded. See decodeVerdictWire.
const policyVerdictWireFixture = `{"results":[{` +
	// Spelled literally: this fixture is what the guest writes, so it must name
	// the encoded value rather than borrow the const the host decodes with.
	`"severity":"halt",` +
	`"message":"image must be digest-pinned",` +
	`"code":"PROD-014",` +
	`"rule_id":"no-floating-tags",` +
	`"classification":"MAJOR",` +
	`"source_ref":"payments-api",` +
	`"location":{"key_path":"spec.template.spec.containers[0].image","uri":"payments-api/deployment.yaml","line":42,"kind":"rendered-artifact"},` +
	`"suggest":"replace the tag with a sha256 digest"` +
	`}]}`

// decodeVerdictWire turns a guest-shaped verdict document into the bytes a guest
// writes on the verdict roles' wire, and reads them back through the entry point
// both socket runners decode with. The fixtures are written as documents because
// that is the readable form of the shape; what reaches the host is that shape in
// the canonical binary encoding, so the decode under test is the binary one.
func decodeVerdictWire(t *testing.T, document string) abi.PolicyOutput {
	t.Helper()
	var written abi.PolicyOutput
	if err := json.Unmarshal([]byte(document), &written); err != nil {
		t.Fatalf("read the fixture document: %v", err)
	}
	wire, err := written.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the fixture as a guest writes it: %v", err)
	}
	var out abi.PolicyOutput
	if err := wasm.DecodeGuestOutputCBOR(wasm.VerdictIRExport.Name(), wire, &out); err != nil {
		t.Fatalf("decode guest verdict: %v", err)
	}
	return out
}

// TestPolicyVerdictWire_EveryFieldReachesTheHost proves each field a guest may
// populate on a fired verdict survives the decode the socket runners perform.
// The assertion walks the fields beyond severity and message one at a time and
// names the field it lost, so a regression reports which value stopped arriving
// rather than an opaque struct inequality.
func TestPolicyVerdictWire_EveryFieldReachesTheHost(t *testing.T) {
	out := decodeVerdictWire(t, policyVerdictWireFixture)
	if len(out.Results) != 1 {
		t.Fatalf("decoded %d verdicts, want 1", len(out.Results))
	}
	got := out.Results[0]

	for _, c := range []struct{ field, got, want string }{
		{"severity", got.Severity, analyzer.SeverityHalt},
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
// later, so the halt, the warn diagnostic, and the info record are each
// asserted to carry every one of them.
func TestPolicyVerdictWire_EveryFieldIsRendered(t *testing.T) {
	out := decodeVerdictWire(t, policyVerdictWireFixture)
	verdict := out.Results[0]

	errs := policyVerdictErrors("oci://registry.example/policy/house-rules", out)
	if len(errs) != 1 {
		t.Fatalf("expected one error for the fired halt verdict, got %d", len(errs))
	}
	surfaces := map[string]string{
		"halt":          errs[0].Error(),
		"warn":          policyWarnMessage("oci://registry.example/policy/house-rules", verdict),
		"info":          policyVerdictDetail(verdict),
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
	lean := abi.Diagnostic{Severity: analyzer.SeverityHalt, Message: "plain"}
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

// undeclaredSeverityVerdictWireFixture is the guest-shaped document a policy
// plugin would return for a fired verdict carrying a severity the shared
// diagnostic contract does not declare. It is a document rather than a Go
// literal for the same reason the fixture above is: the host's own vocabulary
// cannot produce this value, so only the decode boundary can introduce it.
//
// The severity is spelled literally because the point of the fixture is that the
// value is NOT one of the declared consts.
const undeclaredSeverityVerdictWireFixture = `{"results":[{` +
	`"severity":"critical",` +
	`"message":"image must be digest-pinned"` +
	`}]}`

// TestPolicyVerdict_UndeclaredSeverityIsRefusedRatherThanAllowed is the arbiter
// for the governance surface. It records both halves of the condition this guard
// exists for.
//
// First the exposure: a verdict whose severity is undeclared is invisible to
// every routing path the runners have. The halting path skips it, because it
// selects the halting severity and this is not it; and neither advisory arm
// matches it either. On a governance surface those three misses compose into an
// allow — a verdict the plugin raised, reported nowhere, on a build that
// completes.
//
// Then the closure: the check both runners now perform as they decode a guest's
// output refuses this value outright, naming the plugin and the value, so the
// build halts loudly instead of proceeding as though nothing fired.
func TestPolicyVerdict_UndeclaredSeverityIsRefusedRatherThanAllowed(t *testing.T) {
	out := decodeVerdictWire(t, undeclaredSeverityVerdictWireFixture)
	if len(out.Results) != 1 {
		t.Fatalf("decoded %d verdicts, want 1", len(out.Results))
	}
	got := out.Results[0]

	// The exposure, asserted rather than described.
	if errs := policyVerdictErrors("oci://registry.example/policy/house-rules", out); len(errs) != 0 {
		t.Errorf("the halting path claimed this verdict; the exposure this guard covers has changed shape: %v", errs)
	}
	if got.Severity == analyzer.SeverityWarn || got.Severity == analyzer.SeverityInfo {
		t.Fatalf("fixture severity %q is a declared advisory; the fixture no longer carries an undeclared value", got.Severity)
	}

	// The closure.
	err := wasm.GuestDiagnosticSeverities("oci://registry.example/policy/house-rules", "evaluate", out.Results)
	if err == nil {
		t.Fatal("an undeclared verdict severity was accepted; a governance surface must not model the unroutable as an allow")
	}
	if !errors.Is(err, wasm.ErrWasmDiagnosticSeverity()) {
		t.Errorf("err = %v, want it to wrap the undeclared-severity sentinel", err)
	}
	for _, want := range []string{"oci://registry.example/policy/house-rules", `"critical"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q:\n  %v", want, err)
		}
	}
}
