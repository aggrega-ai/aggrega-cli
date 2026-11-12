// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// verdictPluginFixture seeds a content-cache slot holding a verdict-socket
// plugin's .wasm and returns the tier sources addressing it, the entity reader
// the resolution consumes, the plugin's identity coordinate, and the digest the
// cached manifest reports for those bytes.
//
// The slot is addressed by (identity coordinate, content digest), which is how
// the pull path writes a plugin artifact, so a resolution keyed by either the
// manifest-derived digest or a recorded digest equal to it addresses the same
// slot. That equality is what the cache-layout half of this mechanism rests on,
// so the fixture builds the slot that way rather than asserting it separately.
func verdictPluginFixture(t *testing.T) (VerdictPluginTierSources, func(string) (types.Plugin, string, bool, error), string, string) {
	t.Helper()

	wasm := []byte("\x00asm-verdict-guest")
	wasmDigest := digest.ContentDigest(wasm)

	var entity types.Plugin
	entity.Metadata.Name = "trust-probe"
	entity.Metadata.PluginType = "governance"
	entity.Metadata.Version = "2.4.1"
	entity.Spec.Runtime = &types.PluginRuntime{MaxMemoryPages: 128, TimeoutMs: 4321}
	entity.Spec.Exports = []types.PluginExport{{Name: "verdict"}}

	artifactRef := entity.ArtifactReference()

	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	if err := oci.WriteCached(ws, cacheDir, oci.PluginArtifactKind(), artifactRef, wasmDigest, wasm); err != nil {
		t.Fatalf("seed content cache: %v", err)
	}

	sources := VerdictPluginTierSources{WorkspaceRoot: ws, CacheDir: cacheDir}
	read := func(string) (types.Plugin, string, bool, error) {
		return entity, wasmDigest, true, nil
	}
	return sources, read, artifactRef, wasmDigest
}

// TestVerdictPluginTrustedDigestDisagreementHalts is the load-bearing test of the
// cross-check: a digest recorded as trusted for the plugin that differs from the
// digest its cached manifest reports halts the resolution. That disagreement is
// the tamper case the recorded digest exists to catch, and without the comparison
// the resolution would proceed and verify the bytes against the very manifest
// that named them.
func TestVerdictPluginTrustedDigestDisagreementHalts(t *testing.T) {
	sources, read, artifactRef, wasmDigest := verdictPluginFixture(t)

	const stale = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	trusted := VerdictPluginTrust{PinMaps: pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: stale},
		PinVersions: map[string]string{artifactRef: stale},
	}}

	_, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri")
	if err == nil {
		t.Fatal("a recorded digest disagreeing with the cached manifest must halt; got a clean resolution")
	}
	if !errors.Is(err, ErrVerdictPluginTrustMismatch()) {
		t.Errorf("the halt must name the trust-mismatch condition; got %v", err)
	}
	// A disagreement is not a corrupted artifact, and the two must stay
	// distinguishable: nothing has read the bytes yet.
	if errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("a trust disagreement must not be reported as a content-integrity failure; got %v", err)
	}
	// The diagnostic has to be actionable: the identity, and BOTH digests, so the
	// reader can see what changed rather than only that something did.
	for _, want := range []string{artifactRef, stale, wasmDigest} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt should name %q; got %v", want, err)
		}
	}
}

