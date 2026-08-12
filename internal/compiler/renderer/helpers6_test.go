// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// TestPhaseB3_EarlyReturns pins the early-return arms of phaseB3: nil recipe and
// zero artifacts are both no-ops (no emitter dispatch, no emitted files).
func TestPhaseB3_EarlyReturns(t *testing.T) {
	t.Parallel()
	st := &PhaseBState{}
	if err := phaseB3(context.Background(), &types.TemplateContext{}, st, &stubArtifactsEmitter{}, NodeRenderContext{}); err != nil {
		t.Errorf("nil recipe: %v", err)
	}
	if len(st.EmittedFiles) != 0 {
		t.Errorf("nil recipe emitted files: %v", st.EmittedFiles)
	}
	st = &PhaseBState{Recipe: &types.Recipe{}}
	if err := phaseB3(context.Background(), &types.TemplateContext{}, st, &stubArtifactsEmitter{}, NodeRenderContext{}); err != nil {
		t.Errorf("zero artifacts: %v", err)
	}
	if len(st.EmittedFiles) != 0 {
		t.Errorf("zero artifacts emitted files: %v", st.EmittedFiles)
	}
}

// TestBuildArtifactsProjection_FlatHappyPath pins the host projection for an
// inline entry whose template produces non-metadata content: the entry carries
// the evaluated body verbatim.
func TestBuildArtifactsProjection_FlatHappyPath(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{
		Recipe:        &types.Recipe{},
		RecipeName:    "test/recipe",
		RecipeVersion: "1.0.0",
	}
	state.Recipe.Spec.Artifacts.Set("notes.txt", types.ArtifactEntry{
		Inline: "plain text content\n",
	})
	proj, err := buildArtifactsProjection(&types.TemplateContext{}, state, NodeRenderContext{NodeID: "n"}, "{{", "}}")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if len(proj.Entries) != 1 {
		t.Fatalf("expected 1 entry; got %d", len(proj.Entries))
	}
	if !strings.Contains(string(proj.Entries[0].Content), "plain text content") {
		t.Errorf("body lost; got %s", proj.Entries[0].Content)
	}
}

// TestSpliceArtifactProvenance_NoMetadataMappingHappyPath pins the placer on a
// mapping manifest with no metadata block: a silent no-op that returns the body
// unchanged.
func TestSpliceArtifactProvenance_NoMetadataMappingHappyPath(t *testing.T) {
	t.Parallel()
	body, warning, err := SpliceArtifactProvenance("f.yaml", []byte("kind: Pod\n"), nil, nil, true, 2)
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if warning != "" {
		t.Errorf("no-metadata manifest should not warn; got %q", warning)
	}
	if !strings.Contains(string(body), "kind: Pod") {
		t.Errorf("body lost; got %s", body)
	}
}

// TestSpliceArtifactProvenance_EmptyOutput pins the empty-content short-circuit:
// blank content returns as-is with no warning.
func TestSpliceArtifactProvenance_EmptyOutput(t *testing.T) {
	t.Parallel()
	body, warning, err := SpliceArtifactProvenance("f", []byte("   \n\n  "), nil, nil, true, 2)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if warning != "" {
		t.Errorf("empty content should not warn; got %q", warning)
	}
	if strings.TrimSpace(string(body)) != "" {
		t.Errorf("expected empty body; got %q", body)
	}
}

// TestSpliceArtifactProvenance_AlternateDelimsNoMetadataIsByteFaithful pins the
// alternate-delimiter placer on a metadata-less manifest: the surgical splice has
// nothing to stamp, so it is a SILENT no-op — the original bytes (including the
// unquoted Helm `{{ }}` the host left untouched under [[ ]] evaluation) are
// emitted byte-faithfully and no skip warning is raised.
func TestSpliceArtifactProvenance_AlternateDelimsNoMetadataIsByteFaithful(t *testing.T) {
	t.Parallel()
	// Content already evaluated by the host under [[ ]]; the Helm {{ }} survives.
	src := []byte("kind: Pod\nname: n\nvalueFrom: {{ .Values.x }}\n")
	body, warning, err := SpliceArtifactProvenance("f", src, nil, nil, false, 2)
	if err != nil {
		t.Fatalf("non-default: %v", err)
	}
	if !strings.Contains(string(body), "valueFrom: {{ .Values.x }}") {
		t.Errorf("body should preserve the unquoted Helm token; got %s", body)
	}
	if strings.Contains(string(body), "aggrega.ai/") {
		t.Errorf("metadata-less manifest should not be stamped; got %s", body)
	}
	if warning != "" {
		t.Errorf("no-metadata manifest is a silent no-op; want no warning, got %q", warning)
	}
}

