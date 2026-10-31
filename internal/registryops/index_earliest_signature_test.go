// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The identities a version's signatures are made by in these rows. The first two
// are trusted by the policy the rows read under; the stranger is not.
const (
	firstSignerIdentity  = "first-signer@example.test"
	secondSignerIdentity = "second-signer@example.test"
	strangerIdentity     = "stranger@example.test"
)

// listsReferrersInOrder is an in-memory repository whose referrer listing names
// the referrer manifests in first ahead of every other, in that order. The
// in-memory store alone lists them in no fixed order, so a walk whose answer
// followed the listing would pass or fail by chance; this makes the listing order
// part of a row's input.
type listsReferrersInOrder struct {
	*taggedStore
	first []string
}

func (l *listsReferrersInOrder) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
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

// keylessSignerAs signs as one identity through the in-memory keyless authority,
// with the log recording each signature at a fixed time.
type keylessSignerAs struct {
	authority *signingtest.KeylessAuthority
	identity  string
	at        time.Time
}

func (k keylessSignerAs) Sign(_ context.Context, digest string) ([]byte, string, error) {
	sig, err := k.authority.Sign(k.identity, datedIssuer, digest, k.at)
	if err != nil {
		return nil, "", err
	}
	return sig.Bundle, signing.BundleMediaType, nil
}

