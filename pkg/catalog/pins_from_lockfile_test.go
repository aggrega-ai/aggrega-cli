// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestPinsFromLockfileKeepsEveryRowUnderTheKindItsSectionNames holds the lockfile's
// pin projection to the sections it reads, by kind. Every section pins the same
// reference, each row at a digest of its own, so a projection keyed by reference
// alone would keep one pin where the lockfile records nine rows. Each row the
// cache is read for is kept under the published kind its slot is kept under — an
// entity row's own kind, a library row's axis, a plugin row as the Plugin's
// executable — with its own version and digest. The Plugin entity's row and its
// executable's row share both kind and reference, and are still two pins: the
// executable's is marked as one. The Manifest row is the one left out: a
// Manifest is never read from a cache slot. Source artifacts are excluded, because
// they are held on their own read-time gate rather than by these pins.
func TestPinsFromLockfileKeepsEveryRowUnderTheKindItsSectionNames(t *testing.T) {
	t.Parallel()
	const ref = "core/shared:1.0.0"
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{Kind: closure.KindComponent, URI: ref, ResolvedVersion: "1.0.0", Digest: "sha256:aaa"},
			{Kind: closure.KindManifest, URI: ref, ResolvedVersion: "1.0.0", Digest: "sha256:abb"},
			{Kind: closure.KindRecipe, URI: ref, ResolvedVersion: "1.0.1", Digest: "sha256:acc"},
			{Kind: closure.KindPlugin, URI: ref, ResolvedVersion: "1.0.7", Digest: "sha256:add"},
		},
		SchemaLibraries:   []lockfile.SchemaLibrary{{URI: ref, ResolvedVersion: "1.0.2", Digest: "sha256:bbb"}},
		MappingLibraries:  []lockfile.MappingLibrary{{URI: ref, ResolvedVersion: "1.0.3", Digest: "sha256:ccc"}},
		WorkloadLibraries: []lockfile.WorkloadLibrary{{URI: ref, ResolvedVersion: "1.0.4", Digest: "sha256:ddd"}},
		ArtifactLibraries: []lockfile.ArtifactLibrary{{URI: ref, ResolvedVersion: "1.0.5", Digest: "sha256:eee"}},
		Plugins:           []lockfile.Plugin{{URI: ref, ResolvedVersion: "1.0.6", Digest: "sha256:fff"}},
		SourceArtifacts: []lockfile.SourceArtifact{
			{Kind: "helm-oci-chart", URL: "oci://example/chart", Digest: "sha256:ggg"},
		},
	}

	// The published kinds are named here as a reader of the cache sees them, so a
	// projection that derived a wrong one would not agree with itself by accident.
	// The executable's key is spelled out rather than built by PluginExecutablePin,
	// deliberately: built by the constructor under test, a constructor that stopped
	// marking it would collapse the expected set exactly as it collapses the
	// projection, and the two would agree.
	want := Pins{
		{Kind: "Component", URI: ref}:                {ResolvedVersion: "1.0.0", Digest: "sha256:aaa"},
		{Kind: "Recipe", URI: ref}:                   {ResolvedVersion: "1.0.1", Digest: "sha256:acc"},
		{Kind: "SchemaLibrary", URI: ref}:            {ResolvedVersion: "1.0.2", Digest: "sha256:bbb"},
		{Kind: "MappingLibrary", URI: ref}:           {ResolvedVersion: "1.0.3", Digest: "sha256:ccc"},
		{Kind: "WorkloadLibrary", URI: ref}:          {ResolvedVersion: "1.0.4", Digest: "sha256:ddd"},
		{Kind: "ArtifactLibrary", URI: ref}:          {ResolvedVersion: "1.0.5", Digest: "sha256:eee"},
		{Kind: "Plugin", URI: ref}:                   {ResolvedVersion: "1.0.7", Digest: "sha256:add"},
		{Kind: "Plugin", URI: ref, Executable: true}: {ResolvedVersion: "1.0.6", Digest: "sha256:fff"},
	}
	if len(want) != 8 {
		t.Fatalf("fixture: the expected pin set holds %d keys; want 8, one per row the cache is read for", len(want))
	}
	got, err := PinsFromLockfile(lf)
	if err != nil {
		t.Fatalf("PinsFromLockfile refused a lockfile every row of which records a declared kind: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		for _, k := range want.sortedKeys() {
			if got[k] != want[k] {
				t.Errorf("pin %v: got %+v; want %+v", k, got[k], want[k])
			}
		}
		for _, k := range got.sortedKeys() {
			if _, ok := want[k]; !ok {
				t.Errorf("pin %v is projected and no row of the lockfile is pinned under it", k)
			}
		}
	}
}

// TestPinsFromLockfileRefusesARowWhoseKindNamesNoPublishedKind holds the projection
// to its refusal: an entity row whose kind no cache slot is kept under is never
// passed over, but halts as an internal fault naming the row, while a Manifest row —
// a kind no slot holds — is left out without a fault.
func TestPinsFromLockfileRefusesARowWhoseKindNamesNoPublishedKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		kind    string
		refused bool
	}{
		{name: "a Manifest row, which no slot holds", kind: closure.KindManifest},
		{name: "a row recording no kind", kind: "", refused: true},
		{name: "a row recording an undeclared kind", kind: "release_aggregate_typo", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lf := &lockfile.Lockfile{Entities: []lockfile.Entity{
				{Kind: closure.KindComponent, URI: "core/web:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:aaa"},
				{Kind: tc.kind, URI: "core/odd:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:bbb"},
			}}
			pins, err := PinsFromLockfile(lf)
			if !tc.refused {
				if err != nil {
					t.Fatalf("PinsFromLockfile refused a lockfile whose rows it can key: %v", err)
				}
				if len(pins) != 1 {
					t.Errorf("pins = %v; want only the Component's", pins)
				}
				return
			}
			if err == nil {
				t.Fatalf("PinsFromLockfile passed over a row of kind %q; it must refuse it", tc.kind)
			}
			if !strings.Contains(err.Error(), `"core/odd:1.0.0"`) || !strings.Contains(err.Error(), "internal") {
				t.Errorf("the refusal must name the row and say the fault is internal; got: %v", err)
			}
		})
	}
}