// TestVerdictPluginAgreeingTrustedDigestStillReadsEntity holds the other half of
// the cross-check: the entity read is not substituted away. An agreeing recorded
// digest resolves, and the resolution still carries the version, the capability
// claim and the runtime limits the ENTITY supplies — none of which a digest could
// have provided. Substituting one for the other would lose exactly these.
func TestVerdictPluginAgreeingTrustedDigestStillReadsEntity(t *testing.T) {
	sources, read, artifactRef, wasmDigest := verdictPluginFixture(t)

	trusted := VerdictPluginTrust{PinMaps: pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: wasmDigest},
		PinVersions: map[string]string{artifactRef: wasmDigest},
	}}

	resolved, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri")
	if err != nil {
		t.Fatalf("an agreeing recorded digest must resolve; got %v", err)
	}
	if resolved.Version != "2.4.1" {
		t.Errorf("version must come from the entity, not from the digest; got %q", resolved.Version)
	}
	if resolved.Contract == nil {
		t.Error("the entity's capability claim must survive the comparison; got nil")
	}
	if resolved.Limits == nil {
		t.Fatal("the entity's runtime limits must survive the comparison; got nil")
	}
	if resolved.Limits.MaxMemoryPages != 128 || resolved.Limits.TimeoutMs != 4321 {
		t.Errorf("runtime limits must be the entity's; got pages=%d ms=%d", resolved.Limits.MaxMemoryPages, resolved.Limits.TimeoutMs)
	}
	if resolved.Tier != pluginpath.TierCache {
		t.Errorf("the recorded digest must address the same cache slot the manifest-derived one does; got tier %s", resolved.Tier)
	}
}

// TestVerdictPluginWithoutTrustedDigestIsUnchanged is the A/B against the test
// above: over the identical fixture, supplying no recorded digests resolves to the
// same plugin. An absent record is a first-run state rather than a fault, and it is
// never treated as a pin of zero.
func TestVerdictPluginWithoutTrustedDigestIsUnchanged(t *testing.T) {
	sources, read, _, _ := verdictPluginFixture(t)

	resolved, err := ResolveVerdictPluginFromCache(sources, VerdictPluginTrust{}, read, "any/uri")
	if err != nil {
		t.Fatalf("no recorded digest must resolve exactly as before; got %v", err)
	}
	if resolved.Version != "2.4.1" || resolved.Limits == nil || resolved.Tier != pluginpath.TierCache {
		t.Errorf("the pin-free resolution changed shape: version=%q limits=%v tier=%s",
			resolved.Version, resolved.Limits, resolved.Tier)
	}
}

// TestVerdictPluginTrustedDigestForAnotherPluginDoesNotApply: records are
// addressed by plugin identity, so a digest recorded for a different plugin is not
// consulted for this one. A comparison that matched on anything looser would halt
// on an unrelated record.
func TestVerdictPluginTrustedDigestForAnotherPluginDoesNotApply(t *testing.T) {
	sources, read, _, _ := verdictPluginFixture(t)

	const stale = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	trusted := VerdictPluginTrust{PinMaps: pluginpath.PinMaps{
		Pins:        map[string]string{"governance/someone-else": stale},
		PinVersions: map[string]string{"governance/someone-else": stale},
	}}

	if _, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri"); err != nil {
		t.Fatalf("a record addressed to another plugin must not govern this one; got %v", err)
	}
}

