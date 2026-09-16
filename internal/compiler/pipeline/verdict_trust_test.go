// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The verdict sockets resolve a cached-not-pinned gate plugin through one shared
// site, and the digests recorded as trusted for those plugins reach it as a
// pipeline input. These tests hold that the input is actually consulted there,
// because the zero value of that input is a LEGAL value meaning "nothing is
// recorded" — so a wiring that never supplies it produces exactly the resolution
// it produced before the record existed, with nothing failing and nothing warning.
//
// Each test supplies an EXPLICITLY EMPTY workspace configuration, and empty is
// what these fixtures mean: the resolution under test is driven by the recorded
// digests and by the cache reader on the catalog lookup, and the workspace
// contributes no artifact cache directory of its own. It is supplied rather than
// left out because these drive the resolution helper directly instead of through
// the graph entry, which is where an unsupplied configuration is refused — below
// that entry the configuration is read without a nil test, since the refusal is
// what establishes there is one to read.

// cachedVerdictGate returns a cache reader serving one Plugin entity for a
// verdict gate, the coordinate that entity's identity derives, and the .wasm
// digest its cached manifest reports.
func cachedVerdictGate() (resolver.CatalogLookup, string, string) {
	var entity types.Plugin
	entity.Metadata.Name = "opa-gate"
	entity.Metadata.PluginType = "governance"
	entity.Metadata.Version = "1.2.0"

	const manifestDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	lookup := resolver.CatalogLookup{
		PluginFromCache: func(string) (types.Plugin, string, bool, error) {
			return entity, manifestDigest, true, nil
		},
	}
	return lookup, entity.ArtifactReference(), manifestDigest
}

// TestVerdictSocketHonoursADisagreeingRecordedDigest is the needle for the
// pipeline's consumption of the recorded digests: with a digest recorded for the
// gate that the cached manifest does not report, the resolution refuses before it
// reads a byte.
func TestVerdictSocketHonoursADisagreeingRecordedDigest(t *testing.T) {
	lookup, coordinate, _ := cachedVerdictGate()

	const wrong = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	inputs := Inputs{
		WorkspaceConfig: &workspaceconfig.Config{},
		VerdictPluginTrust: pluginpath.PinMaps{
			Pins:        map[string]string{coordinate: wrong},
			PinVersions: map[string]string{coordinate: wrong},
		},
	}

	_, err := resolveVerdictSocketPluginFromCache(inputs, lookup, "governance/opa-gate:1.2.0")
	if err == nil {
		t.Fatal("a recorded digest the cached manifest contradicts must not resolve; got a clean resolution")
	}
	if !errors.Is(err, resolver.ErrVerdictPluginTrustMismatch()) {
		t.Fatalf("the halt must name the trust-mismatch condition; got %v", err)
	}
}

// TestVerdictSocketResolvesPinFreeWithoutRecordedDigests is the A/B that makes the
// test above attributable. Over the identical fixture, an unsupplied record
// resolves exactly as it did before the record existed: it still fails, because
// the fixture holds no guest bytes, but it fails somewhere else entirely — so a
// pass here proves the trust-mismatch halt above came from the recorded digest
// rather than from the fixture.
func TestVerdictSocketResolvesPinFreeWithoutRecordedDigests(t *testing.T) {
	lookup, _, _ := cachedVerdictGate()

	_, err := resolveVerdictSocketPluginFromCache(
		Inputs{WorkspaceConfig: &workspaceconfig.Config{}}, lookup, "governance/opa-gate:1.2.0")
	if err == nil {
		t.Fatal("the fixture holds no guest bytes, so the resolution cannot succeed; the A/B would be vacuous")
	}
	if errors.Is(err, resolver.ErrVerdictPluginTrustMismatch()) {
		t.Errorf("an unsupplied record is not a pin of zero and must raise no trust disagreement; got %v", err)
	}
}

// TestVerdictSocketAcceptsAnAgreeingRecordedDigest completes the discrimination in
// the third direction: a recorded digest that agrees with the cached manifest
// raises no trust disagreement, so the halt above is keyed on the disagreement
// rather than on a record being present at all.
func TestVerdictSocketAcceptsAnAgreeingRecordedDigest(t *testing.T) {
	lookup, coordinate, manifestDigest := cachedVerdictGate()

	inputs := Inputs{
		WorkspaceConfig: &workspaceconfig.Config{},
		VerdictPluginTrust: pluginpath.PinMaps{
			Pins:        map[string]string{coordinate: manifestDigest},
			PinVersions: map[string]string{coordinate: manifestDigest},
		},
	}

	_, err := resolveVerdictSocketPluginFromCache(inputs, lookup, "governance/opa-gate:1.2.0")
	if errors.Is(err, resolver.ErrVerdictPluginTrustMismatch()) {
		t.Errorf("an agreeing recorded digest must raise no trust disagreement; got %v", err)
	}
}
