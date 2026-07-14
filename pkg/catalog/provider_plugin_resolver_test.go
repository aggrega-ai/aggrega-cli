// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// providerPlugin renders a minimal schema-conforming pluginType: provider Plugin
// entity claiming the given provider name in its targets.
func providerPlugin(name, version, provider string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: ` + name + `
  version: ` + version + `
spec:
  pluginType: provider
  abiVersion: v1alpha1
  capabilities:
    - ` + provider + `
  exports:
    - name: render_node
`
}

// TestProviderPluginByNameResolvesShippedProviders proves the provider-name →
// provider-Plugin resolution over a realistic catalog: each of the three shipped
// provider names resolves to the Plugin that lists it in targets, a
// name no plugin serves misses, and a non-provider (frp_step) plugin that happens
// to list a provider name is NOT treated as a render engine.
func TestProviderPluginByNameResolvesShippedProviders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer.yaml", providerPlugin("helm-renderer", "1.0.0", "helm"))
	writeYAMLToCatalog(t, dir, "plugins/compose-renderer.yaml", providerPlugin("compose-renderer", "1.0.0", "compose"))
	writeYAMLToCatalog(t, dir, "plugins/helm-chart-renderer.yaml", providerPlugin("helm-chart-renderer", "1.0.0", "helm-chart"))
	// An frp_step plugin that lists "helm" in targets — a DIFFERENT
	// meaning (the provider a pipeline step renders alongside), not a render-engine
	// claim. It must be excluded: if it counted, "helm" would be claimed by two
	// distinct plugin names and resolve to (_, false).
	writeYAMLToCatalog(t, dir, "plugins/helm-pipeline-step.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-pipeline-step
  version: 1.0.0
spec:
  pluginType: frp_step
  abiVersion: v1alpha1
  capabilities:
    - helm
`)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lookup := cat.CatalogLookup()
	if lookup.ProviderPlugin == nil {
		t.Fatal("CatalogLookup.ProviderPlugin is nil; the accessor must be wired")
	}

	cases := []struct {
		provider string
		wantName string
	}{
		{"helm", "helm-renderer"},
		{"compose", "compose-renderer"},
		{"helm-chart", "helm-chart-renderer"},
	}
	for _, tc := range cases {
		// Resolve through both the direct method and the CatalogLookup accessor;
		// they must agree.
		got, ok, err := cat.ProviderPluginByName(tc.provider)
		if err != nil {
			t.Errorf("ProviderPluginByName(%q): unexpected error %v", tc.provider, err)
			continue
		}
		if !ok {
			t.Errorf("ProviderPluginByName(%q): not found; want %q", tc.provider, tc.wantName)
			continue
		}
		if got.Metadata.Name != tc.wantName {
			t.Errorf("ProviderPluginByName(%q): got plugin %q; want %q (frp_step must not shadow the provider engine)",
				tc.provider, got.Metadata.Name, tc.wantName)
		}
		viaLookup, ok, err := lookup.ProviderPlugin(tc.provider)
		if err != nil || !ok || viaLookup.Metadata.Name != tc.wantName {
			t.Errorf("CatalogLookup.ProviderPlugin(%q): got (%q, %v, %v); want (%q, true, nil)",
				tc.provider, viaLookup.Metadata.Name, ok, err, tc.wantName)
		}
	}

	if got, ok, err := cat.ProviderPluginByName("terraform"); ok || err != nil {
		t.Errorf("ProviderPluginByName(\"terraform\"): got (%q, %v, %v); want (_, false, nil) for a provider no plugin serves", got.Metadata.Name, ok, err)
	}
}

// TestProviderPluginByNameSelectsHighestVersion proves several versions of ONE
// provider plugin claiming the same provider are not a conflict — the highest
// satisfying version wins, matching the three-form version select the other
// by-name lookups use.
func TestProviderPluginByNameSelectsHighestVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-100.yaml", providerPlugin("helm-renderer", "1.0.0", "helm"))
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-110.yaml", providerPlugin("helm-renderer", "1.1.0", "helm"))

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	got, ok, err := cat.ProviderPluginByName("helm")
	if err != nil {
		t.Fatalf("ProviderPluginByName(\"helm\"): unexpected error %v", err)
	}
	if !ok {
		t.Fatal("ProviderPluginByName(\"helm\"): not found; want the highest version")
	}
	if got.Metadata.Version != "1.1.0" {
		t.Errorf("ProviderPluginByName(\"helm\"): got version %q; want the highest 1.1.0", got.Metadata.Version)
	}
}

