// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

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
	if err := oci.WriteCached(ws, cacheDir, artifactRef, wasmDigest, wasm); err != nil {
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
	trusted := pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: stale},
		PinVersions: map[string]string{artifactRef: stale},
	}

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

	trusted := pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: wasmDigest},
		PinVersions: map[string]string{artifactRef: wasmDigest},
	}

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

	resolved, err := ResolveVerdictPluginFromCache(sources, pluginpath.PinMaps{}, read, "any/uri")
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
	trusted := pluginpath.PinMaps{
		Pins:        map[string]string{"governance/someone-else": stale},
		PinVersions: map[string]string{"governance/someone-else": stale},
	}

	if _, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri"); err != nil {
		t.Fatalf("a record addressed to another plugin must not govern this one; got %v", err)
	}
}

// TestVerdictPluginEmptyRecordedDigestIsNotAPinOfZero: an entry present with an
// empty digest records nothing, so it neither halts nor becomes a pin that no
// bytes can satisfy.
func TestVerdictPluginEmptyRecordedDigestIsNotAPinOfZero(t *testing.T) {
	sources, read, artifactRef, _ := verdictPluginFixture(t)

	trusted := pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: ""},
		PinVersions: map[string]string{artifactRef: ""},
	}

	if _, err := ResolveVerdictPluginFromCache(sources, trusted, read, "any/uri"); err != nil {
		t.Fatalf("an empty recorded digest must not halt the resolution; got %v", err)
	}
}
