// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// testCorePluginVersion is a single synthetic, release-independent version used
// by ALL embedded provider resolution tests. The Plugin entity's version is an
// incidental mirror of the shipped hub entity version and is load-bearing to no
// resolution assertion, so a single synthetic value lets a release tag bump
// leave these tests untouched.
const testCorePluginVersion = "0.0.0-test"

// helmArtifactURI is the bare identity (<socket>/<name>) that is the helm
// provider plugin's identity — the coordinate the helm provider Plugin entity
// derives from its pluginType and name, the artifact reference
// resolver.EntityToArtifact derives, and the key the embed tier registers its
// bytes under. No digest
// literal is frozen here: the reference digest is derived from the embed tier's
// own bytes at test time (see embeddedReferenceDigest), so the resolution
// assertion is a self-consistency check against the bytes the binary actually
// ships, not against a hand-maintained constant.
const (
	helmArtifactURI    = "provider/helm"
	helmPluginEntity   = helmArtifactURI + ":" + testCorePluginVersion
	helmProviderMemPag = 4096
)

// embeddedReferenceDigest returns the content digest derived from the embed
// tier's own bytes for a plugin's bare identity — the same derivation the pull
// path uses to record a core plugin in the lockfile. A resolution test asserts
// the resolved bytes hash to this value, so the check tracks the shipped bytes
// with no frozen literal to re-pin when a guest is rebuilt.
func embeddedReferenceDigest(t *testing.T, uri string) string {
	t.Helper()
	b, ok := embedded.Lookup(uri)
	if !ok {
		t.Fatalf("embed tier has no bytes for %q; the registry key must match the plugin's bare identity", uri)
	}
	return digest.ContentDigest(b)
}

// helmChainCatalog builds an eager Catalog carrying the helm-renderer
// provider-rendering Plugin entity with the same field values the shipped hub YAML
// declares, so its CatalogLookup drives the real resolution chain against the
// artifact embedded in the binary. The Plugin serves the "helm" provider name
// through its targets list.
func helmChainCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Plugins: map[string]types.Plugin{
			helmPluginEntity: {
				APIVersion: "aggrega.ai/v1beta1",
				Kind:       "Plugin",
				Metadata:   types.PluginMetadata{Name: "helm", Version: testCorePluginVersion, PluginType: "provider"},
				Spec: types.PluginSpec{
					AbiVersion: "v1beta1",
					Provider:   types.PluginProvider{Capabilities: []string{"helm"}},
					Exports:    []types.PluginExport{{Name: "render_node"}},
					Runtime:    &types.PluginRuntime{MaxMemoryPages: helmProviderMemPag},
				},
			},
		},
	}
}

// entityFront builds the entity-fronted plugin Lookup over lookup's Plugin
// accessor and cascade — the same two-step (Plugin entity URI → bare-identity
// artifact reference → cascade) the build-load pipeline binds, so the test
// exercises the production resolution seam rather than a bypass.
func entityFront(lookup resolver.CatalogLookup, cascade pluginpath.Lookup) pluginpath.Lookup {
	return pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, *[]pluginpath.PluginCommandMirror, bool) {
		p, ok := lookup.Plugin(uri)
		if !ok {
			return "", nil, nil, nil, false
		}
		return p.ArtifactReference(), resolver.PluginContractClaim(p), resolver.PluginRuntimeLimits(p), resolver.PluginCommandsMirror(p), true
	}, cascade, pluginpath.SurfaceEntityOnly)
}

