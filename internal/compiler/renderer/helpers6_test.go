// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// TestEvaluateArtifacts_NilState pins the early-return arms of
// evaluateArtifacts: nil state, nil recipe, zero artifacts.
func TestEvaluateArtifacts_NilState(t *testing.T) {
	t.Parallel()
	files, err := evaluateArtifacts(context.Background(), &types.TemplateContext{}, nil, nil, NodeRenderContext{})
	if err != nil || files != nil {
		t.Errorf("nil state: (%v, %v)", files, err)
	}
	files, err = evaluateArtifacts(context.Background(), &types.TemplateContext{}, &PhaseBState{}, nil, NodeRenderContext{})
	if err != nil || files != nil {
		t.Errorf("nil recipe: (%v, %v)", files, err)
	}
	state := &PhaseBState{Recipe: &types.Recipe{}}
	files, err = evaluateArtifacts(context.Background(), &types.TemplateContext{}, state, nil, NodeRenderContext{})
	if err != nil || files != nil {
		t.Errorf("zero artifacts: (%v, %v)", files, err)
	}
}

// TestEvaluateArtifacts_FlatEngineHappyPath pins the standard (non-
// external-handler) engine path with an inline artifact template that
// produces YAML output requiring no metadata-injection.
func TestEvaluateArtifacts_FlatEngineHappyPath(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{
		Recipe:        &types.Recipe{},
		RecipeName:    "test/recipe",
		RecipeVersion: "1.0.0",
	}
	state.Recipe.Spec.Artifacts.Set("notes.txt", types.ArtifactEntry{
		Inline: "plain text content\n",
	})
	tmplCtx := &types.TemplateContext{}
	got, err := evaluateArtifacts(context.Background(), tmplCtx, state, noopEngine{}, NodeRenderContext{NodeID: "n"})
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 file; got %d", len(got))
	}
	if !strings.Contains(string(got[0].Body), "plain text content") {
		t.Errorf("body lost; got %s", got[0].Body)
	}
}

// TestEvaluateOneArtifact_DefaultDelimsWrapper pins the default-delims
// wrapper's direct invocation.
func TestEvaluateOneArtifact_DefaultDelimsWrapper(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{
		Recipe:        &types.Recipe{},
		RecipeName:    "test/recipe",
		RecipeVersion: "1.0.0",
	}
	tmplCtx := &types.TemplateContext{}
	body, err := evaluateOneArtifact(tmplCtx, state, NodeRenderContext{NodeID: "n"}, "f.yaml", "kind: Pod\n", "test")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if !strings.Contains(string(body), "kind: Pod") {
		t.Errorf("body lost; got %s", body)
	}
}

// TestEvaluateOneArtifactWithDelims_EmptyOutput pins the empty-output
// short-circuit branch (line 283-285 of artifacts.go).
func TestEvaluateOneArtifactWithDelims_EmptyOutput(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{
		Recipe: &types.Recipe{},
	}
	tmplCtx := &types.TemplateContext{}
	body, err := evaluateOneArtifactWithDelims(tmplCtx, state, NodeRenderContext{}, "f", "   \n\n  ", "test", "{{", "}}")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	// Empty content returns as-is.
	if strings.TrimSpace(string(body)) != "" {
		t.Errorf("expected empty body; got %q", body)
	}
}

// TestEvaluateOneArtifactWithDelims_NonDefaultDelimsExternalHandler
// pins the external-handler path on a metadata-less manifest: the surgical
// splice has nothing to stamp (no metadata mapping), so it is a SILENT no-op
// per the "manifests without metadata skip silently" contract — the
// original bytes (including the unquoted Helm `{{ }}`) are emitted byte-faithfully
// and no skip warning is appended. (The round-trip path this replaced could not
// distinguish "no metadata" from "round-trip mangled" and warned spuriously.)
func TestEvaluateOneArtifactWithDelims_NonDefaultDelimsExternalHandler(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{Recipe: &types.Recipe{}}
	tmplCtx := &types.TemplateContext{}
	// Template uses [[ ]] delims but content contains {{ }}; no metadata mapping.
	src := "kind: Pod\nname: [[ .name ]]\nvalueFrom: {{ .Values.x }}\n"
	tmplCtx.Values.Set("name", "n")
	body, err := evaluateOneArtifactWithDelims(tmplCtx, state, NodeRenderContext{}, "f", src, "test", "[[", "]]")
	if err != nil {
		t.Fatalf("non-default: %v", err)
	}
	// Byte-faithful: the unquoted {{ }} survives unchanged.
	if !strings.Contains(string(body), "valueFrom: {{ .Values.x }}") {
		t.Errorf("body should preserve the unquoted Helm token; got %s", body)
	}
	if strings.Contains(string(body), "aggrega.ai/") {
		t.Errorf("metadata-less manifest should not be stamped; got %s", body)
	}
	if len(state.Warnings) != 0 {
		t.Errorf("no-metadata manifest is a silent no-op; want no warnings, got %v", state.Warnings)
	}
}

