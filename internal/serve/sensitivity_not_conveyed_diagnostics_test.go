// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestPublishRenderDiagnostics_CarriesSensitivityNotConveyedAdvisory proves the
// advisory raised for a sensitivity declaration a node's render cannot convey
// reaches the editor's problem list, not the terminal alone. It drives the
// bridge with the diagnostic the render's own constructor builds — located as
// the render locates it — so the assertion covers the whole crossing rather
// than the formatter in isolation.
//
// The editor is where this class matters most: it is the surface an author has
// open while writing the declaration the advisory is about, which is the moment
// the finding can still change what they write.
func TestPublishRenderDiagnostics_CarriesSensitivityNotConveyedAdvisory(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/release.yaml"

	d := rendercore.SensitivityNotConveyedDiagnostic("sens-mapped", "values.dbPassword")
	d.Location = &renderer.DiagnosticLocation{Instance: "mapped", Kind: "helm"}
	s.publishRenderDiagnostics(ctx, file, []renderer.Diagnostic{d}, nil, nil, nil)
	flush()

	got := s.snapshotDiagnostics(file)
	if len(got) != 1 {
		t.Fatalf("published %d diagnostics; want 1", len(got))
	}
	if got[0].Severity != "warning" {
		t.Errorf("published severity = %q; want the editor wire's %q", got[0].Severity, "warning")
	}
	// The editor entry carries the same line a terminal build prints, so an
	// author reading either surface is told the coordinate they declared, the
	// Recipe holding the lever, the node and the target.
	for _, want := range []string{"values.dbPassword", "sens-mapped", "valuesMapping", "mapped", "helm"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("published editor message %q does not name %s", got[0].Message, want)
		}
	}
}
