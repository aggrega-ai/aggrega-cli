// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// datedRepo is one repository holding the four shapes a version's date can take:
// 1.0.0 signed keyless and stating nothing, as a keyless publication does; 1.1.0
// signed with a key and stating its date, as a keyed publication does; 1.2.0
// unsigned and stating its date; 1.3.0 unsigned and stating nothing, as a
// publication made without dates does.
type datedRepo struct {
	store    *taggedStore
	digests  map[string]string
	logged   time.Time
	keyedAt  time.Time
	stateAt  time.Time
	rootPath string
	pubPEM   string
}

const (
	datedIdentity = "publisher@example.test"
	datedIssuer   = "https://issuer.example.test"
)

func newDatedRepo(t *testing.T) datedRepo {
	t.Helper()
	ctx := context.Background()
	versions := []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"}
	r := datedRepo{
		store:   &taggedStore{Store: memory.New(), tags: versions},
		digests: map[string]string{},
		keyedAt: time.Date(2026, 9, 27, 17, 0, 0, 0, time.UTC),
		stateAt: time.Date(2026, 9, 27, 17, 30, 0, 0, time.UTC),
	}
	for _, v := range versions {
		d, err := oci.Push(ctx, r.store.Store, v, oci.MediaTypeComponent, []byte(fmt.Sprintf("# version %s\n", v)), nil)
		if err != nil {
			t.Fatalf("Push %s: %v", v, err)
		}
		r.digests[v] = d
	}

	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	rootJSON, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	r.rootPath = filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(r.rootPath, rootJSON, 0o644); err != nil {
		t.Fatalf("write trusted root: %v", err)
	}
	keyless, err := authority.Sign(datedIdentity, datedIssuer, r.digests["1.0.0"], time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}
	r.logged = keyless.LoggedAt
	if _, err := oci.AttachReferrer(ctx, r.store.Store, "1.0.0", signing.BundleMediaType, keyless.Bundle); err != nil {
		t.Fatalf("attach keyless signature: %v", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	r.pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	signer, err := signing.LoadKeyedSigner(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	keyed, artifactType, err := signer.Sign(ctx, r.digests["1.1.0"])
	if err != nil {
		t.Fatalf("keyed Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, r.store.Store, "1.1.0", artifactType, keyed); err != nil {
		t.Fatalf("attach keyed signature: %v", err)
	}
	if _, err := oci.AttachPublishedAt(ctx, r.store.Store, "1.1.0", r.keyedAt); err != nil {
		t.Fatalf("attach 1.1.0 date: %v", err)
	}
	if _, err := oci.AttachPublishedAt(ctx, r.store.Store, "1.2.0", r.stateAt); err != nil {
		t.Fatalf("attach 1.2.0 date: %v", err)
	}
	return r
}

// readEachPublishedVersion reads every version in the order given the way the
// index walk reads a version in full: its tag resolved to the digest it names,
// then its signer and date read through readPublishedVersion. A read the walk
// could not finish is not an error here, since these tests hold the entry the
// walk records rather than whether it would read the version again.
func readEachPublishedVersion(ctx context.Context, wsCfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig, target oras.ReadOnlyTarget, versions []string) ([]registryindex.PublishedVersion, error) {
	out := make([]registryindex.PublishedVersion, 0, len(versions))
	for _, v := range versions {
		digest, exists, err := oci.Resolve(ctx, target, v)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("version %s no longer resolves", v)
		}
		pv, _ := readPublishedVersion(ctx, wsCfg, policy, target, v, digest)
		out = append(out, pv)
	}
	return out, nil
}

// wantDate is one version's expected date and source; an empty source expects
// no date at all.
type wantDate struct {
	at     time.Time
	source string
}

// TestReadPublishedVersionsRecordsEachDateWithItsSource proves the index walk
// records every version's date and its source under each kind of trust policy,
// over one fixed registry state. The four versions are the four shapes a date
// takes, so a row failing names both the policy and the shape that broke. Each
// row names the defect it catches.
func TestReadPublishedVersionsRecordsEachDateWithItsSource(t *testing.T) {
	t.Parallel()
	repo := newDatedRepo(t)
	keylessPolicy := workspaceconfig.RegistryConfig{
		TrustedIdentities: []workspaceconfig.TrustedIdentity{{Identity: datedIdentity, Issuer: datedIssuer}},
		TrustedRoot:       repo.rootPath,
	}
	keyedPolicy := workspaceconfig.RegistryConfig{PublicKey: repo.pubPEM}
	skipped := keylessPolicy
	skipped.SkipSignatureVerification = true

	keyed := wantDate{at: repo.keyedAt, source: registryindex.PublishedAtFromPublisher}
	stated := wantDate{at: repo.stateAt, source: registryindex.PublishedAtFromPublisher}
	none := wantDate{}
	cases := []struct {
		name   string
		defect string
		policy workspaceconfig.RegistryConfig
		want   map[string]wantDate
	}{
		{
			name:   "a keyless policy that trusts the signer",
			defect: "a verified keyless signature's log time is not recorded as the date",
			policy: keylessPolicy,
			want: map[string]wantDate{
				"1.0.0": {at: repo.logged, source: registryindex.PublishedAtFromTransparencyLog},
				"1.1.0": keyed, "1.2.0": stated, "1.3.0": none,
			},
		},
		{
			name:   "a keyed policy",
			defect: "a keyless signature this policy did not verify is dated, or a keyed one hides the statement",
			policy: keyedPolicy,
			want:   map[string]wantDate{"1.0.0": none, "1.1.0": keyed, "1.2.0": stated, "1.3.0": none},
		},
		{
			name:   "a policy pinning no anchor",
			defect: "reading the statement is made to need a trust anchor",
			policy: workspaceconfig.RegistryConfig{},
			want:   map[string]wantDate{"1.0.0": none, "1.1.0": keyed, "1.2.0": stated, "1.3.0": none},
		},
		{
			name:   "verification switched off",
			defect: "the log time is taken from a signature nobody verified",
			policy: skipped,
			want:   map[string]wantDate{"1.0.0": none, "1.1.0": keyed, "1.2.0": stated, "1.3.0": none},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			versions := []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"}
			got, err := readEachPublishedVersion(context.Background(), workspaceconfig.Config{}, tc.policy, repo.store, versions)
			if err != nil {
				t.Fatalf("readEachPublishedVersion: %v", err)
			}
			if len(got) != len(versions) {
				t.Fatalf("entries = %d; want %d", len(got), len(versions))
			}
			for i, v := range versions {
				pv := got[i]
				if pv.Version != v || pv.ManifestDigest != repo.digests[v] {
					t.Fatalf("entry %d = %+v; want version %s at %s", i, pv, v, repo.digests[v])
				}
				w := tc.want[v]
				wantAt := ""
				if w.source != "" {
					wantAt = w.at.UTC().Format(time.RFC3339)
				}
				if pv.PublishedAt != wantAt || pv.PublishedAtSource != w.source {
					t.Errorf("%s: date (%q, %q); want (%q, %q) (defect: %s)", v, pv.PublishedAt, pv.PublishedAtSource, wantAt, w.source, tc.defect)
				}
			}
		})
	}
}