// TestHelmProviderEmbeddedResolutionChain proves the full provider→plugin→artifact
// resolution chain end-to-end through the real embed tier: the ProviderPlugin
// accessor on resolver.CatalogLookup (provider name → provider-rendering Plugin
// via targets), the Plugin accessor, the entity-fronted cascade, the
// pinned-digest integrity check (both arms), and the entity's runtime limits
// flowing to the loader spec. It does not re-prove render byte-identity — that is
// the A/B harness's job; this capstone is resolution + digest + limits.
func TestHelmProviderEmbeddedResolutionChain(t *testing.T) {
	cat := helmChainCatalog()
	lookup := cat.CatalogLookup()

	// Level 1: provider name → provider-rendering Plugin entity, via the
	// ProviderPlugin accessor wired into the eager CatalogLookup constructor.
	// Render dispatch resolves a provider name to the Plugin serving it through
	// the Plugin's targets list.
	if lookup.ProviderPlugin == nil {
		t.Fatal("CatalogLookup.ProviderPlugin accessor is nil; the eager constructor did not wire it")
	}
	pplug, ok, err := lookup.ProviderPlugin("helm")
	if err != nil {
		t.Fatalf("ProviderPlugin(\"helm\") errored: %v", err)
	}
	if !ok {
		t.Fatal("ProviderPlugin(\"helm\") not found")
	}
	if got := pplug.ArtifactReference(); got != helmArtifactURI {
		t.Fatalf("provider-plugin artifact reference = %q, want %q", got, helmArtifactURI)
	}

	// Level 2: Plugin-entity URI → Plugin entity → artifact URI. The entity-fronted
	// cascade below resolves the Plugin by this URI.
	if lookup.Plugin == nil {
		t.Fatal("CatalogLookup.Plugin accessor is nil")
	}
	plugin, ok := lookup.Plugin(helmPluginEntity)
	if !ok {
		t.Fatalf("Plugin(%q) not found", helmPluginEntity)
	}
	if got := plugin.ArtifactReference(); got != helmArtifactURI {
		t.Fatalf("plugin artifact reference = %q, want %q", got, helmArtifactURI)
	}

	t.Run("CleanResolveThroughEmbedTier", func(t *testing.T) {
		refDigest := embeddedReferenceDigest(t, helmArtifactURI)
		res := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureLockfile, pluginpath.PinMaps{Pins: map[string]string{helmArtifactURI: refDigest}})
		front := entityFront(lookup, res.Resolve)

		resolved, err := front(helmPluginEntity)
		if err != nil {
			t.Fatalf("clean resolve halted: %v", err)
		}
		if resolved.Tier != pluginpath.TierEmbed {
			t.Fatalf("resolved from tier %q, want %q — the artifact must resolve through the real embed tier",
				resolved.Tier, pluginpath.TierEmbed)
		}
		if got := digest.ContentDigest(resolved.Bytes); got != refDigest {
			t.Fatalf("resolved bytes digest = %q, want the embed tier's own reference digest %q", got, refDigest)
		}

		// Limits flow end-to-end from the Plugin entity's runtime.maxMemoryPages
		// through the neutral limits (attached by the entity front) into the
		// loader spec. Driven from the entity, never hardcoded on the resolver.
		if resolved.Limits == nil {
			t.Fatal("resolved.Limits is nil; the entity front did not attach the entity's runtime limits")
		}
		spec := resolver.PluginSpec("helm", resolved)
		if spec.MaxMemoryPages != helmProviderMemPag {
			t.Fatalf("loader spec MaxMemoryPages = %d, want %d (from Plugin entity runtime)",
				spec.MaxMemoryPages, helmProviderMemPag)
		}
	})

	t.Run("WrongPinHaltsWithFetchIntegrity", func(t *testing.T) {
		wrongPin := "sha256:" + strings.Repeat("0", 64)
		res := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureLockfile, pluginpath.PinMaps{Pins: map[string]string{helmArtifactURI: wrongPin}})
		front := entityFront(lookup, res.Resolve)

		_, err := front(helmPluginEntity)
		if err == nil {
			t.Fatal("resolve with a wrong pin succeeded; expected an integrity halt")
		}
		if !errors.Is(err, oci.ErrFetchIntegrity()) {
			t.Fatalf("wrong-pin resolve error = %v, want wrapped oci.ErrFetchIntegrity", err)
		}
	})

	t.Run("LoadAndDescribeSmoke", func(t *testing.T) {
		if testing.Short() {
			// Compiling the multi-MB embedded guest through the Wasm runtime is
			// seconds-scale; the resolution + digest + limits proof above is the
			// fast core. This subtest is the optional loadability + describe()
			// coherence bonus, skipped under -short.
			t.Skip("skipping embedded-guest load under -short")
		}
		res := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureLockfile, pluginpath.PinMaps{Pins: map[string]string{helmArtifactURI: embeddedReferenceDigest(t, helmArtifactURI)}})
		front := entityFront(lookup, res.Resolve)
		resolved, err := front(helmPluginEntity)
		if err != nil {
			t.Fatalf("resolve for smoke load: %v", err)
		}

		// LoadAndDescribe runs the capability cross-check of the entity's authored
		// exports against the guest's describe() self-report, so a clean load also
		// proves the Plugin entity's authored claims (pluginType/abiVersion/
		// exports) are coherent with the embedded guest.
		spec := resolver.PluginSpec("helm", resolved)
		loader := &wasm.Loader{}
		ctx := context.Background()
		plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
		if err != nil {
			t.Fatalf("LoadAndDescribe of the embedded guest: %v", err)
		}
		t.Cleanup(func() { _ = plugin.Close(ctx) })

		if desc.PluginType != string(wasm.PluginTypeProvider) {
			t.Fatalf("describe() plugin_type = %q, want %q", desc.PluginType, wasm.PluginTypeProvider)
		}
		if _, ok := findExport(desc, "render_node"); !ok {
			t.Fatal("describe() does not declare the render_node export")
		}
	})
}

