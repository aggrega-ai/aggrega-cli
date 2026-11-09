// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"fmt"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// countingMissPuller records every fetch attempt and always reports a miss. It is
// the instrument for "resolved with no registry access": a resolution that consults
// it leaves a count behind, and one that does not cannot.
type countingMissPuller struct{ calls int }

func (p *countingMissPuller) Pull(_ context.Context, reference, tag string) (*oci.PullResult, error) {
	p.calls++
	return nil, fmt.Errorf("fetch %s:%s: %w", reference, tag, oci.ErrFetchNotFound())
}

func (p *countingMissPuller) RegistryPrefix() string { return "hub.aggrega.ai" }

// embeddedProviderIdentity returns the bare identity and metadata.version of one
// core provider this binary carries, read out of the binary itself. The version is
// DERIVED rather than written down: it moves with every compiler build, so a literal
// would be stale on the next one. A binary built without its core plugins carries
// none, and the test skips there rather than asserting over an empty set.
func embeddedProviderIdentity(t *testing.T) (identity, version string) {
	t.Helper()
	for _, uri := range embedded.EntityURIs() {
		raw, ok := embedded.LookupEntity(uri)
		if !ok {
			continue
		}
		var p types.Plugin
		if err := yaml.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.Metadata.PluginType != string(plugintype.Provider) || p.Metadata.Version == "" {
			continue
		}
		return p.Coordinate(), p.Metadata.Version
	}
	t.Skip("this binary carries no core provider entities")
	return "", ""
}

// embeddedOutputShapingIdentity returns the bare identity and metadata.version of one
// core OUTPUT-SHAPING plugin this binary carries — a governance guest, whose plugin
// rewrites the topology graph and therefore the rendered bytes. Like the provider
// helper the version is DERIVED, not written down. A binary built without its core
// plugins carries none, and the test skips there rather than asserting over an empty
// set.
func embeddedOutputShapingIdentity(t *testing.T) (identity, version string) {
	t.Helper()
	for _, uri := range embedded.EntityURIs() {
		raw, ok := embedded.LookupEntity(uri)
		if !ok {
			continue
		}
		var p types.Plugin
		if err := yaml.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.Metadata.PluginType != string(plugintype.Governance) || p.Metadata.Version == "" {
			continue
		}
		return p.Coordinate(), p.Metadata.Version
	}
	t.Skip("this binary carries no core governance entities")
	return "", ""
}

// TestClosureLookupEmbeddedEntityResolutionIsProviderOnly is the gate over the
// cross-package invariant the pull-side entity descent rests on: on the catalog-entity
// axis, the resolved-from-the-binary path is reported for a core PROVIDER and for
// nothing else. The descent's entity pin sites decline that path without inspecting any
// plugin type, so if this resolver ever widened, an output-shaping plugin's entity would
// silently stop being pinned.
//
// The needle is a REAL embedded governance identity at the REAL version the binary
// carries — the value that is one field away from resolving here, and therefore the one
// a too-broad condition accepts. Its own entity IS in the binary, so a NotFound is a
// statement about the plugin type rather than about availability, and the non-zero fetch
// count is the calibration: the reference genuinely took the registry cascade instead.
func TestClosureLookupEmbeddedEntityResolutionIsProviderOnly(t *testing.T) {
	t.Parallel()
	identity, version := embeddedOutputShapingIdentity(t)

	// The needle really is in the binary's entity set — otherwise this test would pass
	// for the wrong reason (nothing to resolve) and prove nothing about the narrowing.
	if _, ok := embedded.LookupEntity(identity); !ok {
		t.Fatalf("%q carries no embedded entity; the negative case would be vacuous", identity)
	}

	for _, form := range []struct{ name, uri string }{
		{"bare-identity", identity},
		{"exact-version", identity + ":" + version},
	} {
		form := form
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cat, err := catalog.LoadAll("", dir)
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			puller := &countingMissPuller{}
			lookup := cat.ClosureLookup(puller, dir, ".aggrega/cache")

			res, err := lookup.ResolveCatalogEntity(context.Background(), form.uri, "plugin")
			if err != nil {
				t.Fatalf("ResolveCatalogEntity(%q): %v", form.uri, err)
			}
			if res.ResolutionPath == closure.ResolutionPathEmbedded {
				t.Fatalf("%q resolved from the binary; only a core provider entity may, because the pull-side entity pin sites decline that path unconditionally", form.uri)
			}
			if !res.NotFound {
				t.Errorf("%q: got resolution %+v; want NotFound (the registry serves nothing here)", form.uri, res)
			}
		})
	}
}

