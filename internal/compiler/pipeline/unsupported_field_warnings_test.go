// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestEmitRenderWarnings_SuppressUnsupportedFieldClass proves the unsupported-field class is
// printed by default with node/provider attribution and suppressed alone when the
// flag is set, while the free-form warning stream always surfaces.
func TestEmitRenderWarnings_SuppressUnsupportedFieldClass(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Warnings: []string{`node "api" (provider "compose"): some other advisory`},
		UnsupportedFieldWarnings: []renderer.Diagnostic{
			{
				Severity: "warning",
				Code:     renderer.UnsupportedFieldCode,
				Message:  `target "compose" does not support scheduling and omitted it`,
				Location: &renderer.DiagnosticLocation{URI: "api", Kind: "compose"},
			},
		},
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