// findExport reports whether the descriptor declares an export of the given name.
func findExport(desc wasm.PluginDescriptor, name string) (wasm.PluginExport, bool) {
	for _, e := range desc.Exports {
		if e.Name == name {
			return e, true
		}
	}
	return wasm.PluginExport{}, false
}

// embeddedProviderCoordinates is the identity coordinate — <pluginType>/<name> — of
// every provider-socket plugin whose generated Plugin entity ships inside a full
// binary.
//
// It is spelled out here on purpose, as an independent cross-check rather than a
// read of the embedded-plugin manifest the assertions below enumerate. An
// expectation derived from the same table it measures cannot tell a shrinking
// embedded set apart from a passing check: were the generated entities to stop
// being produced, a self-derived count would agree with an empty set and every
// digest assertion below would pass while comparing nothing. So this list is
// deliberately a second, hand-written statement of the shipped provider set, and
// it must not be "unified" with the manifest it guards.
//
// The order is the sorted order embedded.EntityURIs reports, so the comparison is
// element-wise and also holds that enumeration to being deterministic. Adding,
// removing, or renaming an embedded provider is expected to fail this list; update
// it in the same change.
var embeddedProviderCoordinates = []string{
	"provider/arm",
	"provider/cloudformation",
	"provider/compose",
	"provider/helm",
	"provider/helm-chart",
	"provider/terraform",
}

// embeddedProvider pairs one embedded provider's whole publishable surface: the
// generated Plugin entity YAML exactly as the binary carries it, the decompressed
// guest bytes, and the entity parsed from those same bytes.
type embeddedProvider struct {
	coordinate string
	entityYAML []byte
	wasm       []byte
	entity     types.Plugin
}

