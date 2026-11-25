// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
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

// TestModuleSharingAResourcesComponentIsHeldBack holds back a module no row
// maps whose drafted Component would take the name a resource of the same
// import drafts: one Component takes one drafted Recipe, which cannot both
// declare the resource and source the module whole. The resource is drafted,
// the module is neither drafted nor referenced, and the instance reading the
// module names it in a comment.
func TestModuleSharingAResourcesComponentIsHeldBack(t *testing.T) {
	var handed []schemadraft.WrappedModule
	out := Import(toyInput(t, "toy", "", []string{"collision.json"}), toyFormat(&handed), toyParse)
	noHalt(t, out.Diagnostics)
	name := schemadraft.NameFor(toyInput(t, "toy", "", nil), toyFormat(nil).Draft, toySearch)
	if len(handed) != 0 {
		t.Errorf("the module writer is handed %+v, want nothing for the held-back module", handed)
	}
	want := []string{"aggregate.yaml", "release.yaml", "manifest.yaml",
		"catalog/component/" + name + "/0.1.0/component.yaml", "catalog/recipe/toy/toy-search-services/0.1.0/recipe.yaml"}
	var got []string
	for _, p := range filePaths(out) {
		if !strings.Contains(p, "toy-compute-workers") {
			got = append(got, p)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the import writes %q, want the resource's Component and Recipe once: %q", filePaths(out), want)
	}
	if recipe := fileOf(t, out, "catalog/recipe/toy/toy-search-services/0.1.0/recipe.yaml"); strings.Contains(recipe,
		"source:") {
		t.Errorf("the drafted Recipe sources the module:\n%s", recipe)
	}
	i := slices.Index(decisionIDs(out), "searchModule")
	if i < 0 {
		t.Fatalf("no decision names searchModule: %q", decisionIDs(out))
	}
	mentions(t, "the decision on searchModule", out.Decisions[i].Rationale, "held back", name, "search",
		"one drafted Recipe")
	mentions(t, "the swap hint", out.Decisions[i].SwapHint, "another name", toyModuleURL)
	if strings.Contains(out.Decisions[i].SwapHint, "mapping_overrides") {
		t.Errorf("the swap hint offers mapping_overrides, which a re-import cannot apply to this import's draft: %s",
			out.Decisions[i].SwapHint)
	}

	exists := onlyCode(t, out.Diagnostics, CodeComponentExists, 1)
	if exists[0].Severity != aggrega.SeverityWarn {
		t.Errorf("component-exists is a %s, want a warning", exists[0].Severity)
	}
	mentions(t, "the warning", exists[0].Message, "searchModule", name, "search", "one drafted Recipe", "another name")
	if strings.Contains(exists[0].Message, "mapping_overrides") || strings.Contains(exists[0].Message, "catalog holds") {
		t.Errorf("the warning offers a remedy that does not fit: %s", exists[0].Message)
	}

	aggregate := fileOf(t, out, "aggregate.yaml")
	if strings.Contains(aggregate, "\n    searchmodule:\n") {
		t.Errorf("the Aggregate writes an instance of the held-back module:\n%s", aggregate)
	}
	block := instanceBlock(t, aggregate, "worker")
	if want := "    # Not connected to searchModule, held back: the Component " + name + " its draft takes is the " +
		"one this import drafts from a resource.\n"; !strings.HasPrefix(block, want) {
		t.Errorf("the worker's instance does not open naming the held-back module %q:\n%s", want, block)
	}
	warnsAbout(t, out.Diagnostics, CodeReferenceNotWired, "worker", "searchModule", "held back")
}

// TestModuleParameterFedBySecretIsLeftOut leaves out of a wrapped module's
// Recipe each parameter a secret feeds — one the format's secret rule names,
// one a secure parameter feeds, one a credential feeds — and warns of each,
// naming the parameter and the module: no value of it is written anywhere.
func TestModuleParameterFedBySecretIsLeftOut(t *testing.T) {
	var handed []schemadraft.WrappedModule
	f := toyFormat(&handed)
	f.Draft.Rules.Secret = func(_ *schemadraft.Type, p *schemadraft.Property) bool {
		return strings.Contains(strings.ToLower(p.Name), "password")
	}
	out := Import(toyInput(t, "toy", "", []string{"secret.json"}), f, toyParse)
	noHalt(t, out.Diagnostics)
	if len(handed) != 1 {
		t.Fatalf("the module writer is handed %d modules, want one", len(handed))
	}
	if got, want := handedInputs(handed[0]), []string{"name override"}; !slices.Equal(got, want) {
		t.Errorf("the module's inputs are %q, want %q", got, want)
	}
	for _, path := range filePaths(out) {
		text := fileOf(t, out, path)
		for _, leaked := range []string{"hunter2-module", "adminPassword", "apiKey", "storageKey"} {
			if strings.Contains(text, leaked) {
				t.Errorf("%s writes %s:\n%s", path, leaked, text)
			}
		}
	}
	notCarried := onlyCode(t, out.Diagnostics, CodeValueNotCarried, 3)
	for i, param := range []string{"adminPassword", "apiKey", "storageKey"} {
		mentions(t, "the warning", notCarried[i].Message, "search", param, toyModuleURL, "secret", "bind "+param)
		if notCarried[i].SourceRef == "" || strings.Contains(notCarried[i].Message, "hunter2") {
			t.Errorf("the warning %+v points at no node or quotes the value", notCarried[i])
		}
	}
	flow := onlyCode(t, out.Diagnostics, CodeCredentialFlow, 1)
	mentions(t, "the credential warning", flow[0].Message, "search", "storageKey", "bind")
	if strings.Contains(flow[0].Message, "reads the property as a secret") {
		t.Errorf("the credential warning says the module's Recipe reads the parameter: %s", flow[0].Message)
	}
}

// TestModuleReadingInsideAParameterIsNotWired delivers a neighbour's value
// into a module's parameter only where the parameter is the value whole: a
// value read inside a parameter is a reference no inject carries, and the
// module writer binds no parameter for it.
func TestModuleReadingInsideAParameterIsNotWired(t *testing.T) {
	var handed []schemadraft.WrappedModule
	out := Import(toyInput(t, "toy", "", []string{"nested.json"}), toyFormat(&handed), toyParse)
	noHalt(t, out.Diagnostics)
	if len(handed) != 1 {
		t.Fatalf("the module writer is handed %d modules, want one", len(handed))
	}
	if got, want := handedInputs(handed[0]), []string{"identity override", "hostName inject host_name"}; !slices.Equal(got, want) {
		t.Errorf("the module's inputs are %q, want %q", got, want)
	}
	warned := onlyCode(t, out.Diagnostics, CodeReferenceNotWired, 1)
	mentions(t, "the warning", warned[0].Message, "search", "identity.ids.0", "identity", "whole")
	block := instanceBlock(t, fileOf(t, out, "aggregate.yaml"), "search")
	if strings.Contains(block, "identity_ids_0") || !strings.Contains(block, `- in: "host_name"`) {
		t.Errorf("the module's instance is delivered\n%s\nwant host_name alone", block)
	}
	if !strings.Contains(block, "# Not wired: search reads db's host at identity.ids.0") {
		t.Errorf("the module's connection does not say why the value inside identity is not wired:\n%s", block)
	}
}

// TestModuleTheFormatDoesNotLocateIsNotCarried classes a module no row maps
// whose source the format does not locate not carried, warns
// linked-source-not-read naming it and its module's prefix, drafts nothing for
// it and drafts the rest.
func TestModuleTheFormatDoesNotLocateIsNotCarried(t *testing.T) {
	var handed []schemadraft.WrappedModule
	out := Import(toyInput(t, "toy", "", []string{"unlocated.json"}), toyFormat(&handed), toyParse)
	noHalt(t, out.Diagnostics)
	if len(handed) != 0 {
		t.Errorf("the module writer is handed %+v, want nothing", handed)
	}
	for _, p := range filePaths(out) {
		if strings.Contains(p, "toy-search-services") {
			t.Errorf("the import drafts %s for the module it does not locate", p)
		}
	}
	fileOf(t, out, "catalog/recipe/toy/toy-compute-workers/0.1.0/recipe.yaml")
	unread := onlyCode(t, out.Diagnostics, CodeLinkedSourceNotRead, 1)
	if unread[0].Severity != aggrega.SeverityWarn || unread[0].SourceRef == "" {
		t.Errorf("linked-source-not-read %+v, want a warning pointing at the module", unread[0])
	}
	mentions(t, "the warning", unread[0].Message, "search", toySearch, "toy.search", "not carried", "mapping_overrides")
	i := slices.Index(decisionIDs(out), "search")
	if i < 0 {
		t.Fatalf("no decision names search: %q", decisionIDs(out))
	}
	mentions(t, "the decision on search", out.Decisions[i].Rationale, "not carried", "toy.search")
	if strings.Contains(fileOf(t, out, "aggregate.yaml"), "\n    search:\n") {
		t.Errorf("the Aggregate writes an instance of the module it does not locate")
	}
	warnsAbout(t, out.Diagnostics, CodeReferenceNotWired, "worker", "search")
}

// TestModuleWithoutVersionWarnsUnpinned drafts a module whose version the
// source does not state with the Recipe's source version left empty, warning
// module-version-unpinned on the module.
func TestModuleWithoutVersionWarnsUnpinned(t *testing.T) {
	out := Import(toyInput(t, "toy", "", []string{"unpinned.json"}), toyFormat(nil), toyParse)
	noHalt(t, out.Diagnostics)
	unpinned := onlyCode(t, out.Diagnostics, CodeModuleVersionUnpinned, 1)
	if unpinned[0].Severity != aggrega.SeverityWarn || unpinned[0].SourceRef != "search" {
		t.Errorf("module-version-unpinned %+v, want a warning on search", unpinned[0])
	}
	mentions(t, "the warning", unpinned[0].Message, toyModuleURL, "spec.source.version")
	recipe := fileOf(t, out, "catalog/recipe/toy/toy-search-services/0.1.0/recipe.yaml")
	if !strings.Contains(recipe, "    url: "+toyModuleURL+"\n    version: \"\"\n") {
		t.Errorf("the Recipe does not leave its source version empty:\n%s", recipe)
	}
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