// TestClosureLookupResolvesEmbeddedPluginEntityWithoutFetching proves a Plugin
// entity the binary carries resolves through the production lookup with NO registry
// access, on both the bare-identity and exact-version reference forms, and reports
// the resolved-from-the-binary path the pull-side pin sites read as "already
// present". Before this resolution existed such a reference fell through to a
// registry fetch for a version only the local binary knows, which no registry can
// serve.
func TestClosureLookupResolvesEmbeddedPluginEntityWithoutFetching(t *testing.T) {
	t.Parallel()
	identity, version := embeddedProviderIdentity(t)

	for _, form := range []struct{ name, uri string }{
		{"bare-identity", identity},
		{"exact-version", identity + ":" + version},
	} {
		form := form
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cat, err := catalog.LoadAll("", dir)
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			puller := &countingMissPuller{}
			lookup := cat.ClosureLookup(puller, dir, ".aggrega/cache")

			res, err := lookup.ResolveCatalogEntity(context.Background(), form.uri, "plugin")
			if err != nil {
				t.Fatalf("ResolveCatalogEntity(%q): %v", form.uri, err)
			}
			if res.NotFound {
				t.Fatalf("%q resolved NotFound; a plugin the binary carries must resolve, and a NotFound halts the provider socket", form.uri)
			}
			if res.ResolutionPath != closure.ResolutionPathEmbedded {
				t.Errorf("ResolutionPath: got %q; want %q", res.ResolutionPath, closure.ResolutionPathEmbedded)
			}
			if res.ResolvedVersion != version {
				t.Errorf("ResolvedVersion: got %q; want the version the binary carries, %q", res.ResolvedVersion, version)
			}
			if res.Digest == "" {
				t.Error("Digest: empty; want the content digest of the entity the binary carries")
			}
			if res.ManifestDigest != "" {
				t.Errorf("ManifestDigest: got %q; want empty (the binary carries no registry manifest to verify against)", res.ManifestDigest)
			}
			if puller.calls != 0 {
				t.Errorf("registry fetches: got %d; want 0 — the entity is in the binary", puller.calls)
			}
		})
	}
}

// TestClosureLookupEmbeddedResolutionIsExactAndPluginOnly proves the
// resolved-from-the-binary path does NOT swallow references it must leave alone, so
// the narrowing is measured rather than assumed.
//
// The negative cases are prefix-truncations of real values — a genuine identity and
// a genuine version with their last characters removed — because a truncated real
// value is exactly what a sloppy prefix or substring comparison wrongly ACCEPTS,
// while an obviously invented string is rejected by anything. Each must take the
// registry cascade instead, which the fetch count proves: a non-zero count is also
// this test's calibration, showing the instrument fires at all.
func TestClosureLookupEmbeddedResolutionIsExactAndPluginOnly(t *testing.T) {
	t.Parallel()
	identity, version := embeddedProviderIdentity(t)

	cases := []struct {
		name     string
		uri      string
		kindHint string
		// wantFetch is false only where the reference cannot reach a fetch for a
		// reason unrelated to this resolution: a bare reference carries no tag, and
		// the test puller offers no bare-identity capability, so the registry tier
		// declines it before any request. Those rows still prove the entity did not
		// resolve from the binary; they just cannot also calibrate the instrument.
		wantFetch bool
	}{
		{"truncated-identity-bare", identity[:len(identity)-1], "plugin", false},
		{"truncated-identity-versioned", identity[:len(identity)-1] + ":" + version, "plugin", true},
		{"truncated-version", identity + ":" + version[:len(version)-2], "plugin", true},
		{"another-version-of-the-same-identity", identity + ":0.0.1", "plugin", true},
		{"a-provider-the-binary-does-not-carry", "provider/render-custom:1.0.0", "plugin", true},
		{"not-plugin-kinded", identity + ":" + version, "component", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cat, err := catalog.LoadAll("", dir)
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			puller := &countingMissPuller{}
			lookup := cat.ClosureLookup(puller, dir, ".aggrega/cache")

			res, err := lookup.ResolveCatalogEntity(context.Background(), tc.uri, tc.kindHint)
			if err != nil {
				t.Fatalf("ResolveCatalogEntity(%q): %v", tc.uri, err)
			}
			if res.ResolutionPath == closure.ResolutionPathEmbedded {
				t.Fatalf("%q resolved from the binary; it must not — the binary carries no such entity", tc.uri)
			}
			if !res.NotFound {
				t.Errorf("%q: got resolution %+v; want NotFound (the registry serves nothing here)", tc.uri, res)
			}
			if tc.wantFetch && puller.calls == 0 {
				t.Errorf("%q: the registry was never consulted, so this case proves nothing about the narrowing", tc.uri)
			}
		})
	}
}

// TestClosureLookupLocalPluginEntityWinsOverTheBinary proves an entity authored in a
// local catalog still resolves from that catalog even when the binary carries the
// same coordinate: the local resolution is tried first, so introducing the
// resolved-from-the-binary path changed no entity that already resolved. Both are
// left unpinned by the pull-side descents, so the distinction is about which bytes
// a build reads, not about lockfile content.
func TestClosureLookupLocalPluginEntityWinsOverTheBinary(t *testing.T) {
	t.Parallel()
	identity, _ := embeddedProviderIdentity(t)

	// A local Plugin entity at the same coordinate the binary carries, at a version
	// the binary does not.
	name := identity[len("provider/"):]
	body := "apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: " + name +
		"\n  pluginType: provider\n  version: 9.9.9\nspec:\n  abiVersion: v1beta1\n"
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "wasm-plugins/local-provider.yaml", body)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	puller := &countingMissPuller{}
	lookup := cat.ClosureLookup(puller, dir, ".aggrega/cache")

	res, err := lookup.ResolveCatalogEntity(context.Background(), identity+":9.9.9", "plugin")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity: %v", err)
	}
	if res.ResolutionPath != "tier-1-catalog" {
		t.Errorf("ResolutionPath: got %q; want tier-1-catalog (the locally authored entity)", res.ResolutionPath)
	}
	if puller.calls != 0 {
		t.Errorf("registry fetches: got %d; want 0", puller.calls)
	}
}