// signingAuthority creates an in-memory keyless authority and writes its trusted
// root, returning the authority and the root's path.
func signingAuthority(t *testing.T) (*signingtest.KeylessAuthority, string) {
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

// twoSignerPolicy trusts the first and second signer's keyless identities, and no
// one else, against the authority's root.
func twoSignerPolicy(rootPath string) workspaceconfig.RegistryConfig {
	return workspaceconfig.RegistryConfig{
		TrustedIdentities: []workspaceconfig.TrustedIdentity{
			{Identity: firstSignerIdentity, Issuer: datedIssuer},
			{Identity: secondSignerIdentity, Issuer: datedIssuer},
		},
		TrustedRoot: rootPath,
	}
}

// madeSignature is one signature attached to the version: the referrer manifest
// it was attached as, its bundle's digest, and the time the log recorded it.
type madeSignature struct {
	referrer     ocispec.Descriptor
	bundleDigest string
	loggedAt     time.Time
	identity     string
}

// signedVersionRepo publishes version 1.0.0 into a fresh repository and attaches
// one keyless signature per signer, in the order given, returning the repository,
// the version's digest and what was attached.
func signedVersionRepo(t *testing.T, authority *signingtest.KeylessAuthority, signers []keylessSignerAs) (*taggedStore, string, []madeSignature) {
	t.Helper()
	ctx := context.Background()
	store := &taggedStore{Store: memory.New(), tags: []string{"1.0.0"}}
	dg, err := oci.Push(ctx, store.Store, "1.0.0", oci.MediaTypeComponent, []byte("# a version signed more than once\n"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	made := make([]madeSignature, 0, len(signers))
	for _, s := range signers {
		sig, err := authority.Sign(s.identity, datedIssuer, dg, s.at)
		if err != nil {
			t.Fatalf("keyless Sign as %s: %v", s.identity, err)
		}
		referrer, err := oci.AttachReferrer(ctx, store.Store, "1.0.0", signing.BundleMediaType, sig.Bundle)
		if err != nil {
			t.Fatalf("attach the signature by %s: %v", s.identity, err)
		}
		made = append(made, madeSignature{
			referrer:     referrer,
			bundleDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(sig.Bundle)),
			loggedAt:     sig.LoggedAt,
			identity:     s.identity,
		})
	}
	return store, dg, made
}

// listedAs returns the referrer digests of the signatures at the given indexes, in
// that order: the order the registry is made to list them in.
func listedAs(made []madeSignature, order ...int) []string {
	out := make([]string, 0, len(order))
	for _, i := range order {
		out = append(out, made[i].referrer.Digest.String())
	}
	return out
}

// TestTheIndexRecordsTheEarliestSignatureThatVerifies proves, through the index
// walk, that a version carrying more than one signature records the earliest
// transparency-log time among the signatures its host's policy verifies as its
// date, and that same signature's signer as its signer — whatever order the
// registry lists the signatures in, and however many were attached after it. The
// first row is the control, a version signed once. Each other row carries one
// fault and names the defect it catches.
func TestTheIndexRecordsTheEarliestSignatureThatVerifies(t *testing.T) {
	t.Parallel()
	authority, rootPath := signingAuthority(t)
	policy := twoSignerPolicy(rootPath)
	now := time.Now()
	early, late := now.Add(2*time.Minute), now.Add(5*time.Minute)

	cases := []struct {
		name   string
		defect string
		// signers are attached in this order.
		signers []keylessSignerAs
		// listed is the order the registry lists the signatures in, as indexes into
		// signers.
		listed []int
		// want is the index into signers of the signature whose log time and signer
		// the index must record.
		want int
	}{
		{
			name:    "control: a version signed once",
			defect:  "a version's only verifying signature does not date it",
			signers: []keylessSignerAs{{identity: firstSignerIdentity, at: early}},
			listed:  []int{0},
			want:    0,
		},
		{
			name:   "two signatures that verify, the later listed first",
			defect: "the date and the signer follow the order the registry lists signatures in, so signing a version again moves them",
			signers: []keylessSignerAs{
				{identity: firstSignerIdentity, at: early},
				{identity: secondSignerIdentity, at: late},
			},
			listed: []int{1, 0},
			want:   0,
		},
		{
			name:   "two signatures that verify, the earlier listed first",
			defect: "the date and the signer follow the last signature listed rather than the earliest",
			signers: []keylessSignerAs{
				{identity: firstSignerIdentity, at: early},
				{identity: secondSignerIdentity, at: late},
			},
			listed: []int{0, 1},
			want:   0,
		},
		{
			name:   "a later signature the policy does not admit, listed first",
			defect: "a signature the policy refuses is read as the version's own",
			signers: []keylessSignerAs{
				{identity: firstSignerIdentity, at: early},
				{identity: strangerIdentity, at: late},
			},
			listed: []int{1, 0},
			want:   0,
		},
		{
			name:   "an earlier signature the policy does not admit, listed first",
			defect: "an earlier signature the policy refuses takes the date, so it counts without verifying",
			signers: []keylessSignerAs{
				{identity: strangerIdentity, at: early},
				{identity: secondSignerIdentity, at: late},
			},
			listed: []int{0, 1},
			want:   1,
		},
	}
	// The rows share one in-memory authority and run one after another, so none
	// depends on that authority being safe to sign through from two goroutines.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, dg, made := signedVersionRepo(t, authority, tc.signers)
			target := &listsReferrersInOrder{taggedStore: store, first: listedAs(made, tc.listed...)}

			got, err := readEachPublishedVersion(context.Background(), workspaceconfig.Config{}, policy, target, []string{"1.0.0"})
			if err != nil {
				t.Fatalf("readEachPublishedVersion: %v", err)
			}
			if got[0].ManifestDigest != dg {
				t.Fatalf("1.0.0 resolved to %s; want the digest the fixture signed, %s", got[0].ManifestDigest, dg)
			}
			if len(got) != 1 {
				t.Fatalf("entries = %d; want 1", len(got))
			}
			pv, want := got[0], made[tc.want]
			wantAt := oci.FormatPublishedAt(want.loggedAt)
			if pv.PublishedAt != wantAt || pv.PublishedAtSource != registryindex.PublishedAtFromTransparencyLog {
				t.Errorf("date (%q, %q); want (%q, %q), the log time of %s's signature (defect: %s)",
					pv.PublishedAt, pv.PublishedAtSource, wantAt, registryindex.PublishedAtFromTransparencyLog, want.identity, tc.defect)
			}
			if pv.Signer == nil {
				t.Fatalf("no signer recorded; want %s (defect: %s)", want.identity, tc.defect)
			}
			if pv.Signer.Identity != want.identity || pv.Signer.BundleDigest != want.bundleDigest {
				t.Errorf("signer (%q, %s); want (%q, %s), the signature the date was read from (defect: %s)",
					pv.Signer.Identity, pv.Signer.BundleDigest, want.identity, want.bundleDigest, tc.defect)
			}
		})
	}
}

