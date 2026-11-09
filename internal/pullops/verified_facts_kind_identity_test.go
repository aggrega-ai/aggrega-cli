// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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

// TestAFactAPassLearnsAboutAnAggregateStaysOffTheManifestOfTheSameName holds the
// signature verification and the deprecation-marker discovery to recording what they
// learn about a registry-served Aggregate on that Aggregate's lockfile row and on no
// row of another kind that shares its name.
//
// The Release deploys an Aggregate fetched from a registry, signed and carrying a
// signed deprecation marker, and a Manifest of the same name read from beside the
// Release. The lockfile pins the two as two rows of one reference. The pass asks the
// registry about the Aggregate only, so its answers belong to the Aggregate's row: the
// Manifest's row, a different document the registry was never asked about, must
// record neither the Aggregate's signature nor its marker.
func TestAFactAPassLearnsAboutAnAggregateStaysOffTheManifestOfTheSameName(t *testing.T) {
	t.Parallel()
	const (
		host   = "keyed.example.com"
		shared = "imported/flow"
		marker = "withdrawn: superseded by imported/flow 2"
	)
	ctx := context.Background()
	store := memory.New()
	aggregateBody := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: imported/flow\n")
	aggregateDg := digest.ContentDigest(aggregateBody)
	manifestDg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeAggregate, aggregateBody, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	manifestBody := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: imported/flow\n")
	manifestContentDg := digest.ContentDigest(manifestBody)

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
	if _, err := registryindex.AttachSignedMarker(ctx, io.Discard, store, "1.0.0", host+"/aggregate/imported/flow:1.0.0",
		deprecation.Marker{Severity: deprecation.SeverityWarning, Reason: marker, IssuedOn: "2026-10-01T00:00:00Z"}, signer); err != nil {
		t.Fatalf("AttachSignedMarker: %v", err)
	}
	// The marker is an untagged referrer, and verifying its own signature lists
	// referrers of the marker's digest. A registry resolves a manifest by digest
	// while this in-memory store resolves only what it has tagged, so tag the marker
	// by its digest — a fidelity shim for the store.
	markers, err := oci.ListReferrers(ctx, store, "1.0.0", deprecation.ArtifactType)
	if err != nil || len(markers) == 0 {
		t.Fatalf("marker not discoverable after attach: %v (%d found)", err, len(markers))
	}
	for _, m := range markers {
		if terr := store.Tag(ctx, m, m.Digest.String()); terr != nil {
			t.Fatalf("tag marker by digest: %v", terr)
		}
	}
	wsCfg := workspaceconfig.Config{
		DefaultRegistry: host,
		Registries: []workspaceconfig.RegistryConfig{{
			Host:      host,
			Signing:   workspaceconfig.SigningConfig{Required: true},
			PublicKey: string(pubPEM),
		}},
	}

	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: shared, KindHint: closure.ReleaseAggregateKindHint, ResolvedVersion: "1.0.0", Digest: aggregateDg,
			ManifestDigest: manifestDg, ResolutionPath: closure.ResolutionPathRegistry},
		{URI: shared, KindHint: closure.KindManifest, Digest: manifestContentDg, ResolutionPath: closure.ResolutionPathExternal},
	}}
	lf := &lockfile.Lockfile{Entities: []lockfile.Entity{
		{Kind: closure.KindAggregate, URI: shared, ResolvedVersion: "1.0.0", Digest: aggregateDg, ManifestDigest: manifestDg},
		{Kind: closure.KindManifest, URI: shared, Digest: manifestContentDg},
	}}
	row := func(kind string) *lockfile.Entity {
		for i := range lf.Entities {
			if lf.Entities[i].Kind == kind {
				return &lf.Entities[i]
			}
		}
		t.Fatalf("no %s row for %s", kind, shared)
		return nil
	}

	if err := VerifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), ".aggrega/cache", io.Discard, pluginTargetFor(store, "1.0.0")); err != nil {
		t.Fatalf("VerifyClosureSignatures: %v", err)
	}

	aggregate, manifest := row(closure.KindAggregate), row(closure.KindManifest)
	if aggregate.Signature == nil || aggregate.Signature.Mode != "keyed" || aggregate.Signature.BundleDigest == "" {
		t.Fatalf("the Aggregate's row records no keyed signature with a cached bundle: %+v", aggregate.Signature)
	}
	if aggregate.Deprecation == nil || aggregate.Deprecation.Message != marker {
		t.Fatalf("the Aggregate's row records no deprecation marker %q: %+v", marker, aggregate.Deprecation)
	}
	if manifest.Signature != nil {
		t.Errorf("the Manifest's row took the signature the pass verified for the Aggregate of its name: %+v", *manifest.Signature)
	}
	if manifest.Deprecation != nil {
		t.Errorf("the Manifest's row took the deprecation marker the pass discovered for the Aggregate of its name: %+v", *manifest.Deprecation)
	}
}
