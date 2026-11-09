// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// providerPluginServing renders a schema-conforming pluginType: provider Plugin
// entity serving every capability given, in the order given.
func providerPluginServing(name, version string, capabilities ...string) string {
	doc := `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: ` + name + `
  pluginType: provider
  version: ` + version + `
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
`
	for _, c := range capabilities {
		doc += "      - " + c + "\n"
	}
	return doc + "  exports:\n    - name: render_node\n"
}

// bindingFaultCatalog loads a catalog of fictional provider plugins — names no
// core provider the binary embeds carries, so the judgement is exercised over
// exactly this set — plus a pipeline-step plugin, which lists a provider
// capability in a different sense and must never count as serving it.
func bindingFaultCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/acme-renderer.yaml", providerPluginServing("acme-renderer", "1.0.0", "acme-chart", "acme"))
	writeYAMLToCatalog(t, dir, "plugins/widget-renderer.yaml", providerPluginServing("widget-renderer", "1.0.0", "widget"))
	writeYAMLToCatalog(t, dir, "plugins/shared-a.yaml", providerPluginServing("shared-a", "1.0.0", "shared"))
	writeYAMLToCatalog(t, dir, "plugins/shared-b.yaml", providerPluginServing("shared-b", "1.0.0", "shared"))
	writeYAMLToCatalog(t, dir, "plugins/acme-step.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: acme-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1alpha1
  pipelineStep:
    capabilities:
      - gizmo
`)
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return cat
}

// TestProviderBindingFaultJudgesABindingByItsShape proves the judgement a build
// and a Release-scoped validation ask of every provider binding, whether or not a
// Recipe consults it, through the catalog lookup accessor those runs read.
//
// A binding naming a plugin the catalog holds halts when that plugin does not
// serve the binding's capability, and the halt names the binding, the plugin,
// what the plugin does serve and a spelling one edit away. A capability-only
// binding whose capability no provider plugin serves is an advisory. Each row is
// one fault, asserted against its condition's accessor, and each halting row
// asserts it is not the advisory (and the reverse), so a row cannot pass on the
// other judgement's condition. The controls are the legal bindings: served
// capabilities in both shapes, an ambiguously-served capability, a binding for a
// plugin the catalog does not hold, and a binding with no capability.
func TestProviderBindingFaultJudgesABindingByItsShape(t *testing.T) {
	t.Parallel()
	lookup := bindingFaultCatalog(t).CatalogLookup()
	if lookup.ProviderBindingFault == nil {
		t.Fatal("CatalogLookup.ProviderBindingFault is nil; the judgement must be wired")
	}

	const (
		halts    = "halts"
		advises  = "advises"
		accepted = "accepted"
	)
	cases := []struct {
		name        string
		capability  string
		uri         string
		outcome     string
		mustName    []string
		mustNotName []string
	}{
		{
			name:       "a uri binding whose plugin does not serve its misspelt capability halts",
			capability: "acem", uri: "provider/acme-renderer", outcome: halts,
			mustName: []string{`"acem"`, `"provider/acme-renderer"`, `serves "acme", "acme-chart"`, `"acem" is one edit from "acme"`},
		},
		{
			name:       "a uri binding pinning a version is judged on the plugin it names",
			capability: "acem", uri: "provider/acme-renderer:1.0.0", outcome: halts,
			mustName: []string{`"provider/acme-renderer"`, `serves "acme", "acme-chart"`},
		},
		{
			name:       "a uri binding naming a plugin that serves another capability halts without a near miss",
			capability: "widget", uri: "provider/acme-renderer", outcome: halts,
			mustName:    []string{`"widget"`, `serves "acme", "acme-chart"`},
			mustNotName: []string{"one edit"},
		},
		{
			name:       "a uri binding naming a plugin of another type halts",
			capability: "gizmo", uri: "pipeline-step/acme-step", outcome: halts,
			mustName: []string{`"pipeline-step/acme-step" serves no capability as a provider-rendering plugin`},
		},
		{
			name:       "a capability-only binding no provider serves advises, naming the near miss",
			capability: "widgte", outcome: advises,
			mustName: []string{`"widgte"`, `one edit from "widget"`},
		},
		{
			name:       "a capability-only binding a pipeline step lists, and no provider serves, advises",
			capability: "gizmo", outcome: advises,
			mustName:    []string{`"gizmo"`},
			mustNotName: []string{"one edit"},
		},
		{
			name:       "control: a uri binding whose plugin serves its capability is accepted",
			capability: "acme-chart", uri: "provider/acme-renderer", outcome: accepted,
		},
		{
			name:       "control: a capability-only binding a provider serves is accepted",
			capability: "widget", outcome: accepted,
		},
		{
			name:       "control: a capability-only binding for an ambiguously served capability is accepted",
			capability: "shared", outcome: accepted,
		},
		{
			name:       "control: a uri binding naming a plugin the catalog does not hold is not judged",
			capability: "acem", uri: "provider/not-in-this-catalog", outcome: accepted,
		},
		{
			name: "control: a binding naming no capability is not judged",
			uri:  "provider/acme-renderer", outcome: accepted,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := lookup.ProviderBindingFault(tc.capability, tc.uri)
			isHalt := errors.Is(err, catalog.ErrProviderBindingUnsatisfied())
			isAdvisory := errors.Is(err, catalog.ErrProviderBindingCapabilityUnserved())
			switch tc.outcome {
			case accepted:
				if err != nil {
					t.Fatalf("a legal binding {capability: %q, uri: %q} was reported: %v", tc.capability, tc.uri, err)
				}
				return
			case halts:
				if !isHalt || isAdvisory {
					t.Fatalf("want ErrProviderBindingUnsatisfied and not the advisory; got %v", err)
				}
			case advises:
				if !isAdvisory || isHalt {
					t.Fatalf("want ErrProviderBindingCapabilityUnserved and not the halt; got %v", err)
				}
			}
			for _, want := range tc.mustName {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the finding must carry %s; got: %v", want, err)
				}
			}
			for _, unwanted := range tc.mustNotName {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("the finding must not carry %q; got: %v", unwanted, err)
				}
			}
		})
	}
}

// TestABindingARecipeConsultsNamesWhatItsPluginServes proves the halt a Recipe's
// own resolution raises over an unsatisfiable binding names what the bound
// plugin does serve, the same clause the judgement of an unconsulted binding
// carries — one condition, one composition, whichever path reaches it. A binding
// naming a plugin the catalog does not hold keeps the sentence alone, since there
// is nothing true to add about what it serves.
func TestABindingARecipeConsultsNamesWhatItsPluginServes(t *testing.T) {
	t.Parallel()
	cat := bindingFaultCatalog(t)

	_, ok, err := cat.ProviderPluginForTarget("acme", map[string]string{"acme": "provider/widget-renderer"})
	if ok || !errors.Is(err, catalog.ErrProviderBindingUnsatisfied()) {
		t.Fatalf("want ErrProviderBindingUnsatisfied; got (ok=%v, err=%v)", ok, err)
	}
	if !strings.Contains(err.Error(), `"provider/widget-renderer" serves "widget"`) {
		t.Errorf("the halt must name what the bound plugin serves; got: %v", err)
	}

	_, ok, err = cat.ProviderPluginForTarget("acme", map[string]string{"acme": "provider/not-in-this-catalog"})
	if ok || !errors.Is(err, catalog.ErrProviderBindingUnsatisfied()) {
		t.Fatalf("want ErrProviderBindingUnsatisfied; got (ok=%v, err=%v)", ok, err)
	}
	if strings.Contains(err.Error(), `"provider/not-in-this-catalog" serves`) {
		t.Errorf("a binding naming a plugin the catalog does not hold must not be said to serve anything; got: %v", err)
	}
}