// embeddedProviders returns every provider-socket plugin the binary embeds an
// entity for, in embedded.EntityURIs order.
//
// The entity YAML is the raw embedded bytes, never a re-marshalling of the parsed
// entity: those raw bytes are what publishing this plugin sends as the manifest's
// config blob, and a re-marshalled entity is a different byte string that would
// prove nothing about the shipped one. The parsed entity is used only to read the
// facets the searchable annotations are built from — the same split the publish
// path makes.
func embeddedProviders(t *testing.T) []embeddedProvider {
	t.Helper()
	var out []embeddedProvider
	for _, uri := range embedded.EntityURIs() {
		entityYAML, ok := embedded.LookupEntity(uri)
		if !ok {
			t.Fatalf("the embedded set enumerated %q but holds no entity bytes for it", uri)
		}
		var p types.Plugin
		if err := yaml.Unmarshal(entityYAML, &p); err != nil {
			t.Fatalf("parse embedded Plugin entity %q: %v", uri, err)
		}
		if p.Metadata.PluginType != "provider" {
			continue
		}
		// The key an entity is registered under and the identity its own fields
		// derive must be the same string, or a resolution that starts from one
		// cannot find the artifact the other addresses.
		if got := p.Coordinate(); got != uri {
			t.Fatalf("embedded provider registered under %q carries identity coordinate %q; the registration key and the entity's own pluginType/name must agree", uri, got)
		}
		wasmBytes, ok := embedded.Lookup(uri)
		if !ok {
			t.Fatalf("embedded provider %q has an entity but no guest bytes; an entity and its executable ship as one unit", uri)
		}
		out = append(out, embeddedProvider{coordinate: p.Coordinate(), entityYAML: entityYAML, wasm: wasmBytes, entity: p})
	}
	return out
}

