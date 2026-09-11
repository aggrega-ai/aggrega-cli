// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"oras.land/oras-go/v2/content/memory"
)

// seedVerifiedSlot writes the two-blob Tier-2 cache slot a real fetch lays down —
// the content, and the OCI manifest that commits to it — for uri@version, and
// returns the content digest and manifest digest a lockfile would record for it.
//
// It rebuilds the manifest by round-tripping the body through an in-memory
// content store rather than hand-assembling one, so the manifest is the same
// shape the verifying read checks against instead of a fixture that models one.
func seedVerifiedSlot(t *testing.T, ws, cacheDir, uri, version string, body []byte) (contentPin, manifestPin string) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", oci.MediaTypeRecipe, body, nil); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	res, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	if err := oci.WriteCached(ws, cacheDir, uri, version, body); err != nil {
		t.Fatalf("seed write content: %v", err)
	}
	if err := oci.WriteCachedManifest(ws, cacheDir, uri, version, res.ManifestBytes); err != nil {
		t.Fatalf("seed write manifest: %v", err)
	}
	return digest.ContentDigest(body), res.Digest
}

// TestDiscoveryRenderIsPinnedToTheLockfileItRecords proves that the pin set the
// discovery render is held to is the set the lockfile records, for a URI whose
// digest the recipe freeze rewrote.
//
// The two sets cannot be separated by comparing rendered bytes. A writable recipe
// is read from the local catalog rather than from a pinned cache slot, so a stale
// pin for it changes no output today and a whole-artifact comparison passes under
// either ordering. What does separate them is the verifying read itself: it
// recomputes a cache slot's content digest and compares it to the pin the view
// holds, so a view holding the pre-freeze digest reports drift against the very
// slot a view holding the lockfile's digest accepts.
//
// The fixture is built around that. The slot carries the POST-freeze bytes — what
// the lockfile records — while the view is seeded with the PRE-freeze digest,
// which is the state the resolution leaves behind. The first assertion confirms
// that seeded state really does disagree with the slot, so the second assertion
// is known to be capable of failing rather than assumed to be.
func TestDiscoveryRenderIsPinnedToTheLockfileItRecords(t *testing.T) {
	t.Parallel()

	const (
		uri      = "core/database"
		version  = "1.0.0"
		cacheDir = ".aggrega/cache"
	)
	ws := t.TempDir()

	// The recipe file as it stands AFTER the freeze appended its intrinsic
	// content pins. This is what the lockfile records and what a build re-hashes.
	postFreeze := []byte("apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Recipe\nmetadata:\n  name: database\n  version: 1.0.0\n" +
		"spec:\n  sourceDigests:\n    - digest: sha256:frozen\n")
	// The same recipe as the resolution digested it, BEFORE the freeze wrote the
	// pins in. This is the digest the closure-derived pin set carries.
	preFreeze := []byte("apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Recipe\nmetadata:\n  name: database\n  version: 1.0.0\n" +
		"spec: {}\n")

	postFreezeDigest, manifestDigest := seedVerifiedSlot(t, ws, cacheDir, uri+":"+version, version, postFreeze)
	preFreezeDigest := digest.ContentDigest(preFreeze)
	if preFreezeDigest == postFreezeDigest {
		t.Fatal("fixture does not exercise the freeze: the pre- and post-freeze bodies digest identically")
	}

	// What buildLockfile produces once the post-freeze overlay is applied: the
	// digest of the recipe file as the freeze left it.
	lf := &lockfile.Lockfile{
		Version: 1,
		Entities: []lockfile.Entity{{
			URI:             uri,
			ResolvedVersion: version,
			Digest:          postFreezeDigest,
			ManifestDigest:  manifestDigest,
		}},
	}

	view := catalog.NewLazyLookup(ws, cacheDir, nil, nil)
	// The state the resolution leaves the view in: pinned from the closure it
	// walked, which digested the recipe before the freeze rewrote it.
	view.SetPins(map[string]catalog.Pin{uri: {
		ResolvedVersion: version,
		Digest:          preFreezeDigest,
		ManifestDigest:  manifestDigest,
	}})

	// Positive control. The pre-freeze pin must genuinely disagree with the slot,
	// otherwise the assertion below would pass on a view nobody re-pinned and
	// would be proving nothing.
	if err := view.VerifyPinnedIntegrity(); !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("pre-freeze pin does not disagree with the recorded slot, so this test cannot detect the defect it names: got %v", err)
	}

	// The render cannot complete here — there is no Release to render — and it is
	// not what is under test. What is under test is which pins the view is
	// carrying by the time the render is reached, and that is settled before the
	// render begins, so the returned error is deliberately not consulted.
	_, _, _ = discoverExternalInputs(
		context.Background(),
		Config{ReleasePath: "no-such-release.yaml"},
		workspaceconfig.Config{},
		ws,
		cacheDir,
		view,
		lf,
	)

	if err := view.VerifyPinnedIntegrity(); err != nil {
		t.Fatalf("the render is held to a pin the lockfile does not record: %v\n"+
			"the lockfile records %s for %s; the view still carries %s",
			err, postFreezeDigest, uri, preFreezeDigest)
	}
}
