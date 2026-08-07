// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	libTestHost    = "keyed.example.com"
	libTestName    = "PortSpec"
	libTestVersion = "1.4.0"
	// libTestURI is the pinned form a floored reference threads into the closure:
	// the version is part of the identity rather than something the resolver chose.
	libTestURI = libTestName + ":" + libTestVersion
)

// signedLibraryStore publishes a library body to an in-memory store under its
// pinned version, signs the manifest digest with a fresh held key, attaches the
// bundle as a signature referrer, and returns the store, the manifest digest (the
// verify subject), the content digest (the cache/integrity pin), and a workspace
// config pinning the signer's key on the test host.
//
// The config carries default_registry, which is load-bearing rather than
// incidental: a library identity is host-less, so the registry host its signature
// referrers are discovered against is bound from the workspace registry. Without
// it there is no host, and the verify pass correctly skips the reference — which
// would make this test pass while proving nothing.
func signedLibraryStore(t *testing.T) (store *memory.Store, manifestDigest, contentDigest string, wsCfg workspaceconfig.Config, signer signing.Signer) {
	t.Helper()
	ctx := context.Background()
	store = memory.New()

	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\n")
	contentDigest = digest.ContentDigest(body)
	manifestDigest, err := oci.Push(ctx, store, libTestVersion, oci.MediaTypeSchemaLibrary, body, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err = signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, artifactType, err := signer.Sign(ctx, manifestDigest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, libTestVersion, artifactType, bundle); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}

	wsCfg = workspaceconfig.Config{
		DefaultRegistry: libTestHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host:      libTestHost,
			Signing:   workspaceconfig.SigningConfig{Required: true},
			PublicKey: string(pubPEM),
		}},
	}
	return store, manifestDigest, contentDigest, wsCfg, signer
}

// libraryClosureAndLockfile pairs a registry-resolved SchemaLibrary closure
// reference with the lockfile row it folds onto.
func libraryClosureAndLockfile(manifestDigest, contentDigest string) (closure.Closure, *lockfile.Lockfile) {
	cl := closure.Closure{
		SchemaLibraries: []closure.ResolvedLibrary{{
			URI:             libTestURI,
			ResolvedVersion: libTestVersion,
			Digest:          contentDigest,
			ManifestDigest:  manifestDigest,
			ResolutionPath:  "tier-3-pull",
		}},
	}
	lf := &lockfile.Lockfile{
		SchemaLibraries: []lockfile.SchemaLibrary{{
			URI:             libTestURI,
			ResolvedVersion: libTestVersion,
			Digest:          contentDigest,
			ManifestDigest:  manifestDigest,
		}},
	}
	return cl, lf
}

// TestVerifyClosureLibrarySignatureRecordsFact proves a library in the closure
// enters the verifiable set and verifies through the SAME pull-verify pass as a
// catalog entity, against the same per-registry trust policy, with the verified
// fact folding onto the lockfile library row.
//
// Before the library sections entered that set, a library reference was skipped
// silently: a signature-requiring registry admitted it unverified, and nothing
// said so — the least visible failure a trust policy can have, because a policy
// that does not apply looks exactly like a policy that passed.
func TestVerifyClosureLibrarySignatureRecordsFact(t *testing.T) {
	t.Parallel()
	store, manifestDg, contentDg, wsCfg, _ := signedLibraryStore(t)
	// The row carries the CONTENT digest as Digest (the cache/integrity pin) and the
	// MANIFEST digest as ManifestDigest (the verify subject). A verify that bound to
	// the wrong one would reject a legitimately-signed artifact, so driving them
	// distinctly is the anti-regression guard.
	if manifestDg == contentDg {
		t.Fatal("manifest and content digests coincide; the test cannot distinguish the verify subject")
	}

	cl, lf := libraryClosureAndLockfile(manifestDg, contentDg)

	if err := verifyClosureSignatures(context.Background(), lf, cl, wsCfg, t.TempDir(), io.Discard,
		pluginTargetFor(store, libTestVersion)); err != nil {
		t.Fatalf("verifyClosureSignatures: %v", err)
	}

	sig := lf.SchemaLibraries[0].Signature
	if sig == nil {
		t.Fatal("the library signature fact was not recorded on the lockfile row; the library never " +
			"entered the verifiable set")
	}
	if sig.Mode != "keyed" {
		t.Errorf("mode = %q, want keyed", sig.Mode)
	}
	if sig.BundleDigest == "" {
		t.Error("recorded fact carries no cached bundle digest, so an air-gapped re-verify has nothing to check")
	}
}

