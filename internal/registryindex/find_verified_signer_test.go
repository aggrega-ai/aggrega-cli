// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryindex

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// listingFails is a store whose referrer listing cannot be completed.
type listingFails struct {
	*memory.Store
}

func (l *listingFails) Predecessors(context.Context, ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return nil, errors.New("the test registry cannot list referrers")
}

// fetchFails is a store that cannot serve the one blob it names by digest. It
// lists the referrer manifest named by first ahead of every other, so a walk over
// the referrers meets that one before any other; the in-memory store alone lists
// them in no fixed order.
//
// The unfetchable blob is a signature's payload, never its referrer manifest:
// listing referrers over the in-memory store reads every referrer manifest to
// learn its artifact type, so an unfetchable manifest fails the listing rather
// than the fetch of the one signature.
type fetchFails struct {
	*memory.Store
	unfetchable string
	first       string
}

func (f *fetchFails) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if target.Digest.String() == f.unfetchable {
		return nil, errors.New("the test registry cannot serve this blob")
	}
	return f.Store.Fetch(ctx, target)
}

func (f *fetchFails) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	preds, err := f.Store.Predecessors(ctx, node)
	if err != nil {
		return nil, err
	}
	for i, p := range preds {
		if p.Digest.String() == f.first {
			preds[0], preds[i] = preds[i], preds[0]
			break
		}
	}
	return preds, nil
}

// payloadDigest returns the digest of the one payload layer a referrer manifest
// in store points at — for a signature, its bundle.
func payloadDigest(t *testing.T, store *memory.Store, referrer ocispec.Descriptor) string {
	t.Helper()
	raw, err := content.FetchAll(context.Background(), store, referrer)
	if err != nil {
		t.Fatalf("fetch referrer manifest: %v", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode referrer manifest: %v", err)
	}
	if len(manifest.Layers) != 1 {
		t.Fatalf("referrer manifest carries %d layers; want 1", len(manifest.Layers))
	}
	return manifest.Layers[0].Digest.String()
}

// newKeyedSigner returns a signer over a fresh key and the public half as PEM.
func newKeyedSigner(t *testing.T) (signing.Signer, string) {
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
	signer, err := signing.LoadKeyedSigner(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	return signer, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
}

// signVersion signs dg and attaches the signature to version 1.0.0 in store,
// returning the referrer it attached.
func signVersion(t *testing.T, store *memory.Store, signer signing.Signer, dg string) ocispec.Descriptor {
	t.Helper()
	bundle, artifactType, err := signer.Sign(context.Background(), dg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	referrer, err := oci.AttachReferrer(context.Background(), store, "1.0.0", artifactType, bundle)
	if err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}
	return referrer
}

// publishVersion publishes version 1.0.0 into store and returns its digest.
func publishVersion(t *testing.T, store *memory.Store) string {
	t.Helper()
	dg, err := oci.Push(context.Background(), store, "1.0.0", oci.MediaTypeComponent, []byte("# a published version\n"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	return dg
}

// TestFindVerifiedSignerSaysWhenItCouldNotFinish proves the one signature walk
// answers "none verifies" only when it read and judged every signature, and
// answers with an error where a read failed in a way that leaves the answer
// unknown — while the best-effort reading of the same walk, VerifiedSignerAt,
// still answers a verified signer or nil exactly as before. The first row is the
// control; each other row carries one fault.
func TestFindVerifiedSignerSaysWhenItCouldNotFinish(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// build publishes a version, attaches what the row needs, and returns the
		// target to read, the version's digest and the policy to judge it under.
		build        func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig)
		wantVerified bool
		wantErr      bool
	}{
		{
			name: "control: a signature that verifies",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				signer, pub := newKeyedSigner(t)
				signVersion(t, store, signer, dg)
				return store, dg, workspaceconfig.RegistryConfig{PublicKey: pub}
			},
			wantVerified: true,
		},
		{
			name: "no signature at all is an answer",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				_, pub := newKeyedSigner(t)
				return store, dg, workspaceconfig.RegistryConfig{PublicKey: pub}
			},
		},
		{
			name: "a signature the policy does not admit is an answer",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				signer, _ := newKeyedSigner(t)
				signVersion(t, store, signer, dg)
				_, other := newKeyedSigner(t)
				return store, dg, workspaceconfig.RegistryConfig{PublicKey: other}
			},
		},
		{
			name: "a payload that is not a bundle is an answer",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				if _, err := oci.AttachReferrer(context.Background(), store, "1.0.0", signing.BundleMediaType, []byte("not a bundle")); err != nil {
					t.Fatalf("AttachReferrer: %v", err)
				}
				_, pub := newKeyedSigner(t)
				return store, dg, workspaceconfig.RegistryConfig{PublicKey: pub}
			},
		},
		{
			name: "a listing that cannot be completed is not an answer",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				signer, pub := newKeyedSigner(t)
				signVersion(t, store, signer, dg)
				return &listingFails{Store: store}, dg, workspaceconfig.RegistryConfig{PublicKey: pub}
			},
			wantErr: true,
		},
		{
			name: "a signature that cannot be fetched, with none other verifying, is not an answer",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				signer, pub := newKeyedSigner(t)
				referrer := signVersion(t, store, signer, dg)
				return &fetchFails{Store: store, unfetchable: payloadDigest(t, store, referrer)}, dg, workspaceconfig.RegistryConfig{PublicKey: pub}
			},
			wantErr: true,
		},
		{
			name: "a signature that cannot be fetched beside one that verifies is an answer",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				foreign, _ := newKeyedSigner(t)
				unreadable := signVersion(t, store, foreign, dg)
				signer, pub := newKeyedSigner(t)
				signVersion(t, store, signer, dg)
				return &fetchFails{
					Store:       store,
					unfetchable: payloadDigest(t, store, unreadable),
					first:       unreadable.Digest.String(),
				}, dg, workspaceconfig.RegistryConfig{PublicKey: pub}
			},
			wantVerified: true,
		},
		{
			name: "a pinned key that cannot be read is not an answer",
			build: func(t *testing.T) (oras.ReadOnlyTarget, string, workspaceconfig.RegistryConfig) {
				store := memory.New()
				dg := publishVersion(t, store)
				signer, _ := newKeyedSigner(t)
				signVersion(t, store, signer, dg)
				return store, dg, workspaceconfig.RegistryConfig{PublicKey: "not a public key"}
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			target, dg, policy := tc.build(t)
			res, err := FindVerifiedSigner(ctx, workspaceconfig.Config{}, target, "1.0.0", dg, policy)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v; want an error: %v", err, tc.wantErr)
			}
			if (res != nil) != tc.wantVerified {
				t.Fatalf("verified signer = %+v; want one: %v", res, tc.wantVerified)
			}
			best := VerifiedSignerAt(ctx, workspaceconfig.Config{}, target, "1.0.0", dg, policy)
			if (best != nil) != tc.wantVerified {
				t.Errorf("the best-effort reading answered %+v; want a verified signer: %v", best, tc.wantVerified)
			}
		})
	}
}
