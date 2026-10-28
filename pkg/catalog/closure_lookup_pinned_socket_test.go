// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestClosureLookupResolvesAPinnedSocketEntityAtTheBinarysOwnVersion proves the
// resolution a pinned workspace-socket attachment takes — a governance attachment at
// a transform role — answers a governance entity at the version the binary carries
// from the binary, with no registry access, while the general cascade the same
// reference takes from anywhere else still leaves it to the registry.
//
// The needle is a REAL embedded governance identity at the REAL version the binary
// carries, over an empty catalog, so a resolution from the binary is the only way the
// first row can resolve. The second row is the control on scope: the same reference
// through the general cascade must miss and must have asked the registry, which is
// also what calibrates the fetch counter the first row reads as zero. The third row
// keeps the refusal of a version neither source carries, and the fourth shows the
// tier is the one the build's plugin lookup reads, switched by the same view setting.
func TestClosureLookupResolvesAPinnedSocketEntityAtTheBinarysOwnVersion(t *testing.T) {
	t.Parallel()
	identity, version := embeddedOutputShapingIdentity(t)
	raw, ok := embedded.LookupEntity(identity)
	if !ok {
		t.Fatalf("%q carries no embedded entity; every row would be vacuous", identity)
	}

	cases := []struct {
		name string
		uri  string
		// pinnedSocket asks through the pinned-socket resolution; false asks the
		// general cascade as a Plugin entity reference.
		pinnedSocket bool
		// embeddedResolution switches the view's embedded resolution on, as every
		// production view has it.
		embeddedResolution bool
		wantEmbedded       bool
		wantFetch          bool
	}{
		{"the binary's own version, as a pinned socket entity", identity + ":" + version, true, true, true, false},
		{"the same reference through the general cascade", identity + ":" + version, false, true, false, true},
		{"a version neither the catalog nor the binary carries", identity + ":9.9.9", true, true, false, true},
		{"a view with embedded resolution off", identity + ":" + version, true, false, false, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cat, err := catalog.LoadAll("", dir)
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			if tc.embeddedResolution {
				cat.UseEmbeddedProviderResolution()
			}
			puller := &countingMissPuller{}
			lookup := cat.ClosureLookup(puller, dir, ".aggrega/cache")

			var res closure.Resolution
			if tc.pinnedSocket {
				res, err = closure.ResolvePinnedSocketEntity(context.Background(), lookup, tc.uri)
			} else {
				res, err = lookup.ResolveCatalogEntity(context.Background(), tc.uri, "plugin")
			}
			if err != nil {
				t.Fatalf("resolve %q: %v", tc.uri, err)
			}

			if !tc.wantEmbedded {
				if res.ResolutionPath == closure.ResolutionPathEmbedded {
					t.Fatalf("%q resolved from the binary; want it left to the registry, which serves nothing here", tc.uri)
				}
				if !res.NotFound {
					t.Errorf("%q: got resolution %+v; want NotFound", tc.uri, res)
				}
			} else {
				if res.NotFound || res.ResolutionPath != closure.ResolutionPathEmbedded {
					t.Fatalf("%q: got %+v; want it resolved from the binary, which carries this version", tc.uri, res)
				}
				if res.ResolvedVersion != version {
					t.Errorf("ResolvedVersion: got %q; want the version the binary carries, %q", res.ResolvedVersion, version)
				}
				if want := oci.DocumentDigest(raw); res.Digest != want {
					t.Errorf("Digest: got %q; want the digest of the entity the binary carries, %q", res.Digest, want)
				}
				if res.ManifestDigest != "" {
					t.Errorf("ManifestDigest: got %q; want empty — the binary carries no registry manifest", res.ManifestDigest)
				}
			}
			if tc.wantFetch && puller.calls == 0 {
				t.Errorf("%q: the registry was never consulted, so this row proves nothing about where the reference went", tc.uri)
			}
			if !tc.wantFetch && puller.calls != 0 {
				t.Errorf("%q: registry fetches %d; want 0 — the entity is in the binary", tc.uri, puller.calls)
			}
		})
	}
}