// TestTheBinarysOwnModuleRunsAndAnyOtherIsHeldToItsCachedManifest holds which
// module a cached Plugin entity runs, and what that module is held to.
//
// The entity's version selects the entity, not the engine: where the binary carries
// a module under the entity's identity, that module runs and is not held to the
// module digest the cached manifest reports, which describes the module a registry
// publishes for that version. Every other module stays held to that digest, and a
// module that does not match it is refused naming the manifest — never a lockfile
// pin, which nothing here reads. One fault per row; the first row is the control.
func TestTheBinarysOwnModuleRunsAndAnyOtherIsHeldToItsCachedManifest(t *testing.T) {
	binaryModule := []byte("\x00asm-the-binarys-own-module")
	cacheTampered := []byte("\x00asm-rewritten-in-the-cache")
	rows := []struct {
		name     string
		inBinary []byte
		inCache  []byte
		wantTier pluginpath.Tier
		want     []byte
		refused  []byte
	}{
		{name: "the binary carries nothing; the cached module matches its manifest", wantTier: pluginpath.TierCache},
		{name: "the binary carries a module under the identity, other than the manifest's", inBinary: binaryModule,
			wantTier: pluginpath.TierEmbed, want: binaryModule},
		{name: "the binary carries nothing; the cached module does not match its manifest", inCache: cacheTampered,
			refused: cacheTampered},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			sources, read, artifactRef, wasmDigest := verdictPluginFixture(t)
			if row.inBinary != nil {
				t.Cleanup(embedded.SeedForTest(artifactRef, row.inBinary))
			}
			if row.inCache != nil {
				if err := oci.WriteCached(sources.WorkspaceRoot, sources.CacheDir, oci.PluginArtifactKind(), artifactRef, wasmDigest, row.inCache); err != nil {
					t.Fatalf("rewrite the cached module: %v", err)
				}
			}
			resolved, err := ResolveVerdictPluginFromCache(sources, VerdictPluginTrust{}, read, "any/uri")
			if row.refused == nil {
				if err != nil {
					t.Fatalf("want the module to resolve; got %v", err)
				}
				if resolved.Tier != row.wantTier {
					t.Errorf("resolved from %s; want %s", resolved.Tier, row.wantTier)
				}
				if row.want != nil && string(resolved.Bytes) != string(row.want) {
					t.Errorf("resolved bytes are not the binary's own module")
				}
				// The entity still supplies what the version selects.
				if resolved.Version != "2.4.1" || resolved.Contract == nil || resolved.Limits == nil {
					t.Errorf("the entity's version, contract and limits must ride on the resolution: version=%q contract=%v limits=%v",
						resolved.Version, resolved.Contract, resolved.Limits)
				}
				return
			}
			if !errors.Is(err, oci.ErrFetchIntegrity()) {
				t.Fatalf("want the content-digest refusal; got %v", err)
			}
			msg := err.Error()
			for _, want := range []string{artifactRef, digest.ContentDigest(row.refused), cachedManifestModuleDigest + " " + wasmDigest} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if strings.Contains(msg, "lockfile pin") {
				t.Errorf("the refusal names a lockfile pin, which this resolution does not read: %v", err)
			}
			if resolved.Bytes != nil {
				t.Errorf("a refused load handed back the plugin's bytes")
			}
		})
	}
}

// TestVerdictPluginEmptyRecordedDigestIsNotAPinOfZero: an entry present with an
// empty digest records nothing, so it neither halts nor becomes a pin that no
// bytes can satisfy.
func TestVerdictPluginEmptyRecordedDigestIsNotAPinOfZero(t *testing.T) {
	sources, read, artifactRef, _ := verdictPluginFixture(t)

	trusted := VerdictPluginTrust{PinMaps: pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: ""},
		PinVersions: map[string]string{artifactRef: ""},
	}}

	if _, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri"); err != nil {
		t.Fatalf("an empty recorded digest must not halt the resolution; got %v", err)
	}
}

