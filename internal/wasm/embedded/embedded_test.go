// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && !light

package embedded

import "testing"

// TestManifestDerivedURIsAreStable pins the exact bare identities the embedded
// set registers under. The registry keys are load-bearing: the plugin-resolution
// cascade's embed tier and every Plugin entity's bare identity (metadata.name)
// resolve against these exact strings, so a change here is a resolution-behavior
// change, not a refactor. The list is asserted verbatim rather than derived so a
// manifest edit that alters a key fails loudly here.
func TestManifestDerivedURIsAreStable(t *testing.T) {
	want := []string{
		"provider/helm",
		"provider/compose",
		"provider/helm-chart",
		"provider/terraform",
		"provider/cloudformation",
		"linter/publishing",
		"parser/hcl",
		"parser/toml",
		"parser/yaml",
		"parser/json",
		"mutator/label-injector",
		"mutator/selector-tagger",
	}
	got := embeddedArtifacts()
	if len(got) != len(want) {
		t.Fatalf("embedded artifact count = %d; want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].uri != w {
			t.Errorf("artifact[%d].uri = %q; want %q", i, got[i].uri, w)
		}
	}
}

// TestPublishingLinterURIMatchesManifest guards the exported linter identity
// against the manifest-derived key. PublishingLinterURI is a const shared with
// the light build (which embeds no bytes and cannot read the manifest); the
// default build derives the same reference from the manifest. This test fails if
// the two ever diverge, so the shared identity stays single-sourced.
func TestPublishingLinterURIMatchesManifest(t *testing.T) {
	for _, a := range embeddedArtifacts() {
		if a.uri == PublishingLinterURI {
			return
		}
	}
	t.Fatalf("PublishingLinterURI %q not among manifest-derived embedded URIs", PublishingLinterURI)
}