// TestEmbeddedProviderLocalManifestDigestEqualsPushedDigest proves that for every
// provider the binary embeds, the manifest digest computed locally over the
// embedded bytes and their searchable annotations is stable across repeat
// computation and identical to the digest publishing those same bytes lands.
//
// Local equals would-publish. The provider a binary carries is byte-for-byte the
// artifact a publish of it would produce, so the digest a consumer fetches and
// verifies against is derivable from the binary alone — no registry reachable, no
// credentials, no signature involved. That is a reproducibility property of how
// the binary is built, and it is the reason an offline resolution of an embedded
// provider can be addressed by the same identity a registry would serve it under.
//
// The push arm runs against an in-memory content store, so nothing here needs a
// network. A manifest is packed before it is tagged, so its digest is a pure
// function of the entity bytes, the guest bytes and the stamped annotations — the
// tag it is published under cannot perturb it.
//
// The searchable annotations are taken from the same facet projection the publish
// path itself calls, not from a restatement of it. That is what lets the claim be
// about publishing rather than about two agreeing inputs: a facet added to the
// projection lands here and in the publish in one edit, so this cannot keep
// passing while it has quietly stopped describing what a publish stamps.
func TestEmbeddedProviderLocalManifestDigestEqualsPushedDigest(t *testing.T) {
	embeddedentities.Require(t)
	ctx := context.Background()
	providers := embeddedProviders(t)

	// Fail before any digest is compared if the enumerated set is not the shipped
	// set. Without this the whole test degrades silently: an empty embedded set
	// makes every assertion below unreachable and the run still reports success,
	// which is indistinguishable from a set that was checked and agreed.
	got := make([]string, 0, len(providers))
	for _, p := range providers {
		got = append(got, p.coordinate)
	}
	if len(got) != len(embeddedProviderCoordinates) {
		t.Fatalf("binary embeds %d provider entities %v, want %d %v — a shrunk set would make every digest assertion below vacuous",
			len(got), got, len(embeddedProviderCoordinates), embeddedProviderCoordinates)
	}
	for i, want := range embeddedProviderCoordinates {
		if got[i] != want {
			t.Fatalf("embedded provider %d = %q, want %q (full set %v, want %v)", i, got[i], want, got, embeddedProviderCoordinates)
		}
	}

	for _, p := range providers {
		t.Run(p.coordinate, func(t *testing.T) {
			// The annotation map a publish of this entity stamps, read from the
			// publish path's own facet projection. The annotations are part of the
			// manifest and therefore part of its digest, so the equality below is a
			// claim about this map as much as about the bytes.
			ann, err := oci.PluginSearchableMetadata(p.entity).Annotations()
			if err != nil {
				t.Fatalf("%s: render the searchable annotations a publish stamps: %v", p.coordinate, err)
			}

			// The annotations must actually carry the entity's facets. Were they
			// empty, both digest computations would still agree — on a manifest
			// that stamps nothing — and the equality below would hold while
			// proving nothing about the searchable surface a publish lands.
			if len(ann) == 0 {
				t.Fatalf("%s: rendered no searchable annotations; the digest equality below would then hold over a manifest with no catalog facets", p.coordinate)
			}
			if got := ann[oci.AnnotationKind]; got != "Plugin" {
				t.Fatalf("%s: kind annotation = %q, want %q", p.coordinate, got, "Plugin")
			}
			if got := ann[oci.AnnotationPluginType]; got != "provider" {
				t.Fatalf("%s: plugin-type annotation = %q, want %q", p.coordinate, got, "provider")
			}
			if got, want := ann[oci.AnnotationCapabilities], strings.Join(p.entity.DeclaredCapabilities(), ","); got != want {
				t.Fatalf("%s: capabilities annotation = %q, want %q", p.coordinate, got, want)
			}

			// Stability: the local computation is a pure function of its inputs, so
			// a second pass over the same bytes and annotations must land the same
			// digest. A digest that moved between passes could not be previewed,
			// signed, or compared against a published tag.
			first, err := oci.PluginManifestDigest(ctx, p.entityYAML, p.wasm, ann)
			if err != nil {
				t.Fatalf("%s: compute manifest digest: %v", p.coordinate, err)
			}
			second, err := oci.PluginManifestDigest(ctx, p.entityYAML, p.wasm, ann)
			if err != nil {
				t.Fatalf("%s: recompute manifest digest: %v", p.coordinate, err)
			}
			if first != second {
				t.Fatalf("%s: manifest digest is not stable: %s then %s", p.coordinate, first, second)
			}

			// Equality with what publishing lands: pack and tag the identical bytes
			// and annotations into an in-memory store through the real publish
			// helper, and compare the digest it reports.
			store := memory.New()
			pushed, err := oci.PushPluginManifest(ctx, store, p.entity.Metadata.Version, p.entityYAML, p.wasm, ann)
			if err != nil {
				t.Fatalf("%s: publish into the in-memory store: %v", p.coordinate, err)
			}
			if first != pushed {
				t.Fatalf("%s: locally computed manifest digest %s does not equal the digest publishing lands, %s — the embedded provider is not byte-identical to the artifact a publish of it would produce",
					p.coordinate, first, pushed)
			}

			// Read the artifact back off the store the way a consumer resolves it.
			// This closes the loop: the manifest that digest addresses commits to
			// exactly the embedded entity and the embedded guest, so the digest is
			// not merely reproducible but reproducible over the shipped content.
			readEntity, readWasm, readDigest, err := oci.FetchPluginManifest(ctx, store, p.entity.Metadata.Version)
			if err != nil {
				t.Fatalf("%s: read the published manifest back: %v", p.coordinate, err)
			}
			if readDigest != first {
				t.Fatalf("%s: manifest digest read back as %s, want %s", p.coordinate, readDigest, first)
			}
			if !bytes.Equal(readEntity, p.entityYAML) {
				t.Fatalf("%s: entity read back off the manifest is not the embedded entity (%d bytes back, %d embedded)", p.coordinate, len(readEntity), len(p.entityYAML))
			}
			if !bytes.Equal(readWasm, p.wasm) {
				t.Fatalf("%s: guest read back off the manifest is not the embedded guest (%d bytes back, %d embedded)", p.coordinate, len(readWasm), len(p.wasm))
			}

			t.Logf("%s: local == published == read-back == %s (entity %d bytes, guest %d bytes, %d annotations)",
				p.coordinate, first, len(p.entityYAML), len(p.wasm), len(ann))
		})
	}
}

