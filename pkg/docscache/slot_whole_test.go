// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docscache_test

// A slot is whole or absent, and the signatures a slot keeps survive a fetch
// that replaces it.
//
// Several processes on one machine acquire the same bundle into the same slot —
// two assistant sessions started together are two processes — so a write that
// filled the slot in place would let a reader meet a slot holding a content blob
// and no manifest yet. These cases drive the real write under contention and
// over a write that stopped part-way, and read back through the real read.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// attachKeyedSignature signs the manifest at testVersion in store with a fresh
// key, attaches the signature as a referrer, and returns the key's public PEM.
func attachKeyedSignature(t *testing.T, store *memory.Store) []byte {
	t.Helper()
	ctx := context.Background()
	desc, err := store.Resolve(ctx, testVersion)
	if err != nil {
		t.Fatalf("resolve %s: %v", testVersion, err)
	}
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	sig, artifactType, err := signer.Sign(ctx, desc.Digest.String())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, testVersion, artifactType, sig); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}
	return signer.PublicKeyPEM()
}

// refEntries lists what one reference's directory holds.
func refEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(docscache.DocsBundlesRoot(root), pathsegment.Encode(testRef)))
	if err != nil {
		t.Fatalf("read the reference directory: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestConcurrentAcquisitionsLeaveOneWholeSlot drives several writes of one bundle
// into one slot at once. Every write reports success, the slot that stands serves
// the page, and nothing a write staged or set aside is left beside it.
func TestConcurrentAcquisitionsLeaveOneWholeSlot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store := memory.New()
	if _, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs,
		buildBundle(t, map[string]string{componentPage: componentBody}), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	const writers = 8
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, nil)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d failed: %v", i, err)
		}
	}

	if data, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, admitAll); err != nil || string(data) != componentBody {
		t.Fatalf("the slot left standing does not serve the page: %q (%v)", data, err)
	}
	if got := refEntries(t, root); len(got) != 1 || got[0] != pathsegment.Encode(testVersion) {
		t.Errorf("the reference directory holds %v, want only the slot %q", got, pathsegment.Encode(testVersion))
	}
}

// TestAWriteThatStoppedPartWayIsNotASlot plants what a write interrupted
// mid-way leaves — a staging directory holding the content and no manifest — and
// holds the probe, the acquisition's question and the read to answering that no
// bundle is cached, and the slot-name predicate to not naming it.
func TestAWriteThatStoppedPartWayIsNotASlot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	staging := filepath.Join(docscache.DocsBundlesRoot(root), pathsegment.Encode(testRef), "~staging-123")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "blob"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	if cached, err := docscache.Cached(root, testRef, testVersion); err != nil || cached {
		t.Errorf("the probe answered (%v, %v) over a write that never finished", cached, err)
	}
	if err := docscache.Admitted(root, testRef, testVersion, admitAll); !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Errorf("the acquisition's question answered %v, want no bundle", err)
	}
	if _, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, admitAll); !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Errorf("the read answered %v, want no bundle", err)
	}
	if docscache.IsSlotName(filepath.Base(staging)) {
		t.Errorf("%s is named as a slot", filepath.Base(staging))
	}
	if !docscache.IsSlotName(pathsegment.Encode(testVersion)) {
		t.Errorf("the slot name %s is not named as a slot", pathsegment.Encode(testVersion))
	}
}

// TestAReplacingFetchKeepsTheSignaturesTheSlotHeldForTheSameBundle is the case
// that keeps two readers trusting different signers from evicting each other: a
// bundle fetched under one reader's check keeps the signature that check
// verified, and a fetch of the same bundle under another check, replacing the
// slot, keeps it beside its own. Both readers are then admitted by the one copy.
func TestAReplacingFetchKeepsTheSignaturesTheSlotHeldForTheSameBundle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store := memory.New()
	if _, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs,
		buildBundle(t, map[string]string{componentPage: componentBody}), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	firstKey := attachKeyedSignature(t, store)
	secondKey := attachKeyedSignature(t, store)

	if _, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, keyedVerifier(store, firstKey)); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if err := docscache.Admitted(root, testRef, testVersion, keyedAdmission(secondKey)); err == nil {
		t.Fatal("a reader trusting only the second key was admitted before any fetch under its check, so the case below would prove nothing")
	}

	slot, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, keyedVerifier(store, secondKey))
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	kept, err := os.ReadDir(filepath.Join(slot, "signatures"))
	if err != nil || len(kept) != 2 {
		t.Fatalf("the replacing slot keeps %d signature(s) (%v), want both", len(kept), err)
	}
	for name, key := range map[string][]byte{"first": firstKey, "second": secondKey} {
		if _, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, keyedAdmission(key)); err != nil {
			t.Errorf("the reader trusting the %s key is refused the copy that keeps its signature: %v", name, err)
		}
	}
}
