// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

func unsupportedFieldDiag() renderer.Diagnostic {
	return renderer.Diagnostic{
		Severity: abi.SeverityWarn,
		Code:     renderer.UnsupportedFieldCode,
		Message:  `target "compose" does not support scheduling and omitted it`,
		Location: &renderer.DiagnosticLocation{Instance: "api", Kind: "compose"},
	}
}

func sensitiveByValueDiag() renderer.Diagnostic {
	d := renderer.SensitiveByValueDiagnostic("user_password", "env", "MARIADB_PASSWORD")
	d.Location = &renderer.DiagnosticLocation{Instance: "db", Kind: "helm"}
	return d
}

// TestEmitRenderWarnings_SuppressUnsupportedFieldClass proves the unsupported-field class is
// printed by default with node/provider attribution and suppressed alone when the
// flag is set, while the uncoded warning stream always surfaces.
func TestEmitRenderWarnings_SuppressUnsupportedFieldClass(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Warnings: []renderer.Diagnostic{{
			Severity: abi.SeverityWarn,
			Message:  "some other advisory",
			Location: &renderer.DiagnosticLocation{Instance: "api", Kind: "compose"},
		}},
		Diagnostics: []renderer.Diagnostic{unsupportedFieldDiag()},
	}

	var shown bytes.Buffer
	emitRenderWarnings(&shown, result, false)
	out := shown.String()
	if !strings.Contains(out, "some other advisory") {
		t.Errorf("uncoded warning missing when unsuppressed:\n%s", out)
	}
	if !strings.Contains(out, "does not support scheduling") {
		t.Errorf("unsupported-field warning missing when unsuppressed:\n%s", out)
	}
	if !strings.Contains(out, `node "api" (provider "compose"): target "compose" does not support scheduling`) {
		t.Errorf("unsupported-field warning not node/provider-attributed:\n%s", out)
	}

	var suppressed bytes.Buffer
	emitRenderWarnings(&suppressed, result, true)
	sout := suppressed.String()
	if !strings.Contains(sout, "some other advisory") {
		t.Errorf("uncoded warning must still surface when the class is suppressed:\n%s", sout)
	}
	if strings.Contains(sout, "does not support scheduling") {
		t.Errorf("unsupported-field warning must be suppressed by the flag:\n%s", sout)
	}
}

// TestEmitRenderWarnings_SensitiveByValueSurvivesUnsupportedFieldSuppression is the
// guard on the deliberate separation of the two coded classes. They ride one
// out-of-band stream, so nothing structural stops a suppression written against
// the stream rather than against a code from silencing both — and silencing a
// security-adjacent advisory as a side effect of a target-fidelity preference is
// exactly the outcome the separate code exists to prevent. The test fails the
// moment suppression stops keying on the code.
func TestEmitRenderWarnings_SensitiveByValueSurvivesUnsupportedFieldSuppression(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Diagnostics: []renderer.Diagnostic{unsupportedFieldDiag(), sensitiveByValueDiag()},
	}

	var suppressed bytes.Buffer
	emitRenderWarnings(&suppressed, result, true)
	out := suppressed.String()
	if strings.Contains(out, "does not support scheduling") {
		t.Errorf("unsupported-field warning must be suppressed by the flag:\n%s", out)
	}
	if !strings.Contains(out, "MARIADB_PASSWORD") {
		t.Errorf("sensitive-by-value advisory must survive unsupported-field suppression:\n%s", out)
	}
	if !strings.Contains(out, `node "db" (provider "helm")`) {
		t.Errorf("sensitive-by-value advisory not node/provider-attributed:\n%s", out)
	}
	if renderer.SensitiveByValueCode == renderer.UnsupportedFieldCode {
		t.Fatalf("the two classes must not share a code; both are %q", renderer.SensitiveByValueCode)
	}
}

