// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
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
  plugin: "oci://hub.aggrega.ai/plugins/core/` + name + `"
  pluginType: provider
  abiVersion: v1alpha1
  targets:
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
  plugin: "oci://hub.aggrega.ai/plugins/core/helm-pipeline-step"
  pluginType: frp_step
  abiVersion: v1alpha1
  targets:
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

// TestProviderPluginConflictHaltsAtLoad proves two DISTINCT provider plugins both
// claiming the same provider halt the catalog load loudly, rather than resolving
// to an arbitrary engine at render time.
func TestProviderPluginConflictHaltsAtLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer.yaml", providerPlugin("helm-renderer", "1.0.0", "helm"))
	writeYAMLToCatalog(t, dir, "plugins/helm-renderer-alt.yaml", providerPlugin("helm-renderer-alt", "1.0.0", "helm"))

	_, err := catalog.LoadAll("", dir)
	if err == nil {
		t.Fatal("LoadAll: expected a conflict halt when two distinct provider plugins claim \"helm\"; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "provider \"helm\" is claimed by more than one provider plugin") {
		t.Errorf("conflict error message missing the actionable claim; got:\n%s", msg)
	}
	// Both offending plugin names appear, sorted, so the operator knows what to fix.
	if !strings.Contains(msg, "helm-renderer") || !strings.Contains(msg, "helm-renderer-alt") {
		t.Errorf("conflict error message must name both offending plugins; got:\n%s", msg)
	}
}