// TestSpliceArtifactProvenance_DefaultDelimsParseError pins the YAML-parse-error
// path for default delimiters: a non-YAML result is a hard error.
func TestSpliceArtifactProvenance_DefaultDelimsParseError(t *testing.T) {
	t.Parallel()
	_, _, err := SpliceArtifactProvenance("f", []byte("this: is: malformed yaml: [unclosed"),
		map[string]string{"aggrega.ai/component-id": "n"}, nil, true, 2)
	if err == nil {
		t.Errorf("malformed YAML at default-delims should fail")
	}
}

// TestPreCheckArtifactPillars_AllBranches walks the pre-check's grid: each pillar
// referenced or not, against that pillar declared or not, plus the two source
// shapes a declaration can take and the empty-string case that reads as none.
//
// Every halting row asserts the sentinel rather than "some error". The check has
// two arms reporting one condition, and an arm that stopped raising the identity —
// or a different halt preempting it, since the reference is parsed before either
// arm is reached — is exactly what an any-error assertion cannot see.
//
// The passing rows are the other half of the same proof: a reference to a pillar
// that IS declared must not halt, and a template referencing neither pillar must
// not halt whatever is declared. Without them a check that refused everything
// would satisfy every halting row.
func TestPreCheckArtifactPillars_AllBranches(t *testing.T) {
	t.Parallel()

	// declared carries the two independent axes the check reads, so a row states
	// the state it runs against rather than inheriting it from the row above.
	type declared struct {
		workload bool
		source   any
	}
	for _, tc := range []struct {
		name    string
		state   declared
		tmpl    string
		wantErr bool
	}{
		{
			name:    "workload key referenced, no workload declared",
			tmpl:    "{{ .workload.containers }}",
			wantErr: true,
		},
		{
			name:    "workload root referenced bare, no workload declared",
			tmpl:    "{{ .workload }}",
			wantErr: true,
		},
		{
			name:    "source key referenced, no source declared",
			tmpl:    "{{ .source.path }}",
			wantErr: true,
		},
		{
			name:    "source root referenced bare, no source declared",
			tmpl:    "{{ .source }}",
			wantErr: true,
		},
		{
			name:  "source referenced, declared as a string",
			state: declared{source: "/chart/path"},
			tmpl:  "{{ .source }}",
		},
		{
			name:  "source key referenced, declared as a structured value",
			state: declared{source: map[string]any{"chart": "x"}},
			tmpl:  "{{ .source.chart }}",
		},
		{
			// The resolved source is what this reads, so a source: template that
			// rendered to nothing is indistinguishable here from no source: at all.
			name:    "source referenced, declared as an empty string",
			state:   declared{source: ""},
			tmpl:    "{{ .source }}",
			wantErr: true,
		},
		{
			name:  "workload referenced, workload declared",
			state: declared{workload: true},
			tmpl:  "{{ .workload }}",
		},
		{
			// The near-miss control: a name one edit away from a checked pillar
			// belongs to neither arm, so this check is silent and the unknown-root
			// halt owns it. A row asserting a halt here would be asserting the
			// wrong condition against this function.
			name: "neither pillar referenced, nothing declared",
			tmpl: "{{ .values.port }}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := &PhaseBState{Recipe: &types.Recipe{}}
			state.Recipe.Spec.Source = tc.state.source
			if tc.state.workload {
				state.ParsedWorkload = &types.Workload{}
			}

			err := preCheckArtifactPillars(state, "f", tc.tmpl, "{{", "}}")
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected no halt, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a halt, got none")
			}
			if !errors.Is(err, ErrArtifactPillarNotDeclared()) {
				t.Errorf("halt does not carry ErrArtifactPillarNotDeclared: %v", err)
			}
		})
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

// TestBuildArtifactsProjection_PillarPrecheckError pins the pre-check error
// propagation: an artifact template referencing .workload without workload
// declared returns CATALOG_ERROR from the projection builder.
func TestBuildArtifactsProjection_PillarPrecheckError(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{Recipe: &types.Recipe{}}
	state.Recipe.Spec.Artifacts.Set("f.yaml", types.ArtifactEntry{Inline: "{{ .workload.x }}"})
	_, err := buildArtifactsProjection(&types.TemplateContext{}, state, NodeRenderContext{}, "{{", "}}")
	if err == nil {
		t.Errorf("missing-pillar reference should fail")
	}
}