// TestEmitRenderWarnings_DisplayBytesAreUnchanged pins the whole display pass
// byte for byte: the two-space-and-`warning:` framing, the uncoded stream first
// and the coded stream second, and one line per entry.
//
// Both streams are structured now, so nothing in the shape of the data keeps the
// groups apart — only the order this function reads them in does. That makes the
// order an assertion rather than an incidental property, and the framing is
// asserted with it because the same statement produces both.
func TestEmitRenderWarnings_DisplayBytesAreUnchanged(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Warnings: []renderer.Diagnostic{
			{Severity: abi.SeverityWarn, Message: "bare advisory, no position"},
			{
				Severity: abi.SeverityWarn,
				Message:  `.infra key "cpu" is read by no template`,
				Location: &renderer.DiagnosticLocation{Instance: "api"},
			},
		},
		Diagnostics: []renderer.Diagnostic{unsupportedFieldDiag()},
	}

	const want = "  warning: bare advisory, no position\n" +
		"  warning: node \"api\": .infra key \"cpu\" is read by no template\n" +
		"  warning: node \"api\" (provider \"compose\"): target \"compose\" does not support scheduling and omitted it\n"

	var shown bytes.Buffer
	emitRenderWarnings(&shown, result, false)
	if got := shown.String(); got != want {
		t.Errorf("display bytes moved:\n got: %q\nwant: %q", got, want)
	}
}

// TestEmitRenderWarnings_DisplayByteComparisonCanFail is the negative control on
// the assertion above. An equality check whose failure mode nobody demonstrated
// cannot be told apart from one that compares nothing, so this plants a single
// byte — one space removed from the fixed indent — and requires the comparison
// to reject it.
func TestEmitRenderWarnings_DisplayByteComparisonCanFail(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Warnings: []renderer.Diagnostic{{Severity: abi.SeverityWarn, Message: "an advisory"}},
	}

	const planted = " warning: an advisory\n" // one space, not two

	var shown bytes.Buffer
	emitRenderWarnings(&shown, result, false)
	if shown.String() == planted {
		t.Fatalf("the display emitted the planted single-space indent, so the plant is not a plant: %q", shown.String())
	}
	if shown.String() != "  warning: an advisory\n" {
		t.Errorf("unexpected display bytes: %q", shown.String())
	}
}

// TestMergeRenderDiagnostics_CarriesBothStreamsInDisplayOrder is the carrier
// half of the same property the display tests pin, and it is the half that makes
// an advisory reachable at all by a caller with no terminal.
//
// Before the streams became one list the uncoded ones reached the display and
// then ceased to exist: the returned result had no field of any kind for them, so
// an editor could not have read them however it tried. This asserts they are
// there, that the coded ones still are, and that the sequence is the sequence a
// build printed — which is what lets anyone compare the two.
func TestMergeRenderDiagnostics_CarriesBothStreamsInDisplayOrder(t *testing.T) {
	t.Parallel()
	uncoded := renderer.Diagnostic{
		Severity: abi.SeverityWarn,
		Message:  `.infra key "cpu" is read by no template`,
		Location: &renderer.DiagnosticLocation{Instance: "api"},
	}
	result := &renderer.RenderResult{
		Warnings:    []renderer.Diagnostic{uncoded},
		Diagnostics: []renderer.Diagnostic{unsupportedFieldDiag(), sensitiveByValueDiag()},
	}

	merged := mergeRenderDiagnostics(result)
	if len(merged) != 3 {
		t.Fatalf("merged carries %d entries, want 3: %+v", len(merged), merged)
	}
	// The uncoded entry leads, because that is the group the display prints first.
	if merged[0].Message != uncoded.Message {
		t.Errorf("the uncoded stream does not lead: %+v", merged[0])
	}
	// It arrives carrying its position, which is the whole reason a consumer that
	// renders nothing can act on it.
	if merged[0].Location == nil || merged[0].Location.Instance != "api" {
		t.Errorf("the uncoded entry lost its position in the merge: %+v", merged[0].Location)
	}
	// It carries no code, because its raising site had none — an absent code is a
	// fact about that site rather than something this boundary supplies.
	if merged[0].Code != "" {
		t.Errorf("the merge minted a code onto an uncoded entry: %q", merged[0].Code)
	}
	// And the coded classes follow, each still matchable by its own code.
	if merged[1].Code != renderer.UnsupportedFieldCode || merged[2].Code != renderer.SensitiveByValueCode {
		t.Errorf("the coded classes did not follow in order: %q then %q", merged[1].Code, merged[2].Code)
	}
}

