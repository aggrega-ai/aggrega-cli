// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// refusesAttachments is an in-memory store that refuses every upload of a
// signature bundle or a publication date, the way a registry failing those writes
// past their retries leaves a publication: the content and its tag land, and
// nothing beside them does. The refusal is not one the write layer retries.
type refusesAttachments struct {
	*memory.Store
}

func (r *refusesAttachments) Push(ctx context.Context, desc ocispec.Descriptor, rd io.Reader) error {
	if desc.MediaType == signing.BundleMediaType || desc.MediaType == oci.MediaTypePublishedAt {
		return errors.New("the test registry refuses this upload")
	}
	return r.Store.Push(ctx, desc, rd)
}

// failsListing is an in-memory store whose referrer listing cannot be completed:
// every question about what is attached to a manifest goes unanswered.
type failsListing struct {
	*memory.Store
}

func (f *failsListing) Predecessors(context.Context, ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return nil, errors.New("the test registry cannot list referrers")
}

const (
	completionIdentity = "publisher@example.test"
	completionIssuer   = "https://issuer.example.test"
)

// completionKeys holds the trust material every row draws on: an in-memory keyless
// authority with its trusted root on disk, the key a keyed publication signs with
// and its public half, and a second key the destination never admits.
type completionKeys struct {
	authority    *signingtest.KeylessAuthority
	rootPath     string
	keyPath      string
	pubPEM       string
	foreignKey   string
	unanchoredWS string
}

func newCompletionKeys(t *testing.T) completionKeys {
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
	keyPath, pubPEM := keyPair(t)
	foreignKey, _ := keyPair(t)
	return completionKeys{
		authority: authority, rootPath: rootPath,
		keyPath: keyPath, pubPEM: pubPEM, foreignKey: foreignKey,
		unanchoredWS: "workspace: {}\n",
	}
}

// keyPair writes a real ECDSA P-256 private key and returns its path with the
// public half as PEM, so a keyed signature made with it verifies under a policy
// pinning that public key.
func keyPair(t *testing.T) (keyPath, pubPEM string) {
	t.Helper()
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
	keyPath = filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return keyPath, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
}