// TestVerdictPluginLoadReHashesTheRecordedBundle holds the load to the signature
// bundle the record names for the plugin: the cached bundle is re-hashed against
// the recorded digest before the plugin's bytes are handed back, by the same step a
// lockfile-pinned plugin's bundle goes through. One fault per row, each against the
// same seeded cache; the first row is the control that keeps the others attributable.
func TestVerdictPluginLoadReHashesTheRecordedBundle(t *testing.T) {
	bundle := []byte("a signature bundle the pull cached")
	bundleDigest := digest.ContentDigest(bundle)
	rows := []struct {
		name   string
		cached []byte
		want   error
	}{
		{name: "the bundle as the pull cached it", cached: bundle},
		{name: "the bundle altered after the pull", cached: []byte("rewritten after the pull"), want: oci.ErrSignatureBundleDigestMismatch()},
		{name: "the bundle removed", want: oci.ErrSignatureBundleDigestMismatch()},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			sources, read, artifactRef, wasmDigest := verdictPluginFixture(t)
			if row.cached != nil {
				at := oci.SignatureBundlePath(sources.WorkspaceRoot, sources.CacheDir, bundleDigest)
				if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(at, row.cached, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			trusted := VerdictPluginTrust{PinMaps: pluginpath.PinMaps{
				Pins:          map[string]string{artifactRef: wasmDigest},
				PinVersions:   map[string]string{artifactRef: wasmDigest},
				BundleDigests: map[string]string{artifactRef: bundleDigest},
			}}
			resolved, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri")
			if row.want == nil {
				if err != nil {
					t.Fatalf("an intact recorded bundle must load; got %v", err)
				}
				if resolved.Tier != pluginpath.TierCache {
					t.Errorf("resolved from %s; want the cache tier", resolved.Tier)
				}
				return
			}
			if !errors.Is(err, row.want) {
				t.Fatalf("want %v, got %v", row.want, err)
			}
			if !strings.Contains(err.Error(), artifactRef) {
				t.Errorf("the refusal does not name the plugin %q: %v", artifactRef, err)
			}
			if resolved.Bytes != nil {
				t.Errorf("a refused load handed back the plugin's bytes")
			}
		})
	}

	// A bundle digest recorded for another plugin is not this one's.
	t.Run("a bundle recorded for another plugin", func(t *testing.T) {
		sources, read, _, _ := verdictPluginFixture(t)
		trusted := VerdictPluginTrust{PinMaps: pluginpath.PinMaps{BundleDigests: map[string]string{"governance/someone-else": bundleDigest}}}
		if _, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri"); err != nil {
			t.Fatalf("a bundle recorded for another plugin must not govern this one; got %v", err)
		}
	})
}

// TestVerdictPluginLoadConsultsTheSignatureRequirementFirst holds the load to the
// requirement a plugin whose record carries no signature is held to: the hold is
// asked with the attachment's reference and the plugin's identity coordinate, its
// refusal is the load's, returned as itself, and it is asked before any byte is
// read — the cache below holds no bytes for the plugin at all.
func TestVerdictPluginLoadConsultsTheSignatureRequirementFirst(t *testing.T) {
	refused := errors.New("no signature recorded for a plugin that requires one")
	var gotURI, gotCoordinate string
	trusted := VerdictPluginTrust{HoldUnrecorded: func(uri, coordinate string) error {
		gotURI, gotCoordinate = uri, coordinate
		return refused
	}}
	var entity types.Plugin
	entity.Metadata.Name = "unsigned-gate"
	entity.Metadata.PluginType = "linter"
	entity.Metadata.Version = "1.0.0"
	read := func(string) (types.Plugin, string, bool, error) {
		return entity, "sha256:3333333333333333333333333333333333333333333333333333333333333333", true, nil
	}
	sources := VerdictPluginTierSources{WorkspaceRoot: t.TempDir(), CacheDir: ".aggrega/cache"}

	_, err := ResolveVerdictPluginFromCache(sources, trusted, read, "linter/unsigned-gate:1.0.0")
	if !errors.Is(err, refused) {
		t.Fatalf("the hold's refusal must be the load's; got %v", err)
	}
	if gotURI != "linter/unsigned-gate:1.0.0" || gotCoordinate != entity.ArtifactReference() {
		t.Errorf("the hold was asked about (%q, %q); want the attachment's reference and the plugin's coordinate (%q, %q)",
			gotURI, gotCoordinate, "linter/unsigned-gate:1.0.0", entity.ArtifactReference())
	}

	// The control: the same load with a hold that admits the plugin goes on to read
	// the bytes, and fails there, so the refusal above is the hold's.
	trusted.HoldUnrecorded = func(string, string) error { return nil }
	_, err = ResolveVerdictPluginFromCache(sources, trusted, read, "linter/unsigned-gate:1.0.0")
	if err == nil || errors.Is(err, refused) {
		t.Fatalf("with the hold admitting the plugin the load must reach the cache and miss there; got %v", err)
	}
}
