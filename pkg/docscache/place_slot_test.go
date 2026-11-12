// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package docscache

// A write moves aside only the slot that stood in place when it was ready to
// stage. A slot another writer placed since is that writer's result, and the write
// takes it for its own rather than setting it aside. These cases build each state
// a placement can meet — no slot found before staging, another slot found then, or
// the very slot that still stands — and drive the real placement over it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// placedTwice places a slot for one bundle and then replaces it with a second
// placement of the same bundle, as two writers one after the other would. It
// returns the slot's path and the identities of the first slot and of the second,
// which is the one left standing.
func placedTwice(t *testing.T) (slot string, first, second os.FileInfo) {
	t.Helper()
	const ref, version = "reg.example.com/docs/aggrega-docs", "1.0.0"
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, version, oci.MediaTypeDocs, []byte("bundle content"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	root := t.TempDir()
	slot, err := ResolveAndCache(ctx, store, ref, version, root, nil)
	if err != nil {
		t.Fatalf("first placement: %v", err)
	}
	if first, err = slotIdentity(slot); err != nil {
		t.Fatalf("the first slot: %v", err)
	}
	if _, err := ResolveAndCache(ctx, store, ref, version, root, nil); err != nil {
		t.Fatalf("second placement: %v", err)
	}
	if second, err = slotIdentity(slot); err != nil {
		t.Fatalf("the second slot: %v", err)
	}
	if os.SameFile(first, second) {
		t.Fatal("the second placement left the first slot standing, so the cases below would prove nothing")
	}
	return slot, first, second
}

// TestAPlacementSetsAsideOnlyTheSlotItFoundBeforeStaging drives one placement
// over a slot standing in place, for each slot the write could have found before
// it staged.
func TestAPlacementSetsAsideOnlyTheSlotItFoundBeforeStaging(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// found picks the slot the write found before staging, out of the first
		// placement, the second (the one standing), or none.
		found func(first, second os.FileInfo) os.FileInfo
		// replaced is whether the staged slot is to stand afterwards, the slot
		// that stood having been set aside.
		replaced bool
	}{
		{
			name:  "none was found, and another writer has placed one since",
			found: func(_, _ os.FileInfo) os.FileInfo { return nil },
		},
		{
			name:  "another slot was found, and another writer has replaced it since",
			found: func(first, _ os.FileInfo) os.FileInfo { return first },
		},
		{
			name:     "the slot found is the one still standing",
			found:    func(_, second os.FileInfo) os.FileInfo { return second },
			replaced: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			slot, first, standing := placedTwice(t)
			blob, err := os.ReadFile(filepath.Join(slot, blobFile))
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := os.ReadFile(filepath.Join(slot, manifestFile))
			if err != nil {
				t.Fatal(err)
			}
			staging, err := os.MkdirTemp(filepath.Dir(slot), transientPrefix+"staging-*")
			if err != nil {
				t.Fatal(err)
			}
			if err := writeSlot(staging, blob, manifest, nil); err != nil {
				t.Fatal(err)
			}
			staged, err := slotIdentity(staging)
			if err != nil {
				t.Fatal(err)
			}

			if err := placeSlot(staging, slot, "reg.example.com/docs/aggrega-docs", "1.0.0", tc.found(first, standing)); err != nil {
				t.Fatalf("placeSlot: %v", err)
			}

			after, err := slotIdentity(slot)
			if err != nil {
				t.Fatalf("no slot stands after the placement: %v", err)
			}
			_, stagingErr := os.Stat(staging)
			switch {
			case tc.replaced && !os.SameFile(after, staged):
				t.Errorf("the slot the write found still stands; the staged slot should have replaced it")
			case !tc.replaced && !os.SameFile(after, standing):
				t.Errorf("the slot another writer placed was set aside; it should stand as this write's result")
			case !tc.replaced && stagingErr != nil:
				t.Errorf("the staging was moved (%v); it should be left for the caller to discard", stagingErr)
			}
			entries, err := os.ReadDir(filepath.Dir(slot))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name() != filepath.Base(slot) && e.Name() != filepath.Base(staging) {
					t.Errorf("the placement left %s beside the slot", e.Name())
				}
			}
		})
	}
}