// TestProviderPluginAmbiguityIsPointOfUse proves two DISTINCT provider plugins
// both serving the same target do NOT halt the catalog load — a federated catalog
// is legal — and instead halt loudly at resolution (the point of use) when no
// plugins.providers entry disambiguates, naming both candidates.
func TestProviderPluginAmbiguityIsPointOfUse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer.yaml", providerPlugin("helm-renderer", "1.0.0", "helm"))
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-alt.yaml", providerPlugin("helm-renderer-alt", "1.0.0", "helm"))

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: a federated catalog (two plugins serving \"helm\") must load without halting; got: %v", err)
	}

	// No binding → loud ambiguity halt naming both candidates at resolution.
	_, ok, rerr := cat.ProviderPluginByName("helm")
	if ok || rerr == nil {
		t.Fatalf("ProviderPluginByName(\"helm\") over an ambiguous catalog: got (ok=%v, err=%v); want (false, ErrAmbiguousProviderPlugin)", ok, rerr)
	}
	if !errors.Is(rerr, catalog.ErrAmbiguousProviderPlugin) {
		t.Errorf("ambiguity error must be ErrAmbiguousProviderPlugin; got: %v", rerr)
	}
	if !strings.Contains(rerr.Error(), "helm-renderer") || !strings.Contains(rerr.Error(), "helm-renderer-alt") {
		t.Errorf("ambiguity error must name both candidates; got:\n%s", rerr.Error())
	}
}

// TestProviderPluginBindingDisambiguates proves a plugins.providers entry selects
// which of two competing plugins renders a target: the binding filters the
// candidate set to the plugin whose spec.plugin matches the bound OCI identity,
// and the other publisher's plugin is excluded.
func TestProviderPluginBindingDisambiguates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer.yaml", providerPlugin("helm-renderer", "1.0.0", "helm"))
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-alt.yaml", providerPlugin("helm-renderer-alt", "1.0.0", "helm"))

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	bindings := map[string]string{"helm": "helm-renderer-alt"}
	got, ok, rerr := cat.ProviderPluginForTarget("helm", bindings)
	if rerr != nil || !ok {
		t.Fatalf("ProviderPluginForTarget with a binding: got (ok=%v, err=%v); want the bound plugin", ok, rerr)
	}
	if got.Metadata.Name != "helm-renderer-alt" {
		t.Errorf("binding must select helm-renderer-alt; got %q", got.Metadata.Name)
	}

	// A binding naming a plugin no candidate matches is a loud, unfulfillable halt.
	_, ok, rerr = cat.ProviderPluginForTarget("helm", map[string]string{"helm": "nonexistent"})
	if ok || !errors.Is(rerr, catalog.ErrProviderBindingUnsatisfied) {
		t.Errorf("an unsatisfiable binding must return ErrProviderBindingUnsatisfied; got (ok=%v, err=%v)", ok, rerr)
	}
}

// TestProviderPluginBindingPreservesVersionCoexistence proves the binding selects
// the plugin repository per target while the version is chosen per-recipe by the
// target pin — so two recipes pinning different versions of one bound repository
// each resolve to their own version.
func TestProviderPluginBindingPreservesVersionCoexistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-100.yaml", providerPlugin("helm-renderer", "1.0.0", "helm"))
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-200.yaml", providerPlugin("helm-renderer", "2.0.0", "helm"))
	// A second publisher whose presence makes the binding necessary.
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-alt.yaml", providerPlugin("helm-renderer-alt", "1.0.0", "helm"))

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	bindings := map[string]string{"helm": "helm-renderer"}

	v1, ok, rerr := cat.ProviderPluginForTarget("helm:1.0.0", bindings)
	if rerr != nil || !ok || v1.Metadata.Version != "1.0.0" {
		t.Errorf("helm:1.0.0 under the binding: got (ver=%q, ok=%v, err=%v); want 1.0.0", v1.Metadata.Version, ok, rerr)
	}
	v2, ok, rerr := cat.ProviderPluginForTarget("helm:2.0.0", bindings)
	if rerr != nil || !ok || v2.Metadata.Version != "2.0.0" {
		t.Errorf("helm:2.0.0 under the binding: got (ver=%q, ok=%v, err=%v); want 2.0.0", v2.Metadata.Version, ok, rerr)
	}
}
