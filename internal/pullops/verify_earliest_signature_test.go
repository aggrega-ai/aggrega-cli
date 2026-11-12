// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// keylessLibraryHost is the registry the library in these tests is pulled from. Its
// policy verifies keyless signatures against an in-memory authority's root.
const keylessLibraryHost = "keyless.example.com"

// The identities a library version's signatures are made by in these rows. The
// first two are trusted by the policy the pull reads under; the stranger is not.
const (
	earliestPublisher = "earliest-publisher@example.test"
	laterSigner       = "later-signer@example.test"
	strangerSigner    = "stranger@example.test"
	publisherIssuer   = "https://issuer.example.test"
)

// listsSignaturesInOrder is an in-memory registry whose referrer listing names the
// referrer manifests in first ahead of every other, in that order. The in-memory
// store alone lists them in no fixed order, so a pass whose answer followed the
// listing would pass or fail by chance; this makes the listing order part of a
// row's input.
type listsSignaturesInOrder struct {
	*memory.Store
	first []string
}

func (l *listsSignaturesInOrder) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	preds, err := l.Store.Predecessors(ctx, node)
	if err != nil {
		return nil, err
	}
	rank := func(d ocispec.Descriptor) int {
		for i, f := range l.first {
			if d.Digest.String() == f {
				return i
			}
		}
		return len(l.first)
	}
	sort.SliceStable(preds, func(i, j int) bool { return rank(preds[i]) < rank(preds[j]) })
	return preds, nil
}

// keylessSignerFor signs as one identity through the in-memory keyless authority,
// with the log recording each signature at a fixed time. It signs a deprecation
// marker in the withdrawal rows.
type keylessSignerFor struct {
	authority *signingtest.KeylessAuthority
	identity  string
	at        time.Time
}

func (k keylessSignerFor) Sign(_ context.Context, dg string) ([]byte, string, error) {
	sig, err := k.authority.Sign(k.identity, publisherIssuer, dg, k.at)
	if err != nil {
		return nil, "", err
	}
	return sig.Bundle, signing.BundleMediaType, nil
}

// signedAs names one signature a row attaches: who makes it, and how long after
// the row starts the log records it. A row reads the clock when it starts, so each
// log time falls inside the minutes the in-memory signing certificate is valid for
// however long the rows before it took.
type signedAs struct {
	identity string
	after    time.Duration
}

// signatureMade is one signature attached to the library version: the referrer
// manifest it was attached as, its bundle's digest and who made it.
type signatureMade struct {
	referrer     ocispec.Descriptor
	bundleDigest string
	identity     string
}

// keylessLibraryAuthority creates an in-memory keyless authority and writes its
// trusted root, returning the authority and the root's path.
func keylessLibraryAuthority(t *testing.T) (*signingtest.KeylessAuthority, string) {
	t.Helper()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	rootJSON, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootPath, rootJSON, 0o644); err != nil {
		t.Fatalf("write trusted root: %v", err)
	}
	return authority, rootPath
}

// keylessLibraryWorkspace is a workspace whose default registry is the keyless
// test host, trusting the earliest publisher's and the later signer's keyless
// identities, and no one else, against the authority's root. It names no
// withdrawal identity, so a withdrawal is honored only from the version's own
// original signer. It requires no signature, so a withdrawal that is not honored is
// reported and left out rather than halting the pull, and a row can read which
// one was recorded.
//
// default_registry is load-bearing: a library identity is host-less, so the
// registry its signatures are discovered at is bound from it, and without one the
// pass skips the reference and every row would pass while proving nothing.
func keylessLibraryWorkspace(rootPath string) workspaceconfig.Config {
	return workspaceconfig.Config{
		DefaultRegistry: keylessLibraryHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host: keylessLibraryHost,
			TrustedIdentities: []workspaceconfig.TrustedIdentity{
				{Identity: earliestPublisher, Issuer: publisherIssuer},
				{Identity: laterSigner, Issuer: publisherIssuer},
			},
			TrustedRoot: rootPath,
		}},
	}
}

