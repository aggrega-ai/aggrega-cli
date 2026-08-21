// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// embeddedLinterAttachment configures one linter attachment whose plugin is
// served by the embed tier, and returns the workspace config, the entity reader,
// the plugin's identity coordinate and the digest of the bytes the embed tier
// serves.
//
// The embed tier is deliberately the tier under test. It fronts the content cache
// in the cascade and consults no manifest, so it is one of the tiers a resolution
// checks against nothing at all unless a digest is recorded for the plugin — which
// makes it where a recorded digest adds a check rather than sharpening one.
func embeddedLinterAttachment(t *testing.T) (workspaceconfig.Config, func(string) (types.Plugin, bool), string, string) {
	t.Helper()

	var entity types.Plugin
	entity.Metadata.Name = "house-style"
	entity.Metadata.PluginType = "linter"
	entity.Metadata.Version = "0.3.0"

	artifactRef := entity.ArtifactReference()
	guest := []byte("\x00asm-house-style-guest")

	restore := embedded.SeedForTest(artifactRef, guest)
	t.Cleanup(restore)

	wsCfg := workspaceconfig.Default()
	wsCfg.Plugins.Linter = []workspaceconfig.PluginRef{
		{URI: "core/linter/house-style:0.3.0", Function: "lint_rules"},
	}

	read := func(string) (types.Plugin, bool) { return entity, true }
	return wsCfg, read, artifactRef, digest.ContentDigest(guest)
}

// writeGuestFile writes guest bytes to a temporary file and returns its path, for
// the artifact-direct override path, which reads a filesystem path verbatim.
func writeGuestFile(t *testing.T, guest []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "override.wasm")
	if err := os.WriteFile(path, guest, 0o644); err != nil {
		t.Fatalf("write override guest: %v", err)
	}
	return path
}

// TestLinterFirstArmVerifiesAgainstAnAgreeingRecordedDigest: with a digest
// recorded for the configured attachment's plugin, the first arm resolves under a
// posture that verifies the bytes against it — and the resolution still succeeds,
// because the bytes agree.
func TestLinterFirstArmVerifiesAgainstAnAgreeingRecordedDigest(t *testing.T) {
	wsCfg, read, artifactRef, guestDigest := embeddedLinterAttachment(t)

	trusted := pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: guestDigest},
		PinVersions: map[string]string{artifactRef: guestDigest},
	}

	attachments, err := ResolveLinterPlugins("", wsCfg, trusted, read)
	if err != nil {
		t.Fatalf("an agreeing recorded digest must resolve; got %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("one configured attachment must resolve to one plugin; got %d", len(attachments))
	}
	if attachments[0].Plugin.Tier != pluginpath.TierEmbed {
		t.Errorf("the fixture must exercise the embed tier; got %s", attachments[0].Plugin.Tier)
	}
	if attachments[0].Export != "lint_rules" {
		t.Errorf("the attachment's own export must survive; got %q", attachments[0].Export)
	}
}

// TestLinterFirstArmHaltsOnADisagreeingRecordedDigest is the discriminating half:
// over the identical fixture, a recorded digest the served bytes do not match must
// not resolve. Without the posture change the pin is ignored and this resolves
// cleanly, so a pass here is what proves the pin participates at all.
func TestLinterFirstArmHaltsOnADisagreeingRecordedDigest(t *testing.T) {
	wsCfg, read, artifactRef, _ := embeddedLinterAttachment(t)

	const wrong = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	trusted := pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: wrong},
		PinVersions: map[string]string{artifactRef: wrong},
	}

	_, err := ResolveLinterPlugins("", wsCfg, trusted, read)
	if err == nil {
		t.Fatal("a recorded digest the resolved bytes do not match must not silently succeed; got a clean resolution")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("the halt must name the content-integrity condition; got %v", err)
	}
}

// TestLinterFirstArmStaysPinFreeWithoutRecordedDigests is the A/B against both
// tests above: over the identical fixture, no recorded digests resolves exactly as
// it did before this mechanism existed. An absent record is a first-run state, not
// a fault, and never a pin of zero.
func TestLinterFirstArmStaysPinFreeWithoutRecordedDigests(t *testing.T) {
	wsCfg, read, _, _ := embeddedLinterAttachment(t)

	attachments, err := ResolveLinterPlugins("", wsCfg, pluginpath.PinMaps{}, read)
	if err != nil {
		t.Fatalf("no recorded digest must resolve exactly as before; got %v", err)
	}
	if len(attachments) != 1 || attachments[0].Plugin.Tier != pluginpath.TierEmbed {
		t.Fatalf("the pin-free resolution changed shape: %d attachment(s)", len(attachments))
	}
}

// TestLinterOverrideStaysOutOfReachOfAnyRecord holds the coverage bound the godoc
// states, and holds it over REAL records rather than a hand-built pin map: the
// --plugin escape hatch resolves by an empty reference, a record is addressed by a
// plugin identity, and the record-to-pin conversion refuses an empty address — so
// no record reaches the override.
//
// The record set deliberately carries both a record for the configured
// attachment's plugin and one whose address is empty. The first must not govern a
// resolution of something else; the second must not survive the conversion at all,
// which is the guard that keeps the override out of reach. Converting through the
// real conversion rather than assembling the maps here is the point: a hand-built
// map could carry a key no record can produce, and the bound being asserted is
// about records.
func TestLinterOverrideStaysOutOfReachOfAnyRecord(t *testing.T) {
	wsCfg, _, artifactRef, _ := embeddedLinterAttachment(t)

	override := writeGuestFile(t, []byte("\x00asm-override-bytes"))
	const wrong = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	records := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{
			{Plugin: artifactRef, ResolvedVersion: "0.3.0", Digest: wrong},
			{Plugin: "", ResolvedVersion: "0.0.0", Digest: wrong},
		},
	}

	attachments, err := ResolveLinterPlugins(override, wsCfg, records.PinMaps(), nil)
	if err != nil {
		t.Fatalf("the override path carries no plugin identity, so no record governs it; got %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("the escape hatch resolves exactly one plugin; got %d", len(attachments))
	}
	if string(attachments[0].Plugin.Bytes) != "\x00asm-override-bytes" {
		t.Errorf("the override must serve the file it names; got %q", attachments[0].Plugin.Bytes)
	}
}
