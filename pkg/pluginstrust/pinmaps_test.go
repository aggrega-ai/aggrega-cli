// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginstrust

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestRecordAddressIsThePluginArtifactReference is the guard on the identity the
// conversion silently depends on: a record is addressed by a plugin's identity
// coordinate, a resolver's pin maps are keyed by the artifact reference a plugin
// resolves to, and PinMaps re-keys one as the other on the strength of those
// being the same string.
//
// Nothing at the conversion site names a Plugin entity, so a reader cannot see
// that dependency and a change to the derivation would not break any compile. It
// would instead make every conversion pin a plugin nothing resolves — silently
// pinning nothing, which is the failure mode a trust record exists to prevent.
// This asserts the derivation directly so that change fails loudly here instead.
func TestRecordAddressIsThePluginArtifactReference(t *testing.T) {
	var entity types.Plugin
	entity.Metadata.Name = "aggrega-security-baseline"
	entity.Metadata.PluginType = "governance"
	entity.Metadata.Version = "1.2.3"

	coordinate := types.PluginCoordinate(entity.Metadata.PluginType, entity.Metadata.Name)
	if got := entity.ArtifactReference(); got != coordinate {
		t.Fatalf("a Plugin entity's artifact reference must be its identity coordinate: got %q, coordinate %q. "+
			"PinMaps re-keys a record's address as a pin key on the strength of these being equal; "+
			"if the derivation has changed, the conversion now pins nothing", got, coordinate)
	}

	// And the conversion must actually produce that key, not merely be able to.
	trust := &Trust{Version: CurrentVersion}
	trust.Upsert(TrustedPlugin{Plugin: coordinate, ResolvedVersion: "1.2.3", Digest: "sha256:abc"})

	pins := trust.PinMaps()
	if got, ok := pins.Pins[entity.ArtifactReference()]; !ok || got != "sha256:abc" {
		t.Errorf("the pin map must be keyed by the artifact reference the cascade resolves by: got %q present=%v", got, ok)
	}
	if got, ok := pins.PinVersions[entity.ArtifactReference()]; !ok || got != "sha256:abc" {
		t.Errorf("the cache-slot key must carry the recorded digest, which is what addresses a plugin artifact's slot: got %q present=%v", got, ok)
	}
}

// TestPinMapsCarriesEveryRecord: the conversion is over the whole record set, so
// a file recording several plugins pins all of them rather than the first.
func TestPinMapsCarriesEveryRecord(t *testing.T) {
	trust := &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{
			{Plugin: "governance/baseline", ResolvedVersion: "1.0.0", Digest: "sha256:aaa"},
			{Plugin: "linter/publishing", ResolvedVersion: "0.5.0", Digest: "sha256:bbb"},
			{Plugin: "linter/house-style", ResolvedVersion: "0.1.0", Digest: "sha256:ccc"},
		},
	}

	pins := trust.PinMaps()
	if len(pins.Pins) != 3 || len(pins.PinVersions) != 3 {
		t.Fatalf("every record must convert: got %d pins, %d versions", len(pins.Pins), len(pins.PinVersions))
	}
	for _, want := range []struct{ coordinate, digest string }{
		{"governance/baseline", "sha256:aaa"},
		{"linter/publishing", "sha256:bbb"},
		{"linter/house-style", "sha256:ccc"},
	} {
		if got := pins.Pins[want.coordinate]; got != want.digest {
			t.Errorf("pin for %s: got %q, want %q", want.coordinate, got, want.digest)
		}
	}
	// A signature bundle is not something this record carries, so the conversion
	// must not invent one.
	if pins.BundleDigests != nil {
		t.Errorf("the conversion must leave signature-bundle digests unset; got %v", pins.BundleDigests)
	}
}

// TestPinMapsOnNilTrustYieldsEmptyMaps: a caller that has loaded no file must not
// have to branch before asking, the same contract Find offers. Empty maps rather
// than nil ones, so a resolver constructed over them carries no pin for anything.
func TestPinMapsOnNilTrustYieldsEmptyMaps(t *testing.T) {
	var absent *Trust

	pins := absent.PinMaps()
	if pins.Pins == nil || pins.PinVersions == nil {
		t.Fatalf("a nil record set must yield usable empty maps; got pins=%v versions=%v", pins.Pins, pins.PinVersions)
	}
	if len(pins.Pins) != 0 || len(pins.PinVersions) != 0 {
		t.Errorf("a nil record set holds no records; got %d pins, %d versions", len(pins.Pins), len(pins.PinVersions))
	}
}

// TestPinMapsSkipsAnEmptyAddress: a record whose coordinate is empty addresses no
// plugin, so converting it would pin whatever a resolver happens to resolve under
// the empty reference — which is exactly what the --plugin escape hatch resolves
// under. Skipping it is what keeps that path out of reach of any record.
func TestPinMapsSkipsAnEmptyAddress(t *testing.T) {
	trust := &Trust{
		Version: CurrentVersion,
		Plugins: []TrustedPlugin{
			{Plugin: "", ResolvedVersion: "1.0.0", Digest: "sha256:ddd"},
			{Plugin: "governance/real", ResolvedVersion: "1.0.0", Digest: "sha256:eee"},
		},
	}

	pins := trust.PinMaps()
	if _, present := pins.Pins[""]; present {
		t.Error("an empty address must not become a pin: it would govern the artifact-direct override path")
	}
	if len(pins.Pins) != 1 {
		t.Errorf("only the addressed record converts; got %d pins", len(pins.Pins))
	}
}
