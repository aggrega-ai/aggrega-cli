// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// jsonTagOf reports the json name a struct field publishes, with any option
// suffix dropped. It reads the tag rather than the field name because the tag is
// what a template author types.
func jsonTagOf(t *testing.T, v any, field string) string {
	t.Helper()
	f, ok := reflect.TypeOf(v).FieldByName(field)
	if !ok {
		t.Fatalf("%T declares no field %q", v, field)
	}
	tag := f.Tag.Get("json")
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}

// TestRenderedNameKeyMatchesBothContextTypes holds the two spellings the rule and
// the three scope narrowings compare against to the json tags the context types
// actually publish.
//
// Without it those spellings are a fourth copy of a name declared twice, and a
// rename would leave the rule watching a key no context defines — which reports
// exactly what a rule that ran and approved reports. The guard is what the
// constants' own documentation claims exists, so its absence would make that
// sentence false.
//
// Both context types are checked, not one: the value is published on the
// schema-bound render context and on the narrower block a provides template
// reads, and a spelling correct for one and wrong for the other would leave the
// rule guarding half the surface.
func TestRenderedNameKeyMatchesBothContextTypes(t *testing.T) {
	if got := jsonTagOf(t, types.TemplateContext{}, "Context"); got != identityContextRoot {
		t.Errorf("types.TemplateContext publishes the identity root as %q, the constant says %q", got, identityContextRoot)
	}
	if got := jsonTagOf(t, ProvidesContext{}, "Context"); got != identityContextRoot {
		t.Errorf("ProvidesContext publishes the identity root as %q, the constant says %q", got, identityContextRoot)
	}
	if got := jsonTagOf(t, types.ContextBlock{}, "RenderedName"); got != renderedNameContextKey {
		t.Errorf("types.ContextBlock publishes the name as %q, the constant says %q", got, renderedNameContextKey)
	}
	if got := jsonTagOf(t, ProvidesContextBlock{}, "RenderedName"); got != renderedNameContextKey {
		t.Errorf("ProvidesContextBlock publishes the name as %q, the constant says %q", got, renderedNameContextKey)
	}
}

// TestRenderedNameReferenceCoversEverySpelling exercises the claim the reference
// reader's own documentation makes: every spelling that RESOLVES to the key is
// found, not only the direct one.
//
// The rows that matter are the indirect ones. A reader matching authored
// keystrokes would pass the direct row and fail the others, and the value would
// then reach an emitted artifact through exactly the spellings a defensive author
// reaches for — which is the hole the refusal exists to close.
//
// The negative rows are what keep the positives meaningful: a reader that said
// yes to everything would satisfy every positive row on its own.
func TestRenderedNameReferenceCoversEverySpelling(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		reads bool
		path  string
	}{
		{
			name:  "the direct spelling",
			text:  "{{ .context.rendered_name }}.svc",
			reads: true,
			path:  ".context.rendered_name",
		},
		{
			name:  "read through a with body over the identity pillar",
			text:  "{{ with .context }}{{ .rendered_name }}{{ end }}.svc",
			reads: true,
			path:  ".context.rendered_name",
		},
		{
			name:  "read through a variable bound to the identity pillar",
			text:  "{{ $c := .context }}{{ $c.rendered_name }}.svc",
			reads: true,
			path:  ".context.rendered_name",
		},
		{
			name: "read through the root under a rebound dot",
			text: "{{ with .values }}{{ $.context.rendered_name }}{{ end }}",
			// The reported path keeps the author's own spelling, dollar included,
			// rather than a normalised one. That is what the refusal quotes, and it
			// has to be the string the author can search their own document for.
			reads: true,
			path:  "$.context.rendered_name",
		},
		{
			name:  "the identity, which is a different key and stays admitted",
			text:  "{{ .context.instance_id }}.svc",
			reads: false,
		},
		{
			name:  "a same-named key under another root",
			text:  "{{ .values.rendered_name }}",
			reads: false,
		},
		{
			name:  "a bare identity-pillar reference reads no key",
			text:  "{{ toJson .context }}",
			reads: false,
		},
		{
			name:  "no template at all",
			text:  "postgres.svc.cluster.local",
			reads: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := gotmpl.ParseTree(tc.text, "{{", "}}")
			if err != nil {
				t.Fatalf("parsing %q: %v", tc.text, err)
			}
			path, reads := renderedNameReference(tree)
			if reads != tc.reads {
				t.Fatalf("%q: read=%v, want %v (path %q)", tc.text, reads, tc.reads, path)
			}
			if reads && path != tc.path {
				t.Errorf("%q: path %q, want %q", tc.text, path, tc.path)
			}
		})
	}
}

