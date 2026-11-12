// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust/livetrusttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// liveRootHost is the registry the entities of the shared-root test are pulled
// from. Its policy verifies keyless signatures against the live public trust root,
// which the test serves itself (livetrusttest).
const liveRootHost = "live-root.example.com"

// TestAPullVerifiesEverySignatureAgainstOneFetchOfTheLiveRoot runs the pull's
// signature pass — and the deprecation-marker pass it runs at the same moment —
// under the context one run of the resolution is carried out under (runContext),
// over more keyless-signed entities than the pass asks about at once, one of them
// carrying a withdrawal its own publisher signed. Every signature and the
// withdrawal's own signature verify against the live public trust root, which the
// test serves, and the run must fetch it once.
//
// Planted defects and what fails (the fetch count, unless stated):
//   - a run context sharing no root, so the signature pass and the marker pass each
//     share one fetch of their own: two fetches;
//   - a pass that starts a shared root of its own even under the run's (a fresh
//     livetrust.WithSharedRoot where livetrust.EnsureSharedRoot stands): two
//     fetches;
//   - no sharing at all: a fetch per verification;
//   - a shared root that skips a check: the withdrawal or a signature is not
//     recorded, which the assertions on the rows catch.
func TestAPullVerifiesEverySignatureAgainstOneFetchOfTheLiveRoot(t *testing.T) {
	// Not parallel: the repository every live fetch reads is the test binary's.
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	rootDoc, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	liveRoot := livetrusttest.Serve(t, rootDoc)

	ctx := context.Background()
	const entities = 2*oci.RegistryRequestsAtOnce + 1
	stores := make(map[string]*memory.Store, entities)
	var cl closure.Closure
	lf := &lockfile.Lockfile{}
	for i := range entities {
		store := memory.New()
		body := []byte(fmt.Sprintf("kind: Component\nmetadata:\n  name: core/svc-%02d\n", i))
		contentDg := digest.ContentDigest(body)
		manifestDg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, body, nil)
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		// Logged a minute after each signature is made, inside the minutes its
		// in-memory signing certificate is valid for however long the setup takes.
		sig, err := authority.Sign(earliestPublisher, publisherIssuer, manifestDg, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatalf("keyless Sign: %v", err)
		}
		if _, err := oci.AttachReferrer(ctx, store, "1.0.0", signing.BundleMediaType, sig.Bundle); err != nil {
			t.Fatalf("attach the signature: %v", err)
		}
		uri := fmt.Sprintf("core/svc-%02d", i)
		stores[uri] = store
		cl.Entities = append(cl.Entities, closure.ResolvedRef{URI: uri, ResolvedVersion: "1.0.0", KindHint: "component",
			Digest: contentDg, ManifestDigest: manifestDg, ResolutionPath: closure.ResolutionPathRegistry})
		lf.Entities = append(lf.Entities, lockfile.Entity{Kind: "component", URI: uri, ResolvedVersion: "1.0.0",
			Digest: contentDg, ManifestDigest: manifestDg})
	}
	// The first entity carries a withdrawal its own publisher signed, so the marker
	// pass verifies a signature against the live root too.
	withdrawn := cl.Entities[0].URI
	marker := deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: "withdrawn by its publisher", IssuedOn: "2026-05-01T00:00:00Z"}
	if _, err := registryindex.AttachSignedMarker(ctx, io.Discard, stores[withdrawn], "1.0.0",
		liveRootHost+"/component/"+withdrawn+":1.0.0", marker,
		keylessSignerFor{authority: authority, identity: earliestPublisher, at: time.Now().Add(2 * time.Minute)}); err != nil {
		t.Fatalf("AttachSignedMarker: %v", err)
	}
	markers, err := oci.ListReferrers(ctx, stores[withdrawn], "1.0.0", deprecation.ArtifactType)
	if err != nil || len(markers) != 1 {
		t.Fatalf("the marker is not discoverable: %v (%d found)", err, len(markers))
	}
	// The in-memory store resolves only what it has tagged, and a marker's own
	// signatures are listed by the marker's digest.
	if err := stores[withdrawn].Tag(ctx, markers[0], markers[0].Digest.String()); err != nil {
		t.Fatalf("tag the marker by digest: %v", err)
	}

	cfg := workspaceconfig.Config{
		DefaultRegistry: liveRootHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host:              liveRootHost,
			Signing:           workspaceconfig.SigningConfig{Required: true},
			TrustedIdentities: []workspaceconfig.TrustedIdentity{{Identity: earliestPublisher, Issuer: publisherIssuer}},
		}},
	}
	targetFor := func(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		return stores[e.URI], "1.0.0", nil
	}
	if err := VerifyClosureSignatures(runContext(ctx), lf, cl, cfg, t.TempDir(), ".aggrega/cache", io.Discard, targetFor); err != nil {
		t.Fatalf("VerifyClosureSignatures: %v", err)
	}

	for _, row := range lf.Entities {
		if row.Signature == nil || row.Signature.Mode != "keyless" || row.Signature.Identity != earliestPublisher {
			t.Errorf("%s records %+v; want the keyless signature of %s", row.URI, row.Signature, earliestPublisher)
		}
	}
	if lf.Entities[0].Deprecation == nil || lf.Entities[0].Deprecation.Signer == nil ||
		lf.Entities[0].Deprecation.Signer.Identity != earliestPublisher {
		t.Errorf("the withdrawal its publisher signed is not recorded on %s: %+v", withdrawn, lf.Entities[0].Deprecation)
	}
	if fetched := liveRoot.Fetches(); fetched != 1 {
		t.Errorf("the run verified %d signatures and a withdrawal against the live trust root and fetched it %d times; want once for the run",
			entities, fetched)
	}
}
