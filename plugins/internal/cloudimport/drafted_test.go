// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// handedInputs names each input of m by its parameter and where it is read.
func handedInputs(m schemadraft.WrappedModule) []string {
	var out []string
	for _, in := range m.Inputs {
		where := "override"
		if in.Source.Pillar == schemadraft.PillarInject {
			where = "inject " + in.Source.Key
		}
		out = append(out, in.Parameter()+" "+where)
	}
	return out
}

// TestModuleNoRowMapsIsDraftedWrapped drafts a module no row maps as a
// Component and a Recipe sourcing the module whole, located by the format and
// written by its module writer: each parameter the node sets is read from
// infra.overrides, the one a neighbour's value is delivered into off the
// inject pipe, the parameter the Recipe binds itself is not carried, and the
// output a neighbour reads is published.
func TestModuleNoRowMapsIsDraftedWrapped(t *testing.T) {
	var handed []schemadraft.WrappedModule
	in := toyInput(t, "toy", "", []string{"module.json"})
	out := Import(in, toyFormat(&handed), toyParse)
	noHalt(t, out.Diagnostics)

	if len(handed) != 1 {
		t.Fatalf("the module writer is handed %d modules, want the one module once", len(handed))
	}
	m := handed[0]
	if m.Type == nil || m.Type.Name != toySearch || m.SourceKind != "toy-module" || m.URL != toyModuleURL ||
		m.Version != "1.2.0" || !reflect.DeepEqual(m.Fixed, []importer.Entry{{Key: "telemetry", Value: "false"}}) {
		t.Errorf("the module writer is handed %+v, want %s from %s at 1.2.0 binding telemetry", m, toySearch,
			toyModuleURL)
	}
	if got, want := handedInputs(m), []string{"name override", "sku override", "replicas override",
		"hostName inject host_name"}; !slices.Equal(got, want) {
		t.Errorf("the module's inputs are %q, want %q", got, want)
	}
	if len(m.Outputs) != 1 || m.Outputs[0].Key != "endpoint" || !slices.Equal(m.Outputs[0].Attribute, []string{"endpoint"}) {
		t.Errorf("the module's outputs are %+v, want endpoint, which the worker reads", m.Outputs)
	}

	name := schemadraft.NameFor(in, toyFormat(nil).Draft, toySearch)
	recipe := fileOf(t, out, "catalog/recipe/toy/toy-search-services/0.1.0/recipe.yaml")
	for _, want := range []string{"  implements: " + name + ":0.1.0\n  source:\n    kind: toy-module\n    url: " +
		toyModuleURL + "\n    version: \"1.2.0\"\n", "telemetry: false"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("the Recipe lacks %q:\n%s", want, recipe)
		}
	}
	fileOf(t, out, "catalog/component/"+name+"/0.1.0/component.yaml")

	aggregate := fileOf(t, out, "aggregate.yaml")
	wantBlock(t, aggregate, "search", `    search:
      type: `+name+`
      uses:
        db:
          db:
            target: db
            inject:
              property:
                - in: "host_name"
                  from: host
`)
	if block := instanceBlock(t, aggregate, "worker"); !strings.Contains(block, "- in: \"search_endpoint\"\n                  from: endpoint\n") {
		t.Errorf("the worker is not delivered the module's output:\n%s", block)
	}
	wantBlock(t, fileOf(t, out, "release.yaml"), "search", `    search:
      infra:
        overrides:
          name: "search-main"
          sku: "basic"
          replicas: 2
`)
	if manifest := fileOf(t, out, "manifest.yaml"); !strings.Contains(manifest, "search: toy/toy-search-services") {
		t.Errorf("the Manifest does not map the module's instance to its drafted Recipe:\n%s", manifest)
	}

	bound := onlyCode(t, out.Diagnostics, CodeValueNotCarried, 1)
	mentions(t, "the warning", bound[0].Message, "telemetry", toyModuleURL)
	i := slices.Index(decisionIDs(out), "search")
	if i < 0 {
		t.Fatalf("no decision names search: %q", decisionIDs(out))
	}
	d := out.Decisions[i]
	if d.Chosen == nil || d.Chosen.Target != name || d.Chosen.Mode != "draft" {
		t.Errorf("the decision on search chooses %+v, want the drafted %s", d.Chosen, name)
	}
	mentions(t, "the rationale", d.Rationale, "drafted:", toyModuleURL)
}