// keylessSignedLibrary publishes a library body under its pinned version into a
// fresh store and attaches one keyless signature per signer, in the order given,
// each logged its signer's interval after start. It returns the store, the manifest
// digest (the verify subject), the content digest (the cache pin) and what was
// attached.
func keylessSignedLibrary(t *testing.T, authority *signingtest.KeylessAuthority, start time.Time, signers []signedAs) (*memory.Store, string, string, []signatureMade) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	body := []byte("apiVersion: aggrega.ai/v1beta1\nkind: SchemaLibrary\n# signed more than once\n")
	contentDg := digest.ContentDigest(body)
	manifestDg, err := oci.Push(ctx, store, libTestVersion, oci.MediaTypeSchemaLibrary, body, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	made := make([]signatureMade, 0, len(signers))
	for _, s := range signers {
		sig, err := authority.Sign(s.identity, publisherIssuer, manifestDg, start.Add(s.after))
		if err != nil {
			t.Fatalf("keyless Sign as %s: %v", s.identity, err)
		}
		referrer, err := oci.AttachReferrer(ctx, store, libTestVersion, signing.BundleMediaType, sig.Bundle)
		if err != nil {
			t.Fatalf("attach the signature by %s: %v", s.identity, err)
		}
		made = append(made, signatureMade{
			referrer:     referrer,
			bundleDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(sig.Bundle)),
			identity:     s.identity,
		})
	}
	return store, manifestDg, contentDg, made
}

// listedInOrder returns the referrer digests of the signatures at the given
// indexes, in that order: the order the registry is made to list them in.
func listedInOrder(made []signatureMade, order ...int) []string {
	out := make([]string, 0, len(order))
	for _, i := range order {
		out = append(out, made[i].referrer.Digest.String())
	}
	return out
}

// TestAPullRecordsTheEarliestSignatureThatVerifies proves, through the pull's
// signature pass, that a library version carrying more than one signature is
// recorded on its lockfile row with the earliest of the signatures its registry's
// policy verifies — that signature's signer and bundle — whatever order the
// registry lists the signatures in. It is the signature the registry index dates
// the version by, so a lockfile and an index never name two different signatures
// for one version. The first row is the control, a version signed once; each other
// row carries one fault and names the defect it catches.
func TestAPullRecordsTheEarliestSignatureThatVerifies(t *testing.T) {
	t.Parallel()
	authority, rootPath := keylessLibraryAuthority(t)
	wsCfg := keylessLibraryWorkspace(rootPath)
	const early, late = 2 * time.Minute, 5 * time.Minute

	cases := []struct {
		name   string
		defect string
		// signers are attached in this order.
		signers []signedAs
		// listed is the order the registry lists the signatures in, as indexes into
		// signers.
		listed []int
		// want is the index into signers of the signature the row must record.
		want int
	}{
		{
			name:    "control: a version signed once",
			defect:  "a version's only verifying signature is not recorded",
			signers: []signedAs{{identity: earliestPublisher, after: early}},
			listed:  []int{0},
			want:    0,
		},
		{
			name:   "two signatures that verify, the later listed first",
			defect: "the recorded signature follows the order the registry lists signatures in, so signing a version again changes it",
			signers: []signedAs{
				{identity: earliestPublisher, after: early},
				{identity: laterSigner, after: late},
			},
			listed: []int{1, 0},
			want:   0,
		},
		{
			name:   "two signatures that verify, the earlier listed first",
			defect: "the recorded signature is the last one listed rather than the earliest",
			signers: []signedAs{
				{identity: earliestPublisher, after: early},
				{identity: laterSigner, after: late},
			},
			listed: []int{0, 1},
			want:   0,
		},
		{
			name:   "a later signature the policy does not admit, listed first",
			defect: "a signature the policy refuses is recorded as the version's own",
			signers: []signedAs{
				{identity: earliestPublisher, after: early},
				{identity: strangerSigner, after: late},
			},
			listed: []int{1, 0},
			want:   0,
		},
		{
			name:   "an earlier signature the policy does not admit, listed first",
			defect: "an earlier signature the policy refuses outranks the one that verifies, so it counts without verifying",
			signers: []signedAs{
				{identity: strangerSigner, after: early},
				{identity: laterSigner, after: late},
			},
			listed: []int{0, 1},
			want:   1,
		},
	}
	// The rows share one in-memory authority and run one after another, so none
	// depends on that authority being safe to sign through from two goroutines.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, manifestDg, contentDg, made := keylessSignedLibrary(t, authority, time.Now(), tc.signers)
			target := &listsSignaturesInOrder{Store: store, first: listedInOrder(made, tc.listed...)}
			cl, lf := libraryClosureAndLockfile(manifestDg, contentDg)

			if err := VerifyClosureSignatures(context.Background(), lf, cl, wsCfg, t.TempDir(), ".aggrega/cache", io.Discard,
				pluginTargetFor(target, libTestVersion)); err != nil {
				t.Fatalf("VerifyClosureSignatures: %v (defect: %s)", err, tc.defect)
			}

			sig, want := lf.SchemaLibraries[0].Signature, made[tc.want]
			if sig == nil {
				t.Fatalf("no signature recorded; want %s's (defect: %s)", want.identity, tc.defect)
			}
			if sig.Mode != "keyless" || sig.Identity != want.identity || sig.BundleDigest != want.bundleDigest {
				t.Errorf("recorded signature (%s, %q, %s); want (keyless, %q, %s), the earliest that verifies (defect: %s)",
					sig.Mode, sig.Identity, sig.BundleDigest, want.identity, want.bundleDigest, tc.defect)
			}
		})
	}
}

