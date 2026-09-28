// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// providerPlugin renders a minimal schema-conforming pluginType: provider Plugin
// entity claiming the given provider name in its targets.
func providerPlugin(name, version, provider string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: ` + name + `
  pluginType: provider
  version: ` + version + `
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - ` + provider + `
  exports:
    - name: render_node
`
}

// TestProviderPluginByNameResolvesShippedProviders proves the provider-name →
// provider-Plugin resolution over a realistic catalog: each of the three
// provider names resolves to the Plugin that lists it in targets, a name no
// plugin serves misses, and a non-provider (pipeline-step) plugin that happens to
// list a provider name is NOT treated as a render engine. The fictional provider
// names deliberately do NOT match any core provider the binary embeds, so this
// exercises the catalog resolution algorithm in isolation from the embedded-wins
// overlay a loaded catalog also applies.
func TestProviderPluginByNameResolvesShippedProviders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/acme-renderer.yaml", providerPlugin("acme-renderer", "1.0.0", "acme"))
	writeYAMLToCatalog(t, dir, "plugins/widget-renderer.yaml", providerPlugin("widget-renderer", "1.0.0", "widget"))
	writeYAMLToCatalog(t, dir, "plugins/gadget-renderer.yaml", providerPlugin("gadget-renderer", "1.0.0", "gadget"))
	// A pipeline-step plugin that lists "acme" in targets — a DIFFERENT
	// meaning (the provider a pipeline step renders alongside), not a render-engine
	// claim. It must be excluded: if it counted, "acme" would be claimed by two
	// distinct plugin names and resolve to (_, false).
	writeYAMLToCatalog(t, dir, "plugins/acme-pipeline-step.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: acme-pipeline-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1alpha1
  pipelineStep:
    capabilities:
      - acme
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
		{"acme", "acme-renderer"},
		{"widget", "widget-renderer"},
		{"gadget", "gadget-renderer"},
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
			t.Errorf("ProviderPluginByName(%q): got plugin %q; want %q (pipeline-step must not shadow the provider engine)",
				tc.provider, got.Metadata.Name, tc.wantName)
		}
		viaLookup, ok, err := lookup.ProviderPlugin(tc.provider)
		if err != nil || !ok || viaLookup.Metadata.Name != tc.wantName {
			t.Errorf("CatalogLookup.ProviderPlugin(%q): got (%q, %v, %v); want (%q, true, nil)",
				tc.provider, viaLookup.Metadata.Name, ok, err, tc.wantName)
		}
	}

	if got, ok, err := cat.ProviderPluginByName("nonesuch"); ok || err != nil {
		t.Errorf("ProviderPluginByName(\"nonesuch\"): got (%q, %v, %v); want (_, false, nil) for a provider no plugin serves", got.Metadata.Name, ok, err)
	}
}

// TestProviderPluginByNameSelectsHighestVersion proves several versions of ONE
// provider plugin claiming the same provider are not a conflict — the highest
// satisfying version wins, matching the three-form version select the other
// by-name lookups use. The fictional provider name does not match any core
// provider the binary embeds, so this exercises the version-select in isolation
// from the embedded-wins overlay.
func TestProviderPluginByNameSelectsHighestVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/acme-renderer-100.yaml", providerPlugin("acme-renderer", "1.0.0", "acme"))
	writeYAMLToCatalog(t, dir, "plugins/acme-renderer-110.yaml", providerPlugin("acme-renderer", "1.1.0", "acme"))

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	got, ok, err := cat.ProviderPluginByName("acme")
	if err != nil {
		t.Fatalf("ProviderPluginByName(\"acme\"): unexpected error %v", err)
	}
	if !ok {
		t.Fatal("ProviderPluginByName(\"acme\"): not found; want the highest version")
	}
	if got.Metadata.Version != "1.1.0" {
		t.Errorf("ProviderPluginByName(\"acme\"): got version %q; want the highest 1.1.0", got.Metadata.Version)
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
	if !errors.Is(rerr, catalog.ErrAmbiguousProviderPlugin()) {
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

	bindings := map[string]string{"helm": "provider/helm-renderer-alt"}
	got, ok, rerr := cat.ProviderPluginForTarget("helm", bindings)
	if rerr != nil || !ok {
		t.Fatalf("ProviderPluginForTarget with a binding: got (ok=%v, err=%v); want the bound plugin", ok, rerr)
	}
	if got.Metadata.Name != "helm-renderer-alt" {
		t.Errorf("binding must select helm-renderer-alt; got %q", got.Metadata.Name)
	}

	// A binding naming a plugin no candidate matches is a loud, unfulfillable halt.
	_, ok, rerr = cat.ProviderPluginForTarget("helm", map[string]string{"helm": "nonexistent"})
	if ok || !errors.Is(rerr, catalog.ErrProviderBindingUnsatisfied()) {
		t.Errorf("an unsatisfiable binding must return ErrProviderBindingUnsatisfied; got (ok=%v, err=%v)", ok, rerr)
	}
}

// TestProviderPluginVersionPinIsUnsatisfiable proves the version-pin halt at the
// resolution that raises it, rather than through a caller that hands the
// condition back ready-made. A target pinned to a version the catalog cannot
// serve is an explicit request the compiler cannot honor, so it halts loudly
// instead of rendering through whatever version happens to serve the target.
//
// Both ways a pin goes unsatisfied are exercised, because they leave resolution
// at different points and neither is reached by the other's input: a target no
// plugin serves at all, and a target served at a version outside the pin (in
// both the exact and the caret spelling). A satisfied pin and the pinless form
// of the same missing target are asserted beside them, so each halt is
// attributable to the pin rather than to a fixture that fails every way.
//
// The fictional provider names deliberately do NOT match any core provider the
// binary embeds, so the pin is judged against this catalog's own plugins rather
// than against the embedded-wins overlay a loaded catalog also applies.
func TestProviderPluginVersionPinIsUnsatisfiable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/acme-renderer.yaml", providerPlugin("acme-renderer", "1.0.0", "acme"))

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// A pin the catalog satisfies resolves. Asserted first, because it is what
	// makes each halt below attributable to the pin it names.
	if p, ok, rerr := cat.ProviderPluginForTarget("acme:1.0.0", nil); rerr != nil || !ok || p.Metadata.Version != "1.0.0" {
		t.Errorf("acme:1.0.0 must resolve to the catalog's 1.0.0 plugin; got (ver=%q, ok=%v, err=%v)",
			p.Metadata.Version, ok, rerr)
	}

	// A plugin serves the target at a version the exact pin excludes.
	got, ok, rerr := cat.ProviderPluginForTarget("acme:2.0.0", nil)
	if ok || !errors.Is(rerr, catalog.ErrProviderVersionUnsatisfiable()) {
		t.Errorf("acme:2.0.0 against a catalog holding only 1.0.0 must return ErrProviderVersionUnsatisfiable; got (plugin=%q, ok=%v, err=%v)",
			got.Metadata.Name, ok, rerr)
	}
	if rerr != nil && !strings.Contains(rerr.Error(), "2.0.0") {
		t.Errorf("the halt must name the pin it could not satisfy; got:\n%s", rerr.Error())
	}

	// A caret pin no served version falls inside halts the same way.
	if _, ok, rerr := cat.ProviderPluginForTarget("acme:^2.0.0", nil); ok || !errors.Is(rerr, catalog.ErrProviderVersionUnsatisfiable()) {
		t.Errorf("acme:^2.0.0 against a catalog holding only 1.0.0 must return ErrProviderVersionUnsatisfiable; got (ok=%v, err=%v)",
			ok, rerr)
	}

	// No plugin serves the target at all, and a pin is present. The pin is the
	// explicit request, so this halts rather than reporting the bare miss.
	if _, ok, rerr := cat.ProviderPluginForTarget("nonesuch:1.0.0", nil); ok || !errors.Is(rerr, catalog.ErrProviderVersionUnsatisfiable()) {
		t.Errorf("a pinned target no plugin serves must return ErrProviderVersionUnsatisfiable; got (ok=%v, err=%v)",
			ok, rerr)
	}

	// The pinless form of that same target is the bare miss the caller resolves,
	// which is what distinguishes the halt above from a plain not-found.
	if _, ok, rerr := cat.ProviderPluginForTarget("nonesuch", nil); ok || rerr != nil {
		t.Errorf("an unpinned target no plugin serves is a bare miss; got (ok=%v, err=%v)", ok, rerr)
	}
}

// TestProviderPluginMalformedPinIsReportedAsMalformed proves provider selection
// reads a Recipe's capability pin through the one pin parser: a pin that is none
// of the forms a pin takes is reported as malformed, both where a plugin serves the
// capability and where none does, while a well-formed pin no served version
// satisfies keeps the provider-version condition. The satisfied pin is asserted
// first, so each refusal is attributable to the pin it names.
func TestProviderPluginMalformedPinIsReportedAsMalformed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "plugins/acme-renderer.yaml", providerPlugin("acme-renderer", "1.0.0", "acme"))

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if p, ok, rerr := cat.ProviderPluginForTarget("acme:1.0.0", nil); rerr != nil || !ok || p.Metadata.Version != "1.0.0" {
		t.Fatalf("control: acme:1.0.0 must resolve; got (ver=%q, ok=%v, err=%v)", p.Metadata.Version, ok, rerr)
	}

	for _, tc := range []struct {
		name          string
		ref           string
		wantMalformed bool
	}{
		{"a served capability with a caret pin whose range does not parse", "acme:^garbage", true},
		{"a served capability with an exact pin that is not a semantic version", "acme:1.x", true},
		{"a capability no plugin serves, pinned malformed", "nonesuch:^garbage", true},
		{"a served capability with a well-formed pin no version satisfies", "acme:2.0.0", false},
		{"a capability no plugin serves, pinned well formed", "nonesuch:1.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ok, rerr := cat.ProviderPluginForTarget(tc.ref, nil)
			if ok {
				t.Fatalf("%q resolved; want a refusal", tc.ref)
			}
			if tc.wantMalformed {
				if !errors.Is(rerr, types.ErrMalformedVersionPin()) {
					t.Errorf("%q: want ErrMalformedVersionPin, got %v", tc.ref, rerr)
				}
				if errors.Is(rerr, catalog.ErrProviderVersionUnsatisfiable()) {
					t.Errorf("%q: a malformed pin must not report as an unsatisfiable provider pin: %v", tc.ref, rerr)
				}
				if rerr != nil && !strings.Contains(rerr.Error(), `reference "`+tc.ref+`"`) {
					t.Errorf("%q: the refusal must name the capability reference as written; got %v", tc.ref, rerr)
				}
				return
			}
			if !errors.Is(rerr, catalog.ErrProviderVersionUnsatisfiable()) {
				t.Errorf("%q: want ErrProviderVersionUnsatisfiable, got %v", tc.ref, rerr)
			}
			if errors.Is(rerr, types.ErrMalformedVersionPin()) {
				t.Errorf("%q: a well-formed pin was reported as malformed: %v", tc.ref, rerr)
			}
		})
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
	bindings := map[string]string{"helm": "provider/helm-renderer"}

	v1, ok, rerr := cat.ProviderPluginForTarget("helm:1.0.0", bindings)
	if rerr != nil || !ok || v1.Metadata.Version != "1.0.0" {
		t.Errorf("helm:1.0.0 under the binding: got (ver=%q, ok=%v, err=%v); want 1.0.0", v1.Metadata.Version, ok, rerr)
	}
	v2, ok, rerr := cat.ProviderPluginForTarget("helm:2.0.0", bindings)
	if rerr != nil || !ok || v2.Metadata.Version != "2.0.0" {
		t.Errorf("helm:2.0.0 under the binding: got (ver=%q, ok=%v, err=%v); want 2.0.0", v2.Metadata.Version, ok, rerr)
	}
}