// TestTheWithdrawalCheckTakesTheEarliestSignatureAsTheOriginalSigner proves, through
// the index walk's withdrawal scan, that the signer a keyless policy honors a
// withdrawal from as the version's own publisher is the earliest of the version's
// signatures that verify, not whichever the registry lists first. The version is
// signed by two identities the policy trusts as artifact signers and lists the
// later signature first; the policy names no withdrawal identity, so only the
// original signer's marker can be honored. The two rows differ only in who signed
// the marker.
func TestTheWithdrawalCheckTakesTheEarliestSignatureAsTheOriginalSigner(t *testing.T) {
	t.Parallel()
	authority, rootPath := signingAuthority(t)
	policy := twoSignerPolicy(rootPath)
	now := time.Now()
	early, late, markedAt := now.Add(2*time.Minute), now.Add(5*time.Minute), now.Add(6*time.Minute)

	cases := []struct {
		name         string
		defect       string
		markerSigner string
		wantHonored  bool
	}{
		{
			name:         "a marker signed by the earliest signer is honored",
			defect:       "the original signer is taken from a later signature, so the version's own publisher cannot withdraw it",
			markerSigner: firstSignerIdentity,
			wantHonored:  true,
		},
		{
			name:         "a marker signed by a later signer is not honored",
			defect:       "a later signature makes its identity the original signer, whose withdrawals the version then honors",
			markerSigner: secondSignerIdentity,
			wantHonored:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, _, made := signedVersionRepo(t, authority, []keylessSignerAs{
				{identity: firstSignerIdentity, at: early},
				{identity: secondSignerIdentity, at: late},
			})
			marker := deprecation.Marker{
				Severity: deprecation.SeverityRetracted,
				Reason:   "withdrawn by " + tc.markerSigner,
				IssuedOn: "2026-05-01T00:00:00Z",
			}
			if _, err := registryindex.AttachSignedMarker(ctx, io.Discard, store.Store, "1.0.0",
				"reg.example.test/component/core/api-server:1.0.0", marker,
				keylessSignerAs{authority: authority, identity: tc.markerSigner, at: markedAt}); err != nil {
				t.Fatalf("AttachSignedMarker: %v", err)
			}
			// A marker is an untagged referrer, and its own signature is listed by the
			// marker's digest; the in-memory store resolves only what it has tagged, so
			// the marker is tagged by its digest, as a registry would resolve it.
			markers, err := oci.ListReferrers(ctx, store.Store, "1.0.0", deprecation.ArtifactType)
			if err != nil || len(markers) != 1 {
				t.Fatalf("the marker is not discoverable: %v (%d found)", err, len(markers))
			}
			if err := store.Store.Tag(ctx, markers[0], markers[0].Digest.String()); err != nil {
				t.Fatalf("tag the marker by digest: %v", err)
			}
			target := &listsReferrersInOrder{taggedStore: store, first: listedAs(made, 1, 0)}

			deps, notes := CollectVersionDeprecations(ctx, workspaceconfig.Config{}, policy, target,
				"component/core/api-server", []string{"1.0.0"}, io.Discard)
			if len(notes) != 1 {
				t.Fatalf("notes = %+v; want one, for the one marker", notes)
			}
			if notes[0].Recorded != tc.wantHonored || (len(deps) == 1) != tc.wantHonored {
				t.Fatalf("marker by %s honored = %v (deprecations %d, note %+v); want %v (defect: %s)",
					tc.markerSigner, notes[0].Recorded, len(deps), notes[0], tc.wantHonored, tc.defect)
			}
			if tc.wantHonored && (deps[0].Signer == nil || deps[0].Signer.Identity != tc.markerSigner) {
				t.Errorf("recorded marker signer = %+v; want %s", deps[0].Signer, tc.markerSigner)
			}
		})
	}
}
