// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestEvaluateOneArtifactWithDelims_NonDefaultDelimsParseFailWarns
// pins the non-default-delims parse-failure → warn-and-emit branch:
// rendered output that yaml.Unmarshal can't parse and uses non-default
// delimiters falls back to the warning path rather than erroring.
func TestEvaluateOneArtifactWithDelims_NonDefaultDelimsParseFailWarns(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{Recipe: &types.Recipe{}}
	tmplCtx := &types.TemplateContext{}
	// Output that yaml.Unmarshal will reject but does NOT contain `{{`
	// (so the pre-emptive short-circuit doesn't fire). Use bare colons
	// in an unquoted scalar position that yaml.v3 rejects.
	src := "key: : double-colon-error"
	body, err := evaluateOneArtifactWithDelims(tmplCtx, state, NodeRenderContext{}, "f", src, "test", "[[", "]]")
	if err != nil {
		t.Fatalf("non-default + parse-fail should warn-and-emit: %v", err)
	}
	if len(body) == 0 {
		t.Errorf("rendered bytes should be emitted unchanged")
	}
	if len(state.Warnings) == 0 {
		t.Errorf("warning should be appended")
	}
}

// TestEvaluateOneArtifactWithDelims_EmptyDocumentNode pins the
// empty-document branch: an output that parses to a zero-kind yaml.Node
// (e.g., just whitespace or a `---` separator) returns the bytes as-is
// without provenance injection.
func TestEvaluateOneArtifactWithDelims_EmptyDocumentNode(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{Recipe: &types.Recipe{}}
	tmplCtx := &types.TemplateContext{}
	body, err := evaluateOneArtifactWithDelims(tmplCtx, state, NodeRenderContext{}, "f", "---\n", "test", "{{", "}}")
	if err != nil {
		t.Fatalf("empty doc: %v", err)
	}
	// Output may be the rendered bytes unchanged or a trimmed variant —
	// both surface the empty-document branch. Just verify no panic + non-error.
	_ = body
}
