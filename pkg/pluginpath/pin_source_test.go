// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import "testing"

// TestAPinFreeSourceAndARecordedOneAreDifferentConstructions is the property the
// type exists for, and the one a clause-by-clause reading keeps collapsing: it is
// not enough that both are spelled, the two spellings have to REACH the resolver
// differently. An implementation mapping both onto one posture would satisfy
// every other assertion about pin sources while restoring the exact defect they
// were written for.
//
// It asserts against the POSTURE rather than against the map's length, and that
// is the rule keeping the two halves apart. An empty pin map is the ordinary
// first-run state for a cache-not-pin read and the defect for a pinned one, so a
// test keyed on emptiness would be asking the question that has two answers at
// once.
func TestAPinFreeSourceAndARecordedOneAreDifferentConstructions(t *testing.T) {
	rc := ResolutionContext{}

	pinFree := NoPins().Resolver(rc)
	if pinFree.expectsRecordedIdentity {
		t.Error("a pin-free source produced a construction expecting a recorded identity; it has nothing to expect one from")
	}

	// The empty map is the point: a record that pins nothing yet still declares
	// that pins govern here. That claim is what keeps an unrecorded guest worth
	// reporting, and it is precisely what a pin-free source does not say.
	recorded := RecordedPins(PinMaps{}).Resolver(rc)
	if !recorded.expectsRecordedIdentity {
		t.Error("a recorded source that pins nothing yet produced a construction expecting no recorded identity; " +
			"the claim is that pins govern here, not that any exist")
	}
}

// TestARecordedSourceThreadsEveryPinMap proves the source carries the maps
// through rather than merely carrying the posture. A source that named the right
// posture and dropped the pins would pass the test above and resolve every
// plugin unverified.
func TestARecordedSourceThreadsEveryPinMap(t *testing.T) {
	pins := PinMaps{
		Pins:          map[string]string{"oci://p": "sha256:digest"},
		PinVersions:   map[string]string{"oci://p": "1.2.0"},
		BundleDigests: map[string]string{"oci://p": "sha256:bundle"},
	}

	r := RecordedPins(pins).Resolver(ResolutionContext{})

	if got := r.pins["oci://p"]; got != "sha256:digest" {
		t.Errorf("digest pin did not reach the resolver: got %q", got)
	}
	if got := r.pinVersions["oci://p"]; got != "1.2.0" {
		t.Errorf("resolved version did not reach the resolver, so the versioned cache slot is unaddressed: got %q", got)
	}
	if got := r.bundleDigests["oci://p"]; got != "sha256:bundle" {
		t.Errorf("signature-bundle digest did not reach the resolver: got %q", got)
	}
}

// TestAPinFreeSourceCarriesNoPinsWhateverItIsHanded holds the pin-free base to
// its documented contract: the maps are ignored under that posture, so a site
// that declared itself pin-free cannot carry a pin by accident.
func TestAPinFreeSourceCarriesNoPinsWhateverItIsHanded(t *testing.T) {
	// NoPins takes no argument, so the only way this could regress is the
	// posture switch itself. Driving NewResolver directly is what exercises it.
	r := NewResolver(ResolutionContext{}, PinPostureNone, PinMaps{
		Pins:        map[string]string{"oci://p": "sha256:digest"},
		PinVersions: map[string]string{"oci://p": "1.2.0"},
	})

	if r.pins != nil || r.pinVersions != nil || r.bundleDigests != nil {
		t.Error("the pin-free base threaded pins it was handed; a site that declared itself pin-free is carrying a pin")
	}
	if r.expectsRecordedIdentity {
		t.Error("the pin-free base expects a recorded identity")
	}
}