// TestVerifyClosureLibraryUnsignedHalts proves the Loud Guardrail reaches the
// library axis: a signature-requiring registry halts the pull for an unsigned
// library exactly as it does for an unsigned entity. The recording arm above
// cannot show this — a pass that skipped the axis entirely would also record
// nothing and return nil.
func TestVerifyClosureLibraryUnsignedHalts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\n")
	contentDg := digest.ContentDigest(body)
	manifestDg, err := oci.Push(ctx, store, libTestVersion, oci.MediaTypeSchemaLibrary, body, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	cl, lf := libraryClosureAndLockfile(manifestDg, contentDg)
	wsCfg := workspaceconfig.Config{
		DefaultRegistry: libTestHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host:      libTestHost,
			Signing:   workspaceconfig.SigningConfig{Required: true},
			PublicKey: "irrelevant",
		}},
	}

	err = verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), io.Discard, pluginTargetFor(store, libTestVersion))
	if err == nil {
		t.Fatal("an unsigned library under a signature-requiring registry must halt the pull")
	}
	if !errors.Is(err, signing.ErrNoSignatureFound) {
		t.Fatalf("want ErrNoSignatureFound, got %v", err)
	}
}

// TestDiscoverLibraryDeprecationRecordsMarker is the marker-discovery
// demonstration: a signed withdrawal marker attached to a library's published
// version is discovered at pull time, its own signature verified, and the verified
// record folded onto the lockfile library row — which is what makes the build-time
// notice possible at all, since build reads the recorded fact and never touches a
// registry.
func TestDiscoverLibraryDeprecationRecordsMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, manifestDg, contentDg, wsCfg, signer := signedLibraryStore(t)

	marker := deprecation.Marker{
		Severity: deprecation.SeverityRetracted,
		Reason:   "withdrawn: unsound port range",
		IssuedOn: "2026-08-01T00:00:00Z",
	}
	if err := attachSignedMarker(ctx, io.Discard, store, libTestVersion,
		libTestHost+"/schemalibrary/portspec:"+libTestVersion, marker, signer); err != nil {
		t.Fatalf("attachSignedMarker: %v", err)
	}
	// A marker is an untagged referrer, and verifying its own signature lists
	// referrers of the marker's digest. A registry resolves a manifest by digest
	// while this in-memory store resolves only what it has tagged, so tag the marker
	// by its digest — a fidelity shim for the store, not something the production
	// path needs.
	markers, err := oci.ListReferrers(ctx, store, libTestVersion, deprecation.ArtifactType)
	if err != nil || len(markers) == 0 {
		t.Fatalf("marker not discoverable after attach: %v (%d found)", err, len(markers))
	}
	for _, m := range markers {
		if terr := store.Tag(ctx, m, m.Digest.String()); terr != nil {
			t.Fatalf("tag marker by digest: %v", terr)
		}
	}

	cl, lf := libraryClosureAndLockfile(manifestDg, contentDg)
	if err := verifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), io.Discard,
		pluginTargetFor(store, libTestVersion)); err != nil {
		t.Fatalf("verifyClosureSignatures: %v", err)
	}

	rec := lf.SchemaLibraries[0].Deprecation
	if rec == nil {
		t.Fatal("marker discovery did not reach the library reference; the lockfile row records no withdrawal, " +
			"so the build has nothing to report")
	}
	if rec.Severity != string(deprecation.SeverityRetracted) {
		t.Errorf("severity = %q, want %q", rec.Severity, deprecation.SeverityRetracted)
	}
	if rec.Message != marker.Reason {
		t.Errorf("message = %q, want the marker reason %q", rec.Message, marker.Reason)
	}
	// Recording implies verification: the record carries the verified signer of the
	// marker itself, so no read site has to re-judge trust offline.
	if rec.Signer == nil {
		t.Fatal("the recorded marker carries no verified signer; recording must imply verification")
	}
}