// keyedWS is a workspace whose policy for the destination pins pubPEM.
func keyedWS(pubPEM string) string {
	var b strings.Builder
	b.WriteString("workspace: {}\nregistries:\n  - host: " + datedRegistry + "\n    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(pubPEM, "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// keylessWS is a workspace whose policy for the destination admits the keyless
// publisher identity against the in-memory authority's root.
func (k completionKeys) keylessWS() string {
	return "workspace: {}\nregistries:\n  - host: " + datedRegistry + "\n" +
		"    trusted_identities:\n      - identity: " + completionIdentity + "\n        issuer: " + completionIssuer + "\n" +
		"    trusted_root: " + k.rootPath + "\n"
}

// keylessOver signs digest as the admitted keyless identity; the bundle carries a
// transparency-log time.
func (k completionKeys) keylessOver(t *testing.T, digest string) []byte {
	t.Helper()
	sig, err := k.authority.Sign(completionIdentity, completionIssuer, digest, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}
	return sig.Bundle
}

// componentDigest is the manifest digest the dated Component publishes at, read
// from a publication into a store of its own; the digest depends on content alone.
func componentDigest(t *testing.T) string {
	t.Helper()
	no := false
	res, err := Push(context.Background(), PushConfig{
		EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &no,
		Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: memory.New(), Out: io.Discard,
	})
	if err != nil {
		t.Fatalf("Push (digest source): %v", err)
	}
	return res.Digest
}

// publishAs is one publication of the dated Component.
type publishAs struct {
	ws      string
	sign    bool
	keyPath string
	signer  signing.Signer
}

func pushComponent(t *testing.T, target oras.Target, p publishAs, at time.Time, out io.Writer) (PushResult, error) {
	t.Helper()
	sign := p.sign
	return Push(context.Background(), PushConfig{
		EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &sign, KeyPath: p.keyPath, Signer: p.signer,
		Workspace: resolveIn(t, p.ws, nil), PushTarget: target, Now: func() time.Time { return at }, Out: out,
	})
}

// countReferrers counts what is attached to tag in store, of one artifact type or,
// with an empty type, of every type.
func countReferrers(t *testing.T, store *memory.Store, tag, artifactType string) int {
	t.Helper()
	refs, err := oci.ListReferrers(context.Background(), store, tag, artifactType)
	if err != nil {
		t.Fatalf("ListReferrers %q: %v", artifactType, err)
	}
	return len(refs)
}

// rerunOutcome is what a re-publication reported, read the same way off each verb.
type rerunOutcome struct {
	reference string
	tag       string
	digest    string
	signed    bool
	dated     bool
	already   bool
	reach     Reach
}

// completionRow is one state a version can be left in, and what running its
// publication again must attach to it. Each row carries one fault — one thing the
// version lacks, or one read that cannot be completed — against the control.
type completionRow struct {
	name   string
	defect string
	// land puts the version into the state the row describes, in store.
	land func(t *testing.T, k completionKeys, store *memory.Store)
	// rerun publishes the same content again against target.
	rerun func(t *testing.T, k completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error)
	// wrap, when set, is the target the re-publication reaches instead of store.
	wrap func(store *memory.Store) oras.Target
	// tag is the version tag the row publishes at.
	tag string
	// wantSigs and wantDates are the signature and published-at referrers the
	// re-publication must attach; nothing else may be attached.
	wantSigs, wantDates int
	wantSigned          bool
	// verifyWS, when set, is the workspace whose policy the version's signature
	// must verify under after the re-publication.
	verifyWS func(k completionKeys) string
	// signerUnused is the signer the re-publication was handed, which must not have
	// been asked to sign.
	signerUnused *signingtest.FakeSigner
	// wantErr, when set, is a fragment the re-publication's error must carry.
	wantErr string
}

// The first publication's clock, and the completing run's. A date the completion
// states must be the second: the time the publication was completed.
var (
	firstRunAt      = time.Date(2026, 10, 4, 7, 29, 28, 0, time.UTC)
	completingRunAt = time.Date(2026, 10, 4, 9, 15, 0, 0, time.UTC)
)

// landPartially runs a first publication of the dated Component that fails after
// its content lands, as one whose signature or date could not be attached does,
// and checks that it left the content and nothing beside it.
func landPartially(t *testing.T, store *memory.Store, p publishAs) {
	t.Helper()
	if _, err := pushComponent(t, &refusesAttachments{Store: store}, p, firstRunAt, io.Discard); err == nil {
		t.Fatal("the first publication succeeded; this row needs it to fail after its content landed")
	}
	if _, exists, err := oci.Resolve(context.Background(), store, "1.0.0"); err != nil || !exists {
		t.Fatalf("the first publication did not leave its content: exists=%v err=%v", exists, err)
	}
	if n := countReferrers(t, store, "1.0.0", ""); n != 0 {
		t.Fatalf("the first publication left %d referrers; this row needs none", n)
	}
}

// landCompletely runs a first publication of the dated Component that succeeds.
func landCompletely(t *testing.T, store *memory.Store, p publishAs) {
	t.Helper()
	if _, err := pushComponent(t, store, p, firstRunAt, io.Discard); err != nil {
		t.Fatalf("the first publication failed: %v", err)
	}
}

// rerunComponent publishes the dated Component again and reads its outcome.
func rerunComponent(t *testing.T, target oras.Target, p publishAs, out *bytes.Buffer) (rerunOutcome, error) {
	t.Helper()
	res, err := pushComponent(t, target, p, completingRunAt, out)
	return pushOutcome(res), err
}

// pushOutcome reads a catalog-entity publication's outcome, the Plugin's included.
func pushOutcome(res PushResult) rerunOutcome {
	return rerunOutcome{
		reference: res.Reference, tag: res.Tag, digest: res.Digest,
		signed: res.Signed, dated: res.Dated, already: res.AlreadyPublished, reach: res.Reach(),
	}
}

// TestRunningAPublicationAgainCompletesWhatItLacksAndNothingElse proves that a
// publication of content already published attaches the publication date and the
// signature that version still lacks, through each publishing verb, and attaches
// nothing to a publication that is already complete. The first row is the control:
// a complete publication run again. Every other row carries one fault, named with
// the defect it catches.
func TestRunningAPublicationAgainCompletesWhatItLacksAndNothingElse(t *testing.T) {
	t.Parallel()
	k := newCompletionKeys(t)
	unsigned := publishAs{ws: k.unanchoredWS}
	keyedAnchored := publishAs{ws: keyedWS(k.pubPEM), sign: true, keyPath: k.keyPath}
	keyedUnanchored := publishAs{ws: k.unanchoredWS, sign: true, keyPath: k.keyPath}
	unusedAnchored := &signingtest.FakeSigner{}
	unusedUnanchored := &signingtest.FakeSigner{}
	unusedKeylessAnchored := &signingtest.FakeSigner{}
	unusedKeylessUnanchored := &signingtest.FakeSigner{}

	var pluginEntity, pluginWasm string
	// The artifact file is written once, under THIS test's directory rather than a
	// row's: more than one row publishes it, and a directory a row created is
	// removed when that row ends, so a later row would find the file gone.
	artifactFile := filepath.Join(t.TempDir(), "bundle.bin")
	if err := os.WriteFile(artifactFile, []byte("artifact bytes"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	pushPluginOnce := func(t *testing.T, target oras.Target, at time.Time, out io.Writer) (PushResult, error) {
		t.Helper()
		if pluginEntity == "" {
			pluginEntity, pluginWasm = writeRetriedPlugin(t)
		}
		no := false
		return Push(context.Background(), PushConfig{
			EntityPath: pluginEntity, PluginPath: pluginWasm, Registry: datedRegistry, Sign: &no,
			Workspace: resolveIn(t, k.unanchoredWS, nil), PushTarget: target, Now: func() time.Time { return at }, Out: out,
		})
	}
	pushArtifactOnce := func(t *testing.T, target oras.Target, at time.Time, out io.Writer) (PushArtifactResult, error) {
		t.Helper()
		return PushArtifact(context.Background(), PushArtifactConfig{
			FilePath: artifactFile, Name: "demo", Tag: "2.0.0", Registry: datedRegistry,
			Workspace: resolveIn(t, k.unanchoredWS, nil), PushTarget: target, Now: func() time.Time { return at }, Out: out,
		})
	}

	rows := []completionRow{
		{
			name: "control: a complete unsigned publication", defect: "a complete publication is given a second date",
			tag:  "1.0.0",
			land: func(t *testing.T, _ completionKeys, store *memory.Store) { landCompletely(t, store, unsigned) },
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, unsigned, out)
			},
		},
		{
			name: "(a) an unsigned Component whose date was not attached", defect: "a re-run leaves the version undated",
			tag: "1.0.0", wantDates: 1,
			land: func(t *testing.T, _ completionKeys, store *memory.Store) { landPartially(t, store, unsigned) },
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, unsigned, out)
			},
		},
		{
			name: "(b) an unsigned Plugin whose date was not attached", defect: "a Plugin re-run leaves the version undated",
			tag: "1.0.0", wantDates: 1,
			land: func(t *testing.T, _ completionKeys, store *memory.Store) {
				if _, err := pushPluginOnce(t, &refusesAttachments{Store: store}, firstRunAt, io.Discard); err == nil {
					t.Fatal("the first Plugin publication succeeded; this row needs it to fail after its content landed")
				}
				if n := countReferrers(t, store, "1.0.0", ""); n != 0 {
					t.Fatalf("the first Plugin publication left %d referrers; this row needs none", n)
				}
			},
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				res, err := pushPluginOnce(t, target, completingRunAt, out)
				return pushOutcome(res), err
			},
		},
		{
			name: "(b) a data artifact whose date was not attached", defect: "a data artifact re-run leaves the version undated",
			tag: "2.0.0", wantDates: 1,
			land: func(t *testing.T, _ completionKeys, store *memory.Store) {
				if _, err := pushArtifactOnce(t, &refusesAttachments{Store: store}, firstRunAt, io.Discard); err == nil {
					t.Fatal("the first artifact publication succeeded; this row needs it to fail after its content landed")
				}
				if n := countReferrers(t, store, "2.0.0", ""); n != 0 {
					t.Fatalf("the first artifact publication left %d referrers; this row needs none", n)
				}
			},
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				res, err := pushArtifactOnce(t, target, completingRunAt, out)
				return rerunOutcome{
					reference: res.Reference, tag: res.Tag, digest: res.Digest,
					dated: res.Dated, already: res.AlreadyPublished, reach: res.Reach(),
				}, err
			},
		},
		{
			name: "(b) a complete data artifact run again", defect: "a complete data artifact is given a second date, or its re-run claims to have reached beyond this machine",
			tag: "2.0.0",
			land: func(t *testing.T, _ completionKeys, store *memory.Store) {
				if _, err := pushArtifactOnce(t, store, firstRunAt, io.Discard); err != nil {
					t.Fatalf("the first artifact publication failed: %v", err)
				}
			},
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				res, err := pushArtifactOnce(t, target, completingRunAt, out)
				return rerunOutcome{
					reference: res.Reference, tag: res.Tag, digest: res.Digest,
					dated: res.Dated, already: res.AlreadyPublished, reach: res.Reach(),
				}, err
			},
		},
		{
			name:   "(c) an unsigned publication later signed keyless by reference, with no date",
			defect: "a keyless signature someone attached afterwards stands in for the date an unsigned publication owes",
			tag:    "1.0.0", wantDates: 1,
			land: func(t *testing.T, k completionKeys, store *memory.Store) {
				landPartially(t, store, unsigned)
				digest, _, err := oci.Resolve(context.Background(), store, "1.0.0")
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}
				ref := datedRegistry + "/component/core/api-server:1.0.0"
				if _, err := Sign(context.Background(), SignConfig{
					Workspace: resolveIn(t, k.unanchoredWS, nil), References: []string{ref},
					SubjectRefs: map[string]string{ref: "1.0.0"}, Target: store,
					Signer:  &signingtest.FakeSigner{Bundle: k.keylessOver(t, digest)},
					IDToken: "supplied-token", EnvLookup: func(string) string { return "" }, Out: io.Discard,
				}); err != nil {
					t.Fatalf("Sign by reference: %v", err)
				}
			},
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, unsigned, out)
			},
		},
		{
			name:   "(d) keyed signing to a policy pinning the key, no signature attached",
			defect: "a re-run that signs leaves the version unsigned",
			tag:    "1.0.0", wantSigs: 1, wantDates: 1, wantSigned: true,
			verifyWS: func(k completionKeys) string { return keyedWS(k.pubPEM) },
			land:     func(t *testing.T, _ completionKeys, store *memory.Store) { landPartially(t, store, keyedAnchored) },
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, keyedAnchored, out)
			},
		},
		{
			name:   "(d) keyed signing to a policy pinning no anchor, no signature attached",
			defect: "a re-run to a registry pinning no anchor leaves the version unsigned",
			tag:    "1.0.0", wantSigs: 1, wantDates: 1, wantSigned: true,
			verifyWS: func(k completionKeys) string { return keyedWS(k.pubPEM) },
			land:     func(t *testing.T, _ completionKeys, store *memory.Store) { landPartially(t, store, keyedUnanchored) },
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, keyedUnanchored, out)
			},
		},
		{
			name:   "(d) keyed signing to a policy pinning the key, carrying only a signature that policy does not admit",
			defect: "a signature the destination's policy refuses is taken as the publication's own",
			tag:    "1.0.0", wantSigs: 1, wantDates: 1, wantSigned: true,
			verifyWS: func(k completionKeys) string { return keyedWS(k.pubPEM) },
			land: func(t *testing.T, k completionKeys, store *memory.Store) {
				landPartially(t, store, unsigned)
				attachForeignKeyed(t, k, store)
			},
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, keyedAnchored, out)
			},
		},
		{
			name:   "(d) keyed signing to a policy pinning no anchor, carrying a signature it cannot judge",
			defect: "a registry pinning no anchor draws a second signature, since nothing there can verify",
			tag:    "1.0.0", wantDates: 1,
			land: func(t *testing.T, k completionKeys, store *memory.Store) {
				landPartially(t, store, unsigned)
				attachForeignKeyed(t, k, store)
			},
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, keyedUnanchored, out)
			},
		},
		{
			name:   "(e) keyless signing to a policy admitting the signer, no signature attached",
			defect: "a keyless completion states a date its signature already carries, or leaves the version unsigned",
			tag:    "1.0.0", wantSigs: 1, wantSigned: true,
			verifyWS: func(k completionKeys) string { return k.keylessWS() },
			land: func(t *testing.T, k completionKeys, store *memory.Store) {
				landPartially(t, store, publishAs{ws: k.unanchoredWS, sign: true,
					signer: &signingtest.FakeSigner{Bundle: k.keylessOver(t, componentDigest(t))}})
			},
			rerun: func(t *testing.T, k completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, publishAs{ws: k.keylessWS(), sign: true,
					signer: &signingtest.FakeSigner{Bundle: k.keylessOver(t, componentDigest(t))}}, out)
			},
		},
		{
			name:   "(f) keyless signing to a policy admitting the signer, carrying its verifying keyless signature",
			defect: "a complete keyless publication is signed a second time or given a date",
			tag:    "1.0.0", signerUnused: unusedKeylessAnchored,
			land: func(t *testing.T, k completionKeys, store *memory.Store) {
				landCompletely(t, store, publishAs{ws: k.unanchoredWS, sign: true,
					signer: &signingtest.FakeSigner{Bundle: k.keylessOver(t, componentDigest(t))}})
			},
			rerun: func(t *testing.T, k completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, publishAs{ws: k.keylessWS(), sign: true, signer: unusedKeylessAnchored}, out)
			},
		},
		{
			name:   "(f) keyless signing to a policy pinning no anchor, carrying its keyless signature",
			defect: "a complete keyless publication to a registry pinning no anchor is signed a second time or given a date",
			tag:    "1.0.0", signerUnused: unusedKeylessUnanchored,
			land: func(t *testing.T, k completionKeys, store *memory.Store) {
				landCompletely(t, store, publishAs{ws: k.unanchoredWS, sign: true,
					signer: &signingtest.FakeSigner{Bundle: k.keylessOver(t, componentDigest(t))}})
			},
			rerun: func(t *testing.T, k completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, publishAs{ws: k.unanchoredWS, sign: true, signer: unusedKeylessUnanchored}, out)
			},
		},
		{
			name:   "(g) a complete keyed publication to a policy pinning the key",
			defect: "a complete keyed publication is signed or dated a second time",
			tag:    "1.0.0",
			land:   func(t *testing.T, _ completionKeys, store *memory.Store) { landCompletely(t, store, keyedAnchored) },
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, keyedAnchored, out)
			},
		},
		{
			name:   "(g) a complete keyed publication to a policy pinning no anchor",
			defect: "a complete keyed publication to a registry pinning no anchor is signed or dated a second time",
			tag:    "1.0.0",
			land:   func(t *testing.T, _ completionKeys, store *memory.Store) { landCompletely(t, store, keyedUnanchored) },
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, keyedUnanchored, out)
			},
		},
		{
			name: "(h) an unsigned publication whose date listing cannot be completed", defect: "an unanswered listing is read as no date",
			tag: "1.0.0", wantErr: "whether it states a publication date could not be read",
			wrap: func(store *memory.Store) oras.Target { return &failsListing{Store: store} },
			land: func(t *testing.T, _ completionKeys, store *memory.Store) { landPartially(t, store, unsigned) },
			rerun: func(t *testing.T, _ completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, unsigned, out)
			},
		},
		{
			name:   "(h) signing to a policy pinning the key, whose signature listing cannot be completed",
			defect: "an unanswered listing is read as no verifying signature",
			tag:    "1.0.0", wantErr: "whether it carries a signature that verifies could not be read", signerUnused: unusedAnchored,
			wrap: func(store *memory.Store) oras.Target { return &failsListing{Store: store} },
			land: func(t *testing.T, _ completionKeys, store *memory.Store) { landPartially(t, store, unsigned) },
			rerun: func(t *testing.T, k completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, publishAs{ws: keyedWS(k.pubPEM), sign: true, signer: unusedAnchored}, out)
			},
		},
		{
			name:   "(h) signing to a policy pinning no anchor, whose signature listing cannot be completed",
			defect: "an unanswered listing is read as no signature",
			tag:    "1.0.0", wantErr: "whether it carries a signature could not be read", signerUnused: unusedUnanchored,
			wrap: func(store *memory.Store) oras.Target { return &failsListing{Store: store} },
			land: func(t *testing.T, _ completionKeys, store *memory.Store) { landPartially(t, store, unsigned) },
			rerun: func(t *testing.T, k completionKeys, target oras.Target, out *bytes.Buffer) (rerunOutcome, error) {
				return rerunComponent(t, target, publishAs{ws: k.unanchoredWS, sign: true, signer: unusedUnanchored}, out)
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()
			store := memory.New()
			row.land(t, k, store)
			sigsBefore := countReferrers(t, store, row.tag, signing.BundleMediaType)
			datesBefore := countReferrers(t, store, row.tag, oci.MediaTypePublishedAt)
			allBefore := countReferrers(t, store, row.tag, "")

			var target oras.Target = store
			if row.wrap != nil {
				target = row.wrap(store)
			}
			var out bytes.Buffer
			got, err := row.rerun(t, k, target, &out)

			sigs := countReferrers(t, store, row.tag, signing.BundleMediaType) - sigsBefore
			dates := countReferrers(t, store, row.tag, oci.MediaTypePublishedAt) - datesBefore
			all := countReferrers(t, store, row.tag, "") - allBefore
			if row.signerUnused != nil && len(row.signerUnused.Signed) != 0 {
				t.Errorf("the re-publication signed %v; it must not have been asked to sign (defect: %s)", row.signerUnused.Signed, row.defect)
			}

			if row.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), row.wantErr) {
					t.Fatalf("error = %v; want one carrying %q (defect: %s)\n%s", err, row.wantErr, row.defect, out.String())
				}
				if all != 0 {
					t.Fatalf("a re-publication that failed attached %d referrers; want none (defect: %s)", all, row.defect)
				}
				return
			}
			if err != nil {
				t.Fatalf("the re-publication failed: %v (defect: %s)\n%s", err, row.defect, out.String())
			}

			if sigs != row.wantSigs || dates != row.wantDates || all != row.wantSigs+row.wantDates {
				t.Fatalf("attached %d signatures, %d dates, %d referrers in all; want %d, %d and %d (defect: %s)\n%s",
					sigs, dates, all, row.wantSigs, row.wantDates, row.wantSigs+row.wantDates, row.defect, out.String())
			}
			if !got.already {
				t.Errorf("the re-publication was not reported as already published\n%s", out.String())
			}
			if got.signed != row.wantSigned {
				t.Errorf("Signed = %v; want %v: it reports a signature this call attached", got.signed, row.wantSigned)
			}
			if got.dated != (row.wantDates > 0) {
				t.Errorf("Dated = %v; want %v: it reports a date this call attached", got.dated, row.wantDates > 0)
			}
			// A re-publication uploads nothing, so it reached beyond this machine
			// exactly when it attached a signature or a date — and the control, a
			// complete publication run again, did not.
			if wantBeyond := row.wantSigs+row.wantDates > 0; got.reach.BeyondThisMachine != wantBeyond ||
				len(got.reach.Registries) != 1 || got.reach.Registries[0] != datedRegistry {
				t.Errorf("Reach() = %+v; want registries [%s] and beyond this machine %v (defect: %s)",
					got.reach, datedRegistry, wantBeyond, row.defect)
			}

			// The already-published line keeps the form a reader of its third field
			// depends on, and each attachment follows it in a first publication's own
			// words. A Plugin's advisory notices may precede it, so it is found rather
			// than assumed to open the account.
			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			opening := fmt.Sprintf("Already published: %s:%s (%s)", got.reference, got.tag, got.digest)
			at := -1
			for i, l := range lines {
				if strings.HasPrefix(l, "Already published: ") {
					at = i
					break
				}
			}
			if at < 0 || lines[at] != opening {
				t.Fatalf("the account carries no line %q\n%s", opening, out.String())
			}
			if fields := strings.Fields(lines[at]); len(fields) < 3 || fields[2] != got.reference+":"+got.tag {
				t.Errorf("the third field of %q is not the reference and tag", lines[at])
			}
			sigLine := fmt.Sprintf("Signature attached: %s:%s", got.reference, got.tag)
			dateLine := fmt.Sprintf("Publication date attached: %s:%s (%s)", got.reference, got.tag, oci.FormatPublishedAt(completingRunAt))
			for _, want := range []struct {
				line    string
				present bool
			}{{sigLine, row.wantSigs > 0}, {dateLine, row.wantDates > 0}} {
				found := false
				for _, l := range lines[at+1:] {
					if l == want.line {
						found = true
					}
				}
				if found != want.present {
					t.Errorf("the account carries %q = %v; want %v\n%s", want.line, found, want.present, out.String())
				}
			}

			if row.wantDates > 0 {
				at, found, rerr := oci.ReadPublishedAt(ctx, store, row.tag)
				if rerr != nil || !found {
					t.Fatalf("ReadPublishedAt: found=%v err=%v", found, rerr)
				}
				if !at.Equal(completingRunAt) {
					t.Errorf("the stated date is %v; want the completing run's clock %v", at, completingRunAt)
				}
			}
			if row.verifyWS != nil {
				rw := resolveIn(t, row.verifyWS(k), nil)
				policy, _ := rw.Config().RegistryTrustPolicy(datedRegistry)
				res, ferr := registryindex.FindVerifiedSigner(ctx, rw.Config(), store, row.tag, got.digest, policy)
				if ferr != nil || res == nil {
					t.Errorf("after the re-publication no signature verifies under the destination's policy: %v", ferr)
				}
			}
		})
	}
}

// attachForeignKeyed attaches to the dated Component a keyed signature made with
// the key no destination in these rows admits.
func attachForeignKeyed(t *testing.T, k completionKeys, store *memory.Store) {
	t.Helper()
	ctx := context.Background()
	pemBytes, err := os.ReadFile(k.foreignKey)
	if err != nil {
		t.Fatalf("read foreign key: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(pemBytes, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	digest, _, err := oci.Resolve(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	bundle, artifactType, err := signer.Sign(ctx, digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", artifactType, bundle); err != nil {
		t.Fatalf("attach foreign signature: %v", err)
	}
}
