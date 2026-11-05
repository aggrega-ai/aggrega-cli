// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// valuesCaptureEngine records the values a FeatureRecipe's render hands its
// provider — the output of the FeatureRecipe's own values mapping — so a test can
// read what that mapping evaluated to.
type valuesCaptureEngine struct{ captured *map[string]any }

func (e valuesCaptureEngine) Render(_ context.Context, values map[string]any, _ NodeRenderContext) ([]RenderedFile, error) {
	*e.captured = values
	return nil, nil
}

func (e valuesCaptureEngine) Contract() ProviderContract {
	return ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// TestAttachmentValuesMappingReadsItsOwnSource drives a per-component attachment's
// render and asserts what `.source` reads in the FeatureRecipe's own templates: the
// source that FeatureRecipe declares, evaluated ahead of its values mapping, and
// never the source of the component it is attached to.
//
// The host context carries a source of its own in every row but one, because that
// is the state the attachment's copy is taken from: the host's own render has
// already published the host's source by the time its attachments render. A row
// whose host declares none is the shape a build first showed the defect in — the
// mapping read nothing where the attachment declared a source. The sourceless
// attachment row reads the pillar from an artifacts body, the template a sourceless
// FeatureRecipe renders, and asserts it reads nothing rather than the host's.
func TestAttachmentValuesMappingReadsItsOwnSource(t *testing.T) {
	t.Parallel()

	const (
		hostSource       = "oci://host.example.test/charts/host"
		attachmentSource = "oci://attachment.example.test/charts/secret"
	)

	for _, tc := range []struct {
		name       string
		hostSource any
		frSource   any
		mapping    string
		artifact   string
		want       string
	}{
		{
			name:       "the mapping reads the attachment's own source over the host's",
			hostSource: hostSource, frSource: attachmentSource,
			mapping: `src: "{{ .source }}"`,
			want:    attachmentSource,
		},
		{
			name:       "the mapping reads the attachment's own source on a host declaring none",
			hostSource: nil, frSource: attachmentSource,
			mapping: `src: "{{ .source }}"`,
			want:    attachmentSource,
		},
		{
			name:       "control: a mapping not reading the pillar renders what it rendered before",
			hostSource: hostSource, frSource: attachmentSource,
			mapping: `src: "fixed"`,
			want:    "fixed",
		},
		{
			name:       "a sourceless attachment reads nothing at the pillar, never the host's source",
			hostSource: hostSource, frSource: nil,
			artifact: `src: "{{ .source }}"` + "\n",
			want:     "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hostCtx := &types.TemplateContext{
				Context: types.ContextBlock{InstanceID: "grafana"},
				Values:  types.OrderedMap[any]{},
				Infra:   types.OrderedMap[any]{},
				Source:  tc.hostSource,
			}
			hostRecipe := &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "grafana-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "helm", Implements: "core/grafana"},
			}
			state := NewPhaseBState(hostRecipe, "grafana", "local", "")

			fr := &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "secret-from-parameter", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/referenced-secret",
					Source:     tc.frSource,
				},
			}
			if tc.mapping != "" {
				fr.Spec.ValuesMapping = types.ValuesMapping{Inline: tc.mapping}
			}
			if tc.artifact != "" {
				art := types.OrderedMap[types.ArtifactEntry]{}
				art.Set("source.yaml", types.ArtifactEntry{Inline: tc.artifact})
				fr.Spec.Artifacts = art
			}

			var captured map[string]any
			emitter := &stubArtifactsEmitter{}
			factory := func(string) (Provider, error) {
				if tc.artifact != "" {
					return emitter, nil
				}
				return valuesCaptureEngine{captured: &captured}, nil
			}

			b6, _, err := phaseB6(context.Background(), hostCtx, state,
				[]FeatureRecipeRef{{InstanceName: "admin_password", FeatureRecipe: fr}}, factory,
				NodeProvides{}, RecipeLookup{}, NodeRenderContext{NodeID: "grafana", BoundaryPath: "local"},
				nil, nil, nil, nil, RendererInput{}, nil, false, "helm", false, renderctx.RenderContext{})
			if err != nil {
				t.Fatalf("phaseB6: %v", err)
			}

			got, read := "", false
			if tc.artifact != "" {
				for name, content := range b6.files {
					if strings.HasSuffix(name, "source.yaml") {
						got, read = string(content), true
					}
				}
				if !read {
					t.Fatalf("the attachment's artifact was not emitted (files: %d)", len(b6.files))
				}
				if want := `src: "` + tc.want + `"`; !strings.Contains(got, want) {
					t.Errorf(".source on a sourceless attachment rendered %q; want %s — the host's source must not reach an attachment that declares none", got, want)
				}
			} else {
				value, ok := captured["src"]
				if !ok {
					t.Fatalf("the provider was not handed the mapping's output: %#v", captured)
				}
				if value != tc.want {
					t.Errorf("the attachment's values mapping read .source as %#v; want %q — its own source, evaluated before the mapping", value, tc.want)
				}
			}
			if hostCtx.Source != tc.hostSource {
				t.Errorf("the attachment render wrote its source back onto the host's own context: %#v", hostCtx.Source)
			}
		})
	}
}

// TestBoundaryScopeValuesMappingReadsItsOwnSource drives the boundary-scope dispatch
// and asserts its values mapping reads the FeatureRecipe's own source at `.source`.
// The boundary's context is built fresh rather than copied from a host, so the
// pillar reads nothing unless the dispatch publishes the source it evaluated.
func TestBoundaryScopeValuesMappingReadsItsOwnSource(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		mapping string
		want    string
	}{
		{name: "the mapping reads the FeatureRecipe's own source", mapping: `src: "{{ .source }}"`, want: "./charts/local-bridge"},
		{name: "control: a mapping not reading the pillar renders what it rendered before", mapping: `src: "fixed"`, want: "fixed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			document := filepath.Join(root, "catalog", "compose-bridge", "featurerecipe.yaml")

			fr, graph, release := boundaryScopeFRWithSource("./charts/local-bridge", types.OrderedMap[string]{})
			fr.Spec.ValuesMapping = types.ValuesMapping{Inline: tc.mapping}

			var captured map[string]any
			factory := func(string) (Provider, error) { return valuesCaptureEngine{captured: &captured}, nil }
			key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

			if err := dispatchBoundaryScopeFR(
				context.Background(), fr, "bridge", key, &PhaseBState{}, NewBoundaryFRState(),
				NodeRenderContext{}, factory, frDocumentLookup(document), graph, release, nil, nil,
				RendererInput{Lockfile: &lockfile.Lockfile{Version: 1}, WorkspaceRoot: root, CacheDir: ".aggrega/cache"},
			); err != nil {
				t.Fatalf("dispatchBoundaryScopeFR: %v", err)
			}

			value, ok := captured["src"]
			if !ok {
				t.Fatalf("the provider was not handed the mapping's output: %#v", captured)
			}
			if value != tc.want {
				t.Errorf("the boundary-scope values mapping read .source as %#v; want %q", value, tc.want)
			}
		})
	}
}
