// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

func unsupportedFieldDiag() renderer.Diagnostic {
	return renderer.Diagnostic{
		Severity: "warn",
		Code:     renderer.UnsupportedFieldCode,
		Message:  `target "compose" does not support scheduling and omitted it`,
		Location: &renderer.DiagnosticLocation{URI: "api", Kind: "compose"},
	}
}

func sensitiveByValueDiag() renderer.Diagnostic {
	d := renderer.SensitiveByValueDiagnostic("user_password", "env", "MARIADB_PASSWORD")
	d.Location = &renderer.DiagnosticLocation{URI: "db", Kind: "helm"}
	return d
}

// TestEmitRenderWarnings_SuppressUnsupportedFieldClass proves the unsupported-field class is
// printed by default with node/provider attribution and suppressed alone when the
// flag is set, while the free-form warning stream always surfaces.
func TestEmitRenderWarnings_SuppressUnsupportedFieldClass(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Warnings:    []string{`node "api" (provider "compose"): some other advisory`},
		Diagnostics: []renderer.Diagnostic{unsupportedFieldDiag()},
	}

	var shown bytes.Buffer
	emitRenderWarnings(&shown, result, false)
	out := shown.String()
	if !strings.Contains(out, "some other advisory") {
		t.Errorf("free-form warning missing when unsuppressed:\n%s", out)
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
		t.Errorf("free-form warning must still surface when the class is suppressed:\n%s", sout)
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

// TestRenderDiagnosticText_LocationForms pins the one formatter's three shapes so
// the terminal and the editor cannot render the same advisory two ways.
func TestRenderDiagnosticText_LocationForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   renderer.Diagnostic
		want string
	}{
		{"no location", renderer.Diagnostic{Message: "bare"}, "bare"},
		{"node only", renderer.Diagnostic{Message: "m", Location: &renderer.DiagnosticLocation{URI: "db"}}, `node "db": m`},
		{"node and provider", renderer.Diagnostic{Message: "m", Location: &renderer.DiagnosticLocation{URI: "db", Kind: "helm"}}, `node "db" (provider "helm"): m`},
	}
	for _, c := range cases {
		if got := renderer.RenderDiagnosticText(c.in); got != c.want {
			t.Errorf("%s: RenderDiagnosticText = %q, want %q", c.name, got, c.want)
		}
	}
}
