// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// baseProvidesInput is the smallest input the base Recipe provides pass runs
// over: one node, one Recipe, one provides entry carrying the text under test.
func baseProvidesInput(key, tmpl string) (*types.IRGraph, map[string]*types.Recipe) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	provides := types.OrderedMap[string]{}
	provides.Set(key, tmpl)
	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}},
		map[string]*types.Recipe{
			"db": {
				Metadata: types.RecipeMetadata{Name: "probe-recipe"},
				Spec:     types.RecipeSpec{Provides: provides},
			},
		}
}

// The attachment name is a field of the provides context type, so an evaluation
// stating no narrowing admits it — and then resolves it through the surface's
// missing-key policy into an empty value, because no attachment is being
// resolved during a base Recipe's provides pass.
//
// The refusal is therefore asserted at the evaluation itself, not only at the
// rule declared over the same authored text. Both refuse it, and this is the
// half that makes the agreement structural rather than a consequence of which
// surface runs first: a pass reached without the declared rule having run halts
// here instead of piping an empty value into a consumer's injection context.
//
// The evaluation composes its own halt over the refusal, so the identity
// asserted is the provides-evaluation one and the refused reference is read from
// the message that halt composes.
func TestEvaluateAllProvides_AttachmentNameHaltsRatherThanRenderingEmpty(t *testing.T) {
	t.Parallel()
	graph, recipes := baseProvidesInput("host", `{{ .attachment_name }}`)
	out, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
	if err == nil {
		rendered := out["db"]
		host, _ := rendered.Get("host")
		t.Fatalf("expected a halt; the pass rendered host = %q, which is the silent-empty value the refusal exists to prevent", host)
	}
	if !errors.Is(err, ErrProvidesEvaluation()) {
		t.Errorf("expected ErrProvidesEvaluation, got: %v", err)
	}
	if !strings.Contains(err.Error(), ".attachment_name") {
		t.Errorf("the diagnostic does not name the refused reference .attachment_name: %v", err)
	}
	t.Logf("diagnostic: %v", err)
}

// The two surfaces over one authored provides entry reach the same verdict, and
// neither depends on the other having run. The rule declared over the authored
// text refuses the reference before anything is evaluated; the evaluation
// refuses it again against the same declared narrowing. Retiring or reordering
// either one leaves the other still refusing, which is the property this pins.
func TestProvidesAttachmentNameRefusedByBothSurfaces(t *testing.T) {
	t.Parallel()
	const tmpl = `{{ .attachment_name }}`

	declared := providesSelfContainmentHalt(t, tmpl, "probe-recipe")
	if declared == nil {
		t.Fatal("the rule declared over the authored text admitted .attachment_name")
	}
	if !errors.Is(declared, ErrProvidesSelfContainment()) {
		t.Errorf("declared rule: expected ErrProvidesSelfContainment, got: %v", declared)
	}

	graph, recipes := baseProvidesInput("host", tmpl)
	if _, _, evaluated := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{}); evaluated == nil {
		t.Error("the evaluation admitted .attachment_name, so the agreement between the two surfaces rests on which of them runs first")
	}
}

// The narrowing removes one root and nothing else. Every root the provides
// surface does expose still renders through the pass, so stating the declared
// narrowing at the evaluation cannot have narrowed the surface past what it
// declares.
func TestEvaluateAllProvides_AdmittedRootsStillRender(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})

	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("db", json.RawMessage(`{"port":"5432"}`))
	infraMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping.Set("db", json.RawMessage(`{"zone":"eu-west-1a"}`))

	graph := &types.IRGraph{Spec: types.IRSpec{
		Nodes:         nodes,
		ValuesMapping: valuesMapping,
		InfraMapping:  infraMapping,
	}}

	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .context.instance_id }}.svc`)
	provides.Set("port", `{{ .values.port }}`)
	provides.Set("zone", `{{ .infra.zone }}`)
	provides.Set("ident", `{{ .locals.ident }}`)
	locals := types.OrderedMap[string]{}
	locals.Set("ident", `{{ .context.instance_id }}-primary`)

	recipes := map[string]*types.Recipe{
		"db": {
			Metadata: types.RecipeMetadata{Name: "probe-recipe"},
			Spec:     types.RecipeSpec{Provides: provides, Locals: locals},
		},
	}

	resolved, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
	if err != nil {
		t.Fatalf("the admitted roots must still render: %v", err)
	}
	dbProv := resolved["db"]
	for key, want := range map[string]string{
		"host":  "db.svc",
		"port":  "5432",
		"zone":  "eu-west-1a",
		"ident": "db-primary",
	} {
		if got, _ := dbProv.Get(key); got != want {
			t.Errorf("provides[%q] = %q, want %q", key, got, want)
		}
	}
}

// The walk the scope consults is the same one the evaluation seeds its data
// from, so the attachment name has to be collected as a root reference at all.
// Without this the halt above could pass for a reason other than the refusal.
func TestProvidesAttachmentNameIsCollectedAsARootReference(t *testing.T) {
	t.Parallel()
	tree, err := gotmpl.ParseTree(`{{ .attachment_name }}`, "{{", "}}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	refs := gotmpl.CollectRootRefs(tree)
	for _, ref := range refs {
		if len(ref.Segs) > 0 && ref.Segs[0] == "attachment_name" {
			return
		}
	}
	t.Fatalf("the walk collected %v, which does not include the attachment name; the halt above would then pass for the wrong reason", refs)
}
