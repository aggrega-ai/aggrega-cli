// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && !light

package embedded

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// TestContentDigestsMatchGuestDerivation asserts the embedded-identity accessor
// reports exactly the content digest each guest derives to — the same derivation
// every content-addressing consumer (the loader, the content-addressed caches)
// performs on the decompressed guest bytes. Covering the full embedded set
// (URIs), not only the entity-carrying subset, because a describe-cache entry is
// keyed by the guest's content digest whether or not that guest ships an entity.
// It is order-aligned with URIs so the set is deterministic.
func TestContentDigestsMatchGuestDerivation(t *testing.T) {
	uris := URIs()
	got := ContentDigests()
	if len(got) != len(uris) {
		t.Fatalf("ContentDigests count = %d; want %d (one per embedded URI)", len(got), len(uris))
	}
	for i, u := range uris {
		b, ok := Lookup(u)
		if !ok {
			t.Fatalf("Lookup miss for embedded URI %q", u)
		}
		want := digest.ContentDigest(b)
		if got[i] != want {
			t.Errorf("ContentDigests[%d] for %q = %q; want %q", i, u, got[i], want)
		}
	}
}

// TestContentDigestsDeterministic asserts repeated calls return byte-identical,
// order-stable output (Deterministic Output invariant).
func TestContentDigestsDeterministic(t *testing.T) {
	a, b := ContentDigests(), ContentDigests()
	if len(a) != len(b) {
		t.Fatalf("length drift: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("order/content drift at %d: %q vs %q", i, a[i], b[i])
		}
	}
}

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
		"governance/label-injector",
		"governance/selector-tagger",
		"governance/rego",
		"governance/cel",
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