// TestModulesOfOneComponentDraftOnce drafts the modules of one type no row
// maps as one Component and one Recipe, sourcing the module the first by ID
// calls and reading every parameter any of them sets, matched regardless of
// case; each decision names the version the other calls.
func TestModulesOfOneComponentDraftOnce(t *testing.T) {
	var handed []schemadraft.WrappedModule
	out := Import(toyInput(t, "toy", "", []string{"modules.json"}), toyFormat(&handed), toyParse)
	noHalt(t, out.Diagnostics)
	if len(handed) != 1 || handed[0].Version != "1.2.0" {
		t.Fatalf("the module writer is handed %+v, want searchA's module once", handed)
	}
	if got, want := handedInputs(handed[0]), []string{"name override", "sku override", "replicas override"}; !slices.Equal(got, want) {
		t.Errorf("the module's inputs are %q, want %q", got, want)
	}
	var recipes int
	for _, p := range filePaths(out) {
		if strings.HasPrefix(p, "catalog/recipe/") {
			recipes++
		}
	}
	if recipes != 1 {
		t.Errorf("the import writes %q, want one Recipe", filePaths(out))
	}
	wantBlock(t, fileOf(t, out, "release.yaml"), "searchb", `    searchb:
      infra:
        overrides:
          name: "search-b"
          sku: "standard"
          replicas: 3
`)
	for _, d := range out.Decisions {
		mentions(t, "the rationale of "+d.SourceNode.ID, d.Rationale, "searchA", "searchB", "toy/search", "1.2.0",
			"1.3.0")
	}
}

// TestModuleWithNoLocatorHalts halts source-refused on a module no row maps
// where the format has a module writer and nothing locating its module, and
// classes nothing.
func TestModuleWithNoLocatorHalts(t *testing.T) {
	f := toyFormat(nil)
	f.Module = nil
	g := &Graph{Nodes: []*Node{{ID: "search", Type: toySearch, Form: FormModule, Module: &ModuleRef{Path: "toy/search"}}}}
	out, diags := Classify(toyInput(t, "toy", "", nil), f, g)
	if out != nil || len(diags) != 1 || diags[0].Code != string(CodeSourceRefused) {
		t.Fatalf("Classify = %v, %+v; want one source-refused halt and nothing classified", classes(out), diags)
	}
	mentions(t, "the halt", diags[0].Message, "search", toySearch, "importer's authors")
}

// TestModuleAndResourceOfOneComponentHalt halts source-refused where a module
// and a resource no row maps would draft one Component name, which cannot both
// source the module and declare the resource, and classes nothing.
func TestModuleAndResourceOfOneComponentHalt(t *testing.T) {
	out := Import(toyInput(t, "toy", "", []string{"collision.json"}), toyFormat(nil), toyParse)
	draftsNothing(t, out, string(CodeSourceRefused))
	name := schemadraft.NameFor(toyInput(t, "toy", "", nil), toyFormat(nil).Draft, toySearch)
	mentions(t, "the halt", out.Diagnostics[0].Message, "search", "searchModule", name, "mapping_overrides")
}

// TestHeldBackDraftsNothingAndIsNamed holds back a resource whose drafted
// Component the catalog holds: nothing is drafted for it, its decision says
// so, and the instance reading it names it in a comment.
func TestHeldBackDraftsNothingAndIsNamed(t *testing.T) {
	in := toyInput(t, "toy", "", []string{"heldback.json"})
	held := schemadraft.NameFor(in, toyFormat(nil).Draft, toyQueue)
	in = toyInput(t, "toy", "", []string{"heldback.json"}, component(held,
		map[string]string{"component.aggrega.ai/category": "component"}))
	out := Import(in, toyFormat(nil), toyParse)
	noHalt(t, out.Diagnostics)

	for _, p := range filePaths(out) {
		if strings.Contains(p, "toy-messaging-queues") {
			t.Errorf("the import drafts %s for the held-back queue", p)
		}
	}
	for _, e := range out.Entities {
		if e.Name == held {
			t.Errorf("the import emits %+v for the held-back queue", e)
		}
	}
	if got, want := decisionIDs(out), []string{"queue", "worker"}; !slices.Equal(got, want) {
		t.Fatalf("decisions name %q, want %q", got, want)
	}
	mentions(t, "the decision on queue", out.Decisions[0].Rationale, "held back", held)

	aggregate := fileOf(t, out, "aggregate.yaml")
	if strings.Contains(aggregate, "\n    queue:\n") {
		t.Errorf("the Aggregate writes an instance of the held-back queue:\n%s", aggregate)
	}
	block := instanceBlock(t, aggregate, "worker")
	if want := "    # Not connected to queue, held back: the catalog holds the Component " + held +
		" its draft takes.\n"; !strings.HasPrefix(block, want) {
		t.Errorf("the worker's instance does not open naming the held-back queue %q:\n%s", want, block)
	}
	if strings.Contains(block, "uses:") {
		t.Errorf("the worker's instance is connected to the held-back queue:\n%s", block)
	}
	exists := onlyCode(t, out.Diagnostics, CodeComponentExists, 1)
	mentions(t, "the warning", exists[0].Message, "queue", held)
	warnsAbout(t, out.Diagnostics, CodeReferenceNotWired, "worker", "queue", "held back")
	if release := fileOf(t, out, "release.yaml"); strings.Contains(release, "\n    queue:\n") {
		t.Errorf("the Release writes an entry for the held-back queue:\n%s", release)
	}
}