// TestRenderedNameRemedyDiffersByWhetherTheNodeSuppliesSource pins the property
// the rendered-name refusal turns on: it tells the author what to write instead,
// and the two cases need different sentences.
//
// A node supplying no source, on a target reporting no name for it, has its output
// named after its own identity, so there IS a correct value and the remedy names
// it. A node that does supply source and whose target still reports no name has
// no substitute, and naming the identity there would send its reader to a value
// that names none of that node's resources — so the assertion is that the
// identity is named in exactly one of the two.
func TestRenderedNameRemedyDiffersByWhetherTheNodeSuppliesSource(t *testing.T) {
	sourceless := renderedNameRemedy(false)
	sourced := renderedNameRemedy(true)

	if sourceless == sourced {
		t.Fatal("both cases render one remedy; the two absences have different correct answers")
	}
	if !containsSubstring(sourceless, "{{ .context.instance_id }}") {
		t.Errorf("the sourceless remedy does not name the value that IS correct there: %q", sourceless)
	}
	if containsSubstring(sourced, "{{ .context.instance_id }}") {
		t.Errorf("the source-supplying remedy names the identity, which is wrong for that node: %q", sourced)
	}
}

// containsSubstring is a local reading of substring containment, kept here so the
// assertions above read as one property each.
func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// recordingRenderedNamer is a provider engine implementing the rendered-name
// capability and nothing else of note. It records every question it is put, and
// answers with a name only for a node the question calls synthesized — so a name
// reaching the collected result is itself evidence the flag crossed, independent
// of the recorded inputs.
type recordingRenderedNamer struct {
	asked []RenderedNameInput
}

func (r *recordingRenderedNamer) Render(context.Context, map[string]any, NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}

func (r *recordingRenderedNamer) Contract() ProviderContract { return ProviderContract{} }

func (r *recordingRenderedNamer) RenderedName(_ context.Context, in RenderedNameInput) (RenderedNameOutput, error) {
	r.asked = append(r.asked, in)
	if in.Synthesized {
		return RenderedNameOutput{Name: "named-" + in.NodeID}, nil
	}
	return RenderedNameOutput{}, nil
}

// TestCollectRenderedNamesTellsTheProviderWhichInputKindsANodeDeclares drives
// the pass over one node of each shape a Recipe can author — source alone, a
// workload alone, both, and neither — and reads what the provider was asked.
//
// The synthesized flag is the subject: a provider that names the resources it
// synthesizes after a name of its own can only report that name if it learns the
// node is synthesized, and a pass that never set the flag would leave every such
// node with no name to publish while every sourced row still passed. The sourced
// flag is asserted beside it so a fix that swapped or merged the two fails here,
// and the neither-row keeps a pass that set both on every node from passing.
func TestCollectRenderedNamesTellsTheProviderWhichInputKindsANodeDeclares(t *testing.T) {
	graph := &types.IRGraph{}
	for _, id := range []string{"chart", "synth", "both", "placed"} {
		graph.Spec.Nodes.Set(id, types.IRNode{})
	}
	source := "oci://charts.example/app"
	workload := types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1beta1\nkind: Workload\n"}
	recipes := map[string]*types.Recipe{
		"chart":  {Spec: types.RecipeSpec{Capability: "helm", Source: source}},
		"synth":  {Spec: types.RecipeSpec{Capability: "helm", Workload: workload}},
		"both":   {Spec: types.RecipeSpec{Capability: "helm", Source: source, Workload: workload}},
		"placed": {Spec: types.RecipeSpec{Capability: "helm"}},
	}
	engine := &recordingRenderedNamer{}
	factory := func(string) (Provider, error) { return engine, nil }

	names, err := CollectRenderedNames(t.Context(), graph, recipes, factory)
	if err != nil {
		t.Fatalf("collecting rendered names: %v", err)
	}

	// Asked in sorted identity order, which is the pass's own determinism claim.
	want := []RenderedNameInput{
		{NodeID: "both", Sourced: true, Synthesized: true},
		{NodeID: "chart", Sourced: true},
		{NodeID: "placed"},
		{NodeID: "synth", Synthesized: true},
	}
	if !reflect.DeepEqual(engine.asked, want) {
		t.Errorf("the provider was asked %+v, want %+v", engine.asked, want)
	}
	wantNames := NodeRenderedNames{"both": "named-both", "synth": "named-synth"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("collected names %v, want %v", names, wantNames)
	}
}

// TestCollectRenderedNamesAsksNothingWithoutAnEngineFactory pins the no-op shape
// every reader of this pass depends on: a render wired with no engine factory
// reports no name for any node rather than failing, so the exchange costs a build
// that resolves no plugin nothing at all.
func TestCollectRenderedNamesAsksNothingWithoutAnEngineFactory(t *testing.T) {
	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("api", types.IRNode{})

	names, err := CollectRenderedNames(t.Context(), graph, map[string]*types.Recipe{}, nil)
	if err != nil {
		t.Fatalf("collecting with no engine factory: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("reported %d name(s) with no engine factory: %v", len(names), names)
	}
}