// TestLibraryVerifyLocationEqualsPushLocation cross-checks the location a library
// reference is verified at against the location push publishes it to. The two are
// formed by different code — the verify binding here, buildPushReference there —
// so an independent comparison is what catches one of them drifting. A verify that
// discovered referrers at the wrong repository would find no signature and report a
// legitimately-signed library as unsigned.
func TestLibraryVerifyLocationEqualsPushLocation(t *testing.T) {
	t.Parallel()
	const reg = "reg.example"
	for _, kind := range []string{"SchemaLibrary", "MappingLibrary", "WorkloadLibrary", "ArtifactsLibrary"} {
		axis := closure.VerifiableAxis{Axis: kind + "s", Kind: kind}
		// The pinned form must bind to the same location as the bare form: the
		// location is version-independent, and a version left in the path would name
		// a repository that does not exist.
		for _, uri := range []string{libTestName, libTestURI} {
			gotKind, gotLocation, bound := verifySubjectBinding(reg, axis, closure.VerifiableRef{URI: uri})
			if !bound {
				t.Errorf("%s %q: no location bound, so the reference would be verified against its "+
					"host-less identity and skipped", kind, uri)
				continue
			}
			if gotKind != kind {
				t.Errorf("%s %q: kind = %q, want %q — the org signing policy scopes on this", kind, uri, gotKind, kind)
			}
			if want := buildPushReference(reg, kind, "", libTestName); gotLocation != want {
				t.Errorf("%s %q: verify location %q != push location %q (verify and push split the "+
					"single location rule)", kind, uri, gotLocation, want)
			}
		}
	}
}

// TestLibraryVerifyBindingRefusesWithoutARegistry pins the honest-skip posture: a
// workspace with no default registry cannot bind a host-less library identity to
// any location, and the binding says so rather than inventing one. Inventing a
// location would send referrer discovery at a repository nobody published to and
// report a signed library as unsigned.
func TestLibraryVerifyBindingRefusesWithoutARegistry(t *testing.T) {
	t.Parallel()
	axis := closure.VerifiableAxis{Axis: "SchemaLibraries", Kind: "SchemaLibrary"}
	kind, location, bound := verifySubjectBinding("", axis, closure.VerifiableRef{URI: libTestURI})
	if bound || location != "" {
		t.Errorf("with no default registry the binding must refuse; got location %q bound=%v", location, bound)
	}
	if kind != "SchemaLibrary" {
		t.Errorf("the kind is known from the axis even when no location binds; got %q", kind)
	}
}

// TestVerifyClosureSkipsALibraryWithNoBoundLocation pins the skip the collect loop
// owes an unbindable library. A library identity is host-less and may carry a
// version suffix, so falling back to the URI-derived host would read that suffix's
// colon as a host:port authority and hand referrer discovery a reference no
// registry serves. The reference is skipped instead, which is the honest posture:
// with no registry to bind to there is no signature to discover.
func TestVerifyClosureSkipsALibraryWithNoBoundLocation(t *testing.T) {
	t.Parallel()
	store, manifestDg, contentDg, wsCfg, _ := signedLibraryStore(t)
	// Drop the default registry: the library identity then binds to no location.
	wsCfg.DefaultRegistry = ""

	cl, lf := libraryClosureAndLockfile(manifestDg, contentDg)
	if err := verifyClosureSignatures(context.Background(), lf, cl, wsCfg, t.TempDir(), io.Discard,
		pluginTargetFor(store, libTestVersion)); err != nil {
		t.Fatalf("an unbindable library must be skipped, not halted on: %v", err)
	}
	if lf.SchemaLibraries[0].Signature != nil {
		t.Error("a library with no bound registry location must record no signature; recording one " +
			"means it was verified against a location nobody published to")
	}
}