// TestEvaluateOneArtifactWithDelims_DefaultDelimsParseError pins the
// YAML-parse-error path for default delimiters: parse failure returns
// CATALOG_ERROR.
func TestEvaluateOneArtifactWithDelims_DefaultDelimsParseError(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{Recipe: &types.Recipe{}}
	tmplCtx := &types.TemplateContext{}
	// Output that's not valid YAML.
	src := "this: is: malformed yaml: [unclosed"
	_, err := evaluateOneArtifactWithDelims(tmplCtx, state, NodeRenderContext{}, "f", src, "test", "{{", "}}")
	if err == nil {
		t.Errorf("malformed YAML at default-delims should fail")
	}
}

// TestPreCheckArtifactPillars_AllBranches pins workload-declared and
// source-declared dispatch arms, plus the missing-pillar errors.
func TestPreCheckArtifactPillars_AllBranches(t *testing.T) {
	t.Parallel()
	// No workload, no source — references to either pillar error.
	state := &PhaseBState{Recipe: &types.Recipe{}}
	err := preCheckArtifactPillars(state, "f", "{{ .workload.containers }}", "{{", "}}")
	if err == nil {
		t.Errorf("workload ref without workload should fail")
	}
	err = preCheckArtifactPillars(state, "f", "{{ .source.path }}", "{{", "}}")
	if err == nil {
		t.Errorf("source ref without source should fail")
	}
	// Source declared as string.
	state.Recipe.Spec.Source = "/chart/path"
	if err := preCheckArtifactPillars(state, "f", "{{ .source }}", "{{", "}}"); err != nil {
		t.Errorf("source-declared should pass: %v", err)
	}
	// Source declared as non-string (structured).
	state.Recipe.Spec.Source = map[string]any{"chart": "x"}
	if err := preCheckArtifactPillars(state, "f", "{{ .source.chart }}", "{{", "}}"); err != nil {
		t.Errorf("structured source should pass: %v", err)
	}
	// Source declared as empty string → still flagged.
	state.Recipe.Spec.Source = ""
	if err := preCheckArtifactPillars(state, "f", "{{ .source }}", "{{", "}}"); err == nil {
		t.Errorf("empty-string source should fail")
	}
	// Workload declared.
	state.Recipe.Spec.Source = nil
	state.ParsedWorkload = &types.Workload{}
	if err := preCheckArtifactPillars(state, "f", "{{ .workload }}", "{{", "}}"); err != nil {
		t.Errorf("workload-declared should pass: %v", err)
	}
}

// TestMappingValueByKey_EveryBranch pins the mapping-content lookup:
// nil, wrong-kind, hit, miss.
func TestMappingValueByKey_EveryBranch(t *testing.T) {
	t.Parallel()
	if mappingValueByKey(nil, "k") != nil {
		t.Errorf("nil → nil")
	}
	// Wrong kind.
	if mappingValueByKey(&yaml.Node{Kind: yaml.SequenceNode}, "k") != nil {
		t.Errorf("sequence → nil")
	}
	// Hit.
	m := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "a"},
		{Kind: yaml.ScalarNode, Value: "1"},
	}}
	if got := mappingValueByKey(m, "a"); got == nil || got.Value != "1" {
		t.Errorf("hit a: %v", got)
	}
	// Miss.
	if mappingValueByKey(m, "absent") != nil {
		t.Errorf("absent → nil")
	}
}

// TestNodeKindName_EveryKind pins each documented yaml.Kind dispatch.
func TestNodeKindName_EveryKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind yaml.Kind
		want string
	}{
		{yaml.DocumentNode, "document"},
		{yaml.SequenceNode, "sequence"},
		{yaml.MappingNode, "mapping"},
		{yaml.ScalarNode, "scalar"},
		{yaml.AliasNode, "alias"},
		{yaml.Kind(99), "unknown kind 99"},
	}
	for _, tc := range cases {
		if got := nodeKindName(tc.kind); got != tc.want {
			t.Errorf("kind(%d) = %q; want %q", tc.kind, got, tc.want)
		}
	}
}

// TestProvenanceSkipWarning_FilenameSubstring pins the formatter.
func TestProvenanceSkipWarning_FilenameSubstring(t *testing.T) {
	t.Parallel()
	got := provenanceSkipWarning("custom-file.yaml")
	if !strings.Contains(got, "custom-file.yaml") {
		t.Errorf("warning should contain filename; got %q", got)
	}
}

// TestEvaluateOneArtifactWithDelims_PillarPrecheckError pins the
// pre-check error propagation: a template referencing .workload without
// workload declared returns CATALOG_ERROR.
func TestEvaluateOneArtifactWithDelims_PillarPrecheckError(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{Recipe: &types.Recipe{}}
	_, err := evaluateOneArtifactWithDelims(&types.TemplateContext{}, state, NodeRenderContext{},
		"f", "{{ .workload.x }}", "test", "{{", "}}")
	if err == nil {
		t.Errorf("missing-pillar reference should fail")
	}
}