// TestMergeRenderDiagnostics_OrderMatchesTheDisplay is the assertion that keeps
// the carrier's order and the display's order from drifting apart.
//
// Each is stated in its own place — one reads two fields in sequence, the other
// concatenates them — so nothing but this compares them. It renders the merged
// list through the same formatter the display uses and requires the result to be
// exactly what the display wrote, line for line.
func TestMergeRenderDiagnostics_OrderMatchesTheDisplay(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Warnings: []renderer.Diagnostic{
			{Severity: abi.SeverityWarn, Message: "first uncoded"},
			{Severity: abi.SeverityWarn, Message: "second uncoded",
				Location: &renderer.DiagnosticLocation{Instance: "api"}},
		},
		Diagnostics: []renderer.Diagnostic{unsupportedFieldDiag(), sensitiveByValueDiag()},
	}

	var shown bytes.Buffer
	emitRenderWarnings(&shown, result, false)

	var fromCarrier bytes.Buffer
	for _, d := range mergeRenderDiagnostics(result) {
		fromCarrier.WriteString("  warning: " + renderer.RenderDiagnosticText(d) + "\n")
	}

	if shown.String() != fromCarrier.String() {
		t.Errorf("the carrier and the display disagree:\n display: %q\n carrier: %q",
			shown.String(), fromCarrier.String())
	}
	// The control: with the streams concatenated the other way round the two must
	// disagree, or the comparison above is comparing nothing.
	swapped := &renderer.RenderResult{Warnings: result.Diagnostics, Diagnostics: result.Warnings}
	var fromSwapped bytes.Buffer
	for _, d := range mergeRenderDiagnostics(swapped) {
		fromSwapped.WriteString("  warning: " + renderer.RenderDiagnosticText(d) + "\n")
	}
	if fromSwapped.String() == shown.String() {
		t.Error("a reversed merge produced the display's bytes, so the order assertion cannot fail")
	}
}

// TestRenderDiagnosticText_LocationForms pins the one formatter's shapes so the
// terminal and the editor cannot render the same advisory two ways. A location a
// plugin set itself, naming a document and no instance, keeps the line it printed
// before an instance could be carried, byte for byte; one naming an instance prints
// the instance even beside a document.
func TestRenderDiagnosticText_LocationForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   renderer.Diagnostic
		want string
	}{
		{"no location", renderer.Diagnostic{Message: "bare"}, "bare"},
		{"node only", renderer.Diagnostic{Message: "m", Location: &renderer.DiagnosticLocation{Instance: "db"}}, `node "db": m`},
		{"node and provider", renderer.Diagnostic{Message: "m", Location: &renderer.DiagnosticLocation{Instance: "db", Kind: "helm"}}, `node "db" (provider "helm"): m`},
		{"a plugin-set location with no instance", renderer.Diagnostic{Message: "Unsupported argument",
			Location: &renderer.DiagnosticLocation{URI: "main.tf", Line: 3, Kind: "terraform"}},
			`node "main.tf" (provider "terraform"): Unsupported argument`},
		{"a plugin-set document and no provider", renderer.Diagnostic{Message: "m",
			Location: &renderer.DiagnosticLocation{URI: "main.tf"}}, `node "main.tf": m`},
		{"an instance beside a document", renderer.Diagnostic{Message: "m",
			Location: &renderer.DiagnosticLocation{URI: "main.tf", Kind: "terraform", Instance: "db"}},
			`node "db" (provider "terraform"): m`},
	}
	for _, c := range cases {
		if got := renderer.RenderDiagnosticText(c.in); got != c.want {
			t.Errorf("%s: RenderDiagnosticText = %q, want %q", c.name, got, c.want)
		}
	}
}