// TestEmbeddedProviderManifestDigestDetectsMutatedInput is the negative control for
// the equality proof above. Without it, an agreeing pair of digests cannot be told
// apart from a comparison that never ran or a digest insensitive to its input: any
// two constants are equal. Each case perturbs one input by the smallest amount that
// still counts as different content and requires the digest to move.
//
// Each mutated input is also re-checked for local/published agreement, so the
// equality claim is shown to be a property of the two computations across inputs
// rather than a coincidence at the one input the binary happens to ship.
func TestEmbeddedProviderManifestDigestDetectsMutatedInput(t *testing.T) {
	embeddedentities.Require(t)
	ctx := context.Background()
	providers := embeddedProviders(t)
	if len(providers) == 0 {
		t.Fatal("binary embeds no provider entities; the mutation control has nothing to perturb")
	}
	subject := providers[0]
	baseAnn, err := oci.PluginSearchableMetadata(subject.entity).Annotations()
	if err != nil {
		t.Fatalf("%s: render the searchable annotations a publish stamps: %v", subject.coordinate, err)
	}

	baseline, err := oci.PluginManifestDigest(ctx, subject.entityYAML, subject.wasm, baseAnn)
	if err != nil {
		t.Fatalf("%s: baseline manifest digest: %v", subject.coordinate, err)
	}
	t.Logf("mutation control subject %s, baseline %s", subject.coordinate, baseline)

	// A YAML comment appended to the entity leaves the parsed entity identical and
	// the bytes different. Requiring the digest to move states that the manifest
	// commits to the published bytes, not to their parsed meaning.
	entityDrift := append(append([]byte{}, subject.entityYAML...), "\n# an edit that changes no parsed field\n"...)
	guestDrift := append(append([]byte{}, subject.wasm...), 0x00)

	annWithoutCapabilities := map[string]string{}
	for k, v := range baseAnn {
		annWithoutCapabilities[k] = v
	}
	delete(annWithoutCapabilities, oci.AnnotationCapabilities)

	annWithOtherPluginType := map[string]string{}
	for k, v := range baseAnn {
		annWithOtherPluginType[k] = v
	}
	annWithOtherPluginType[oci.AnnotationPluginType] = "linter"

	cases := []struct {
		name       string
		entityYAML []byte
		wasm       []byte
		ann        map[string]string
	}{
		{"EntityBytesEdited", entityDrift, subject.wasm, baseAnn},
		{"GuestBytesEdited", subject.entityYAML, guestDrift, baseAnn},
		{"CapabilitiesAnnotationDropped", subject.entityYAML, subject.wasm, annWithoutCapabilities},
		{"PluginTypeAnnotationChanged", subject.entityYAML, subject.wasm, annWithOtherPluginType},
		{"AnnotationsStrippedEntirely", subject.entityYAML, subject.wasm, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated, err := oci.PluginManifestDigest(ctx, c.entityYAML, c.wasm, c.ann)
			if err != nil {
				t.Fatalf("compute manifest digest for the mutated input: %v", err)
			}
			if mutated == baseline {
				t.Fatalf("mutated input produced the baseline digest %s; the digest is not sensitive to this input, so an equality assertion over it proves nothing", baseline)
			}
			store := memory.New()
			pushed, err := oci.PushPluginManifest(ctx, store, subject.entity.Metadata.Version, c.entityYAML, c.wasm, c.ann)
			if err != nil {
				t.Fatalf("publish the mutated input into the in-memory store: %v", err)
			}
			if mutated != pushed {
				t.Fatalf("locally computed digest %s does not equal the digest publishing lands, %s, for the mutated input", mutated, pushed)
			}
			t.Logf("%s: digest moved to %s and still equals what publishing lands", c.name, mutated)
		})
	}
}