// TestAPullHonorsWithdrawalsFromTheEarliestSigner proves, through the pull's
// signature pass and the withdrawal discovery that reads it, that the signer a
// keyless policy honors a withdrawal from as the version's own publisher is the
// earliest of the version's signatures that verify — the one its lockfile row
// records — and not whichever the registry lists first. The version is signed by two
// identities the policy trusts and lists the later signature first; the policy
// names no withdrawal identity, so only the original signer's marker can be
// honored. The two rows differ only in who signed the marker.
func TestAPullHonorsWithdrawalsFromTheEarliestSigner(t *testing.T) {
	t.Parallel()
	authority, rootPath := keylessLibraryAuthority(t)
	wsCfg := keylessLibraryWorkspace(rootPath)
	const early, late, marked = 2 * time.Minute, 5 * time.Minute, 6 * time.Minute

	cases := []struct {
		name         string
		defect       string
		markerSigner string
		wantHonored  bool
	}{
		{
			name:         "a marker signed by the earliest signer is honored",
			defect:       "the original signer is taken from a later signature, so the version's own publisher cannot withdraw it",
			markerSigner: earliestPublisher,
			wantHonored:  true,
		},
		{
			name:         "a marker signed by a later signer is not honored",
			defect:       "a later signature makes its identity the original signer, whose withdrawals the version then honors",
			markerSigner: laterSigner,
			wantHonored:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			start := time.Now()
			store, manifestDg, contentDg, made := keylessSignedLibrary(t, authority, start, []signedAs{
				{identity: earliestPublisher, after: early},
				{identity: laterSigner, after: late},
			})
			marker := deprecation.Marker{
				Severity: deprecation.SeverityRetracted,
				Reason:   "withdrawn by " + tc.markerSigner,
				IssuedOn: "2026-05-01T00:00:00Z",
			}
			if _, err := registryindex.AttachSignedMarker(ctx, io.Discard, store, libTestVersion,
				keylessLibraryHost+"/schemalibrary/portspec:"+libTestVersion, marker,
				keylessSignerFor{authority: authority, identity: tc.markerSigner, at: start.Add(marked)}); err != nil {
				t.Fatalf("AttachSignedMarker: %v", err)
			}
			// A marker is an untagged referrer, and its own signature is listed by the
			// marker's digest; the in-memory store resolves only what it has tagged, so
			// the marker is tagged by its digest, as a registry would resolve it.
			markers, err := oci.ListReferrers(ctx, store, libTestVersion, deprecation.ArtifactType)
			if err != nil || len(markers) != 1 {
				t.Fatalf("the marker is not discoverable: %v (%d found)", err, len(markers))
			}
			if err := store.Tag(ctx, markers[0], markers[0].Digest.String()); err != nil {
				t.Fatalf("tag the marker by digest: %v", err)
			}
			target := &listsSignaturesInOrder{Store: store, first: listedInOrder(made, 1, 0)}
			cl, lf := libraryClosureAndLockfile(manifestDg, contentDg)

			var diag bytes.Buffer
			if err := VerifyClosureSignatures(ctx, lf, cl, wsCfg, t.TempDir(), ".aggrega/cache", &diag,
				pluginTargetFor(target, libTestVersion)); err != nil {
				t.Fatalf("VerifyClosureSignatures: %v", err)
			}

			// Both facts are checked whatever the first says, so a row failing on the
			// recorded signature still reports what that did to the withdrawal.
			row := lf.SchemaLibraries[0]
			if row.Signature == nil || row.Signature.Identity != earliestPublisher {
				t.Errorf("recorded signature = %+v; want %s's, the earliest that verifies (defect: %s)",
					row.Signature, earliestPublisher, tc.defect)
			}
			if (row.Deprecation != nil) != tc.wantHonored {
				t.Fatalf("marker by %s recorded = %v (deprecation %+v; diagnostics %q); want %v (defect: %s)",
					tc.markerSigner, row.Deprecation != nil, row.Deprecation, diag.String(), tc.wantHonored, tc.defect)
			}
			if tc.wantHonored {
				if row.Deprecation.Signer == nil || row.Deprecation.Signer.Identity != tc.markerSigner {
					t.Errorf("recorded marker signer = %+v; want %s", row.Deprecation.Signer, tc.markerSigner)
				}
				return
			}
			if !strings.Contains(diag.String(), "marker present but not honored") {
				t.Errorf("a marker left out was not reported; diagnostics %q", diag.String())
			}
		})
	}
}
