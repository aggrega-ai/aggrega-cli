// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestAPinnedSocketEntityTheBinaryCarriesResolvesFromTheBinaryOverItsCachedCopy holds
// the resolution of a pinned workspace-socket entity — a governance attachment at a
// transform role — to one source on both sides of a lockfile. A pull resolves the
// binary's own version from the binary and records it so: the digest of the document
// the binary carries, and no manifest digest. A build over that lockfile reads the
// same reference through a view whose fold has placed a copy a registry served under
// that pin, and the copy must not answer ahead of the binary, or the build reports
// the row as served by the content cache while the pull reported it served by the
// binary, and holds it to a signature the pull had no reason to record.
//
// The subject is a REAL embedded governance entity at the REAL version the binary
// carries, cached under the pin the pull writes for it, so the copy in the cache is
// exactly what a build finds there. The second row is the control that the copy is
// in the view at all: the general cascade, which keeps the binary for core providers
// alone, answers the same reference from the cache — without it the first row could
// pass for want of a copy to prefer. The third row keeps the cache answering a pinned
// socket entity the binary does not carry, so the order moves for the binary's own
// entities and no other.
func TestAPinnedSocketEntityTheBinaryCarriesResolvesFromTheBinaryOverItsCachedCopy(t *testing.T) {
	t.Parallel()
	identity, version, raw := embeddedGovernanceEntity(t)
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"

	carriedURI := identity + ":" + version
	const notCarriedURI = "pipeline-step/pulled-step:1.0.0"

	carriedPin := writePinnedSlot(t, ws, cacheDir, carriedURI, oci.MediaTypePlugin, raw)
	carriedHeld := carriedPin.ManifestDigest
	// The row a pull writes for an entity it resolved from the binary records the
	// binary's document digest and no manifest digest.
	carriedPin.ManifestDigest = ""
	if want := oci.DocumentDigest(raw); carriedPin.Digest != want {
		t.Fatalf("the cached copy's row digest %q is not the binary's document digest %q; the slot would not hold the pin a pull writes", carriedPin.Digest, want)
	}
	notCarriedPin := writePinnedSlot(t, ws, cacheDir, notCarriedURI, oci.MediaTypePlugin, []byte(provenancePulledPluginYAML))
	pins := Pins{
		{Kind: "Plugin", URI: carriedURI}:    carriedPin,
		{Kind: "Plugin", URI: notCarriedURI}: notCarriedPin,
	}

	cases := []struct {
		name         string
		uri          string
		pinnedSocket bool
		wantPath     string
		wantDigest   string
		wantManifest string
	}{
		{"the binary's own version, cached and pinned, as a pinned socket entity", carriedURI, true,
			closure.ResolutionPathEmbedded, oci.DocumentDigest(raw), ""},
		{"control: the same reference through the general cascade", carriedURI, false,
			closure.ResolutionPathCache, carriedPin.Digest, carriedHeld},
		{"an entity the binary does not carry, cached and pinned, as a pinned socket entity", notCarriedURI, true,
			closure.ResolutionPathCache, notCarriedPin.Digest, notCarriedPin.ManifestDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := NewLazyLookup(ws, cacheDir, nil, nil)
			view.SetPins(pins)
			lookup := view.ClosureLookup(nil, ws, cacheDir, nil, nil, &ReuseVerification{PriorPins: pins})
			var res closure.Resolution
			var err error
			if tc.pinnedSocket {
				res, err = closure.ResolvePinnedSocketEntity(context.Background(), lookup, tc.uri)
			} else {
				res, err = lookup.ResolveCatalogEntity(context.Background(), tc.uri, closure.KindPlugin)
			}
			if err != nil {
				t.Fatalf("resolve %q: %v", tc.uri, err)
			}
			if res.NotFound {
				t.Fatalf("%q did not resolve, so its source cannot be compared", tc.uri)
			}
			if res.ResolutionPath != tc.wantPath {
				t.Fatalf("ResolutionPath = %q; want %q", res.ResolutionPath, tc.wantPath)
			}
			if res.Digest != tc.wantDigest {
				t.Errorf("Digest = %q; want %q", res.Digest, tc.wantDigest)
			}
			if res.ManifestDigest != tc.wantManifest {
				t.Errorf("ManifestDigest = %q; want %q", res.ManifestDigest, tc.wantManifest)
			}
		})
	}
}

// embeddedGovernanceEntity returns the identity, the version and the document of one
// governance plugin entity this binary carries, read out of the binary itself, so the
// version moves with every build rather than being written down. A binary built
// without its core plugins carries none, and the test skips there rather than
// asserting over an empty set. It deliberately repeats the selection of
// embeddedOutputShapingIdentity, which lives in the external test package this one
// cannot import, and returns the document as well.
func embeddedGovernanceEntity(t *testing.T) (identity, version string, raw []byte) {
	t.Helper()
	embeds, _ := embeddedPluginEntities()
	for _, p := range embeds {
		if plugintype.Type(p.Metadata.PluginType) != plugintype.Governance || p.Metadata.Version == "" {
			continue
		}
		if doc, ok := embedded.LookupEntity(p.Coordinate()); ok {
			return p.Coordinate(), p.Metadata.Version, doc
		}
	}
	t.Skip("this binary carries no core governance entities")
	return "", "", nil
}
