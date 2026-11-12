// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"context"
	"io"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	sameBytesHost      = "keyed.example.com"
	sameBytesBare      = "test/svc"
	sameBytesVersioned = "test/svc:1.0.0"
	// sameBytesOther is a second Component the content cache serves, whose bytes
	// this run fetched under no reference: the pass asks nothing about it.
	sameBytesOther = "test/other:1.0.0"
)

// TestTheFactsAPassVerifiesReachEveryRowPinningTheSameBytes holds the signature
// verification and the deprecation-marker discovery to recording what they learn
// about an artifact on every lockfile row that pins it.
//
// One Component is referenced twice in one Release — once without its version
// (an Aggregate type) and once with it (a Manifest mapping key) — so the lockfile
// pins it under two rows with one content digest and one manifest digest. Only the
// reference the pull fetched from the registry is asked about; the other resolves,
// in the same pull, from the cache slot that fetch wrote. Both facts are read off the
// manifest the two rows share, so a row whose reference was not the one asked must
// still carry what this pull established for its bytes.
//
// The rows differ in which reference was fetched, whether the artifact is signed,
// and whether the served row already holds a signature carried from an earlier
// lockfile. A row for different bytes the pass never asked about keeps what was
// carried onto it in every case.
func TestTheFactsAPassVerifiesReachEveryRowPinningTheSameBytes(t *testing.T) {
	t.Parallel()
	carried := lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried-from-the-prior-lockfile", BundleDigest: "sha256:carried"}
	cases := []struct {
		name    string
		fetched string
		served  string
		signed  bool
		// carriedOnServed seeds the served row with a signature an earlier lockfile
		// recorded, as the carry-forward does before the pass runs.
		carriedOnServed bool
		// wantFact is whether both rows end up holding this run's verified facts;
		// false means both end up holding no signature at all.
		wantFact bool
	}{
		{
			name:    "the reference without its version is fetched and the versioned one is served from what it cached",
			fetched: sameBytesBare, served: sameBytesVersioned, signed: true, wantFact: true,
		},
		{
			name:    "the versioned reference is fetched and the one without its version is served from what it cached",
			fetched: sameBytesVersioned, served: sameBytesBare, signed: true, wantFact: true,
		},
		{
			name:    "a signature carried onto the served row is replaced by this run's answer for the same bytes",
			fetched: sameBytesBare, served: sameBytesVersioned, signed: true, carriedOnServed: true, wantFact: true,
		},
		{
			name:    "an unsigned artifact clears a signature carried onto the served row, as it would on the fetched one",
			fetched: sameBytesBare, served: sameBytesVersioned, signed: false, carriedOnServed: true, wantFact: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := memory.New()
			body := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: test/svc\n")
			contentDg := digest.ContentDigest(body)
			manifestDg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, body, nil)
			if err != nil {
				t.Fatalf("Push: %v", err)
			}
			otherBody := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: test/other\n")
			otherContentDg := digest.ContentDigest(otherBody)
			otherManifestDg, err := oci.Push(ctx, memory.New(), "1.0.0", oci.MediaTypeComponent, otherBody, nil)
			if err != nil {
				t.Fatalf("Push the other Component: %v", err)
			}

			wsCfg := workspaceconfig.Config{DefaultRegistry: sameBytesHost}
			const marker = "withdrawn: superseded by test/svc 2"
			if tc.signed {
				privPEM, pubPEM, err := signing.GenerateKeyedPEM()
				if err != nil {
					t.Fatalf("GenerateKeyedPEM: %v", err)
				}
				signer, err := signing.LoadKeyedSigner(privPEM, nil)
				if err != nil {
					t.Fatalf("LoadKeyedSigner: %v", err)
				}
				bundle, artifactType, err := signer.Sign(ctx, manifestDg)
				if err != nil {
					t.Fatalf("Sign: %v", err)
				}
				if _, err := oci.AttachReferrer(ctx, store, "1.0.0", artifactType, bundle); err != nil {
					t.Fatalf("AttachReferrer: %v", err)
				}
				if _, err := registryindex.AttachSignedMarker(ctx, io.Discard, store, "1.0.0",
					sameBytesHost+"/component/test/svc:1.0.0",
					deprecation.Marker{Severity: deprecation.SeverityWarning, Reason: marker, IssuedOn: "2026-10-01T00:00:00Z"}, signer); err != nil {
					t.Fatalf("AttachSignedMarker: %v", err)
				}
				// The marker is an untagged referrer, and verifying its own signature lists
				// referrers of the marker's digest. A registry resolves a manifest by digest
				// while this in-memory store resolves only what it has tagged, so tag the
				// marker by its digest — a fidelity shim for the store.
				markers, err := oci.ListReferrers(ctx, store, "1.0.0", deprecation.ArtifactType)
				if err != nil || len(markers) == 0 {
					t.Fatalf("marker not discoverable after attach: %v (%d found)", err, len(markers))
				}
				for _, m := range markers {
					if terr := store.Tag(ctx, m, m.Digest.String()); terr != nil {
						t.Fatalf("tag marker by digest: %v", terr)
					}
				}
				wsCfg.Registries = []workspaceconfig.RegistryConfig{{
					Host:      sameBytesHost,
					Signing:   workspaceconfig.SigningConfig{Required: true},
					PublicKey: string(pubPEM),
				}}
			}

			pathFor := func(uri string) string {
				if uri == tc.fetched {
					return closure.ResolutionPathRegistry
				}
				return closure.ResolutionPathCache
			}
			cl := closure.Closure{Entities: []closure.ResolvedRef{
				{URI: sameBytesBare, KindHint: "component", Digest: contentDg, ManifestDigest: manifestDg, ResolutionPath: pathFor(sameBytesBare)},
				{URI: sameBytesVersioned, KindHint: "component", Digest: contentDg, ManifestDigest: manifestDg, ResolutionPath: pathFor(sameBytesVersioned)},
				{URI: sameBytesOther, KindHint: "component", Digest: otherContentDg, ManifestDigest: otherManifestDg, ResolutionPath: closure.ResolutionPathCache},
			}}
			// The row for the other bytes carries a fact a build accepts — its bundle in the
			// cache, made for its own manifest — so nothing about it has the pass ask.
			root := t.TempDir()
			otherCarried := lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried-from-the-prior-lockfile",
				BundleDigest: cacheBundleMadeFor(t, root, otherManifestDg)}
			otherSig := otherCarried
			lf := &lockfile.Lockfile{Entities: []lockfile.Entity{
				{Kind: "component", URI: sameBytesBare, Digest: contentDg, ManifestDigest: manifestDg},
				{Kind: "component", URI: sameBytesVersioned, Digest: contentDg, ManifestDigest: manifestDg},
				{Kind: "component", URI: sameBytesOther, Digest: otherContentDg, ManifestDigest: otherManifestDg, Signature: &otherSig},
			}}
			row := func(uri string) *lockfile.Entity {
				for i := range lf.Entities {
					if lf.Entities[i].URI == uri {
						return &lf.Entities[i]
					}
				}
				t.Fatalf("no lockfile row for %s", uri)
				return nil
			}
			if tc.carriedOnServed {
				servedSig := carried
				row(tc.served).Signature = &servedSig
			}

			if err := VerifyClosureSignatures(ctx, lf, cl, wsCfg, root, ".aggrega/cache", io.Discard, pluginTargetFor(store, "1.0.0")); err != nil {
				t.Fatalf("VerifyClosureSignatures: %v", err)
			}

			fetched, served := row(tc.fetched), row(tc.served)
			if !tc.wantFact {
				if fetched.Signature != nil || served.Signature != nil {
					t.Fatalf("the registry, asked this run about these bytes, attaches no signature; want neither row to claim one, got %s %+v and %s %+v",
						tc.fetched, fetched.Signature, tc.served, served.Signature)
				}
			} else {
				if fetched.Signature == nil || fetched.Signature.Mode != "keyed" || fetched.Signature.BundleDigest == "" {
					t.Fatalf("the fetched row %s records no keyed signature with a cached bundle: %+v", tc.fetched, fetched.Signature)
				}
				if served.Signature == nil {
					t.Fatalf("the row %s pins the bytes this pull verified under %s and records no signature", tc.served, tc.fetched)
				}
				if *served.Signature != *fetched.Signature {
					t.Errorf("the two rows pinning one manifest record different signature facts: %s %+v, %s %+v",
						tc.fetched, *fetched.Signature, tc.served, *served.Signature)
				}
				if fetched.Deprecation == nil || fetched.Deprecation.Message != marker {
					t.Fatalf("the fetched row %s records no deprecation marker %q: %+v", tc.fetched, marker, fetched.Deprecation)
				}
				if served.Deprecation == nil {
					t.Fatalf("the row %s pins the bytes whose deprecation marker this pull discovered under %s and records none", tc.served, tc.fetched)
				}
				if served.Deprecation.Message != fetched.Deprecation.Message || served.Deprecation.Severity != fetched.Deprecation.Severity {
					t.Errorf("the two rows pinning one manifest record different deprecation markers: %s %+v, %s %+v",
						tc.fetched, *fetched.Deprecation, tc.served, *served.Deprecation)
				}
			}

			other := row(sameBytesOther)
			if other.Signature == nil || *other.Signature != otherCarried {
				t.Errorf("the row %s, whose bytes this run asked nothing about, lost what was carried onto it: %+v", sameBytesOther, other.Signature)
			}
			if other.Deprecation != nil {
				t.Errorf("the row %s, whose bytes this run asked nothing about, gained a deprecation marker: %+v", sameBytesOther, other.Deprecation)
			}
		})
	}
}
