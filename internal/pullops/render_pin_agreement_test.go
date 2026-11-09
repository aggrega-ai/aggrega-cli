// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"oras.land/oras-go/v2/content/memory"
)

// seedVerifiedSlot writes the two-blob Tier-2 cache slot a real fetch lays down —
// the content, and the OCI manifest that commits to it — for uri@version, and
// returns the entity row's digest (rowDigest) and manifest digest a lockfile would
// record for it.
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
	if err := oci.WriteCached(ws, cacheDir, "Recipe", uri, version, body); err != nil {
		t.Fatalf("seed write content: %v", err)
	}
	if err := oci.WriteCachedManifest(ws, cacheDir, "Recipe", uri, version, res.ManifestBytes); err != nil {
		t.Fatalf("seed write manifest: %v", err)
	}
	return rowDigest(t, body), res.Digest
}

// rowDigest is the digest a lockfile row records for published bytes, through the
// product's own rule (catalog.PinDigest): for a Recipe published as its document
// alone, the digest of the one-entry set it carries rather than of its bytes.
func rowDigest(t *testing.T, published []byte) string {
	t.Helper()
	d, err := catalog.PinDigest(published)
	if err != nil {
		t.Fatalf("PinDigest: %v", err)
	}
	return d
}

// TestDiscoveryRenderIsPinnedToTheLockfileItRecords proves that the pin set the
// discovery render is held to is the set the LOCKFILE records, rather than
// whatever the closure walk left on the view.
//
// The two sets cannot be separated by comparing rendered bytes. A writable recipe
// is read from the local catalog rather than from a pinned cache slot, so a stale
// pin for it changes no output today and a whole-artifact comparison passes under
// either ordering. What does separate them is the verifying read itself: it
// recomputes the row digest of a cache slot's content and compares it to the pin
// the view holds, so a view holding a stale digest reports drift against the very slot a
// view holding the lockfile's digest accepts.
//
// The fixture is built around that. The slot carries the bytes the lockfile
// records, while the view is seeded with the digest of different bytes — the
// shape a view is in whenever what it walked and what the lockfile settled on
// have diverged. The first assertion confirms that seeded state really does
// disagree with the slot, so the second assertion is known to be capable of
// failing rather than assumed to be.
func TestDiscoveryRenderIsPinnedToTheLockfileItRecords(t *testing.T) {
	t.Parallel()

	const (
		uri      = "core/database"
		version  = "1.0.0"
		cacheDir = ".aggrega/cache"
	)
	ws := t.TempDir()

	// The recipe body the lockfile records and a build re-hashes.
	recorded := []byte("apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Recipe\nmetadata:\n  name: database\n  version: 1.0.0\n" +
		"spec:\n  capability: helm\n")
	// A different body, standing in for whatever the view was left holding. Only
	// its digest is used; the bytes need to differ and nothing more.
	divergent := []byte("apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Recipe\nmetadata:\n  name: database\n  version: 1.0.0\n" +
		"spec: {}\n")

	recordedDigest, manifestDigest := seedVerifiedSlot(t, ws, cacheDir, uri+":"+version, version, recorded)
	staleDigest := rowDigest(t, divergent)
	if staleDigest == recordedDigest {
		t.Fatal("fixture is inert: the two bodies digest identically, so no view state can disagree with the slot")
	}

	// What buildLockfile produces: the row digest of the recipe as it stands.
	lf := &lockfile.Lockfile{
		Version: 1,
		Entities: []lockfile.Entity{{
			Kind:            closure.KindRecipe,
			URI:             uri,
			ResolvedVersion: version,
			Digest:          recordedDigest,
			ManifestDigest:  manifestDigest,
		}},
	}

	view := catalog.NewLazyLookup(ws, cacheDir, nil, nil)
	// The state the resolution can leave the view in: pinned from the closure it
	// walked rather than from the lockfile the run went on to settle.
	view.SetPins(catalog.Pins{{Kind: "Recipe", URI: uri}: {
		ResolvedVersion: version,
		Digest:          staleDigest,
		ManifestDigest:  manifestDigest,
	}})

	// Positive control. The seeded pin must genuinely disagree with the slot,
	// otherwise the assertion below would pass on a view nobody re-pinned and
	// would be proving nothing.
	if err := view.VerifyPinnedIntegrity(); !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("the seeded pin does not disagree with the recorded slot, so this test cannot detect the defect it names: got %v", err)
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
		nil,
	)

	if err := view.VerifyPinnedIntegrity(); err != nil {
		t.Fatalf("the render is held to a pin the lockfile does not record: %v\n"+
			"the lockfile records %s for %s; the view still carries %s",
			err, recordedDigest, uri, staleDigest)
	}
}
