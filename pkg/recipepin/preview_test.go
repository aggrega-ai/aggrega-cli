// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPreviewLockIsTheWriteWithoutTheWrite holds the two halves of the pin
// record to each other over the four states a resolution can leave it in: it
// adds pins, it changes pins, it leaves a lock whose pins already agree alone,
// and it removes a lock nothing needs any more.
//
// The comparison is what the split exists for. The preview and the write reach
// their bytes through one body, so a caller reporting what a resolution would do
// and a caller performing it cannot come to disagree — and the assertion below
// is the thing that would fail if a second rendering were ever introduced.
//
// Each case also asserts that the preview left the file alone. That is not the
// same claim as "the bytes matched": a preview that produced the right answer and
// wrote it on the way past would satisfy the byte comparison and be exactly the
// defect the mode exists to avoid.
func TestPreviewLockIsTheWriteWithoutTheWrite(t *testing.T) {
	first := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	firstArt := []types.ArtifactDigest{{Name: "extra.yaml", Digest: "sha256:bbb"}}
	second := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "2.0.0", Digest: "sha256:ccc"}}
	chart := []types.LocalSourceDigest{{Path: "chart/Chart.yaml", Digest: "sha256:ddd"}, {Path: "chart/templates/cm.yaml", Digest: "sha256:eee"}}
	editedChart := []types.LocalSourceDigest{{Path: "chart/Chart.yaml", Digest: "sha256:ddd"}, {Path: "chart/templates/cm.yaml", Digest: "sha256:fff"}}

	for _, tc := range []struct {
		name string
		// seed, when it records anything, is written before the case runs, so the
		// case starts from an entity that already carries a lock.
		seed     recipepin.Pins
		want     recipepin.Pins
		rewrites bool
	}{
		{name: "adds a lock to an entity that carries none", want: recipepin.Pins{SourceDigests: first, ArtifactDigests: firstArt}, rewrites: true},
		{name: "changes pins that moved", seed: recipepin.Pins{SourceDigests: first, ArtifactDigests: firstArt}, want: recipepin.Pins{SourceDigests: second, ArtifactDigests: firstArt}, rewrites: true},
		{name: "leaves a lock whose pins already agree", seed: recipepin.Pins{SourceDigests: first, ArtifactDigests: firstArt}, want: recipepin.Pins{SourceDigests: first, ArtifactDigests: firstArt}, rewrites: false},
		{name: "removes a lock the entity no longer needs", seed: recipepin.Pins{SourceDigests: first, ArtifactDigests: firstArt}, rewrites: true},
		{name: "reports nothing for an entity that never had a lock", rewrites: false},
		{name: "adds a lock for a local source alone", want: recipepin.Pins{LocalSourceDigests: chart}, rewrites: true},
		{name: "changes a local source file's pin that moved", seed: recipepin.Pins{LocalSourceDigests: chart}, want: recipepin.Pins{LocalSourceDigests: editedChart}, rewrites: true},
		{name: "leaves a local source's pins that already agree", seed: recipepin.Pins{ArtifactDigests: firstArt, LocalSourceDigests: chart}, want: recipepin.Pins{ArtifactDigests: firstArt, LocalSourceDigests: chart}, rewrites: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeRecipe(t, bareRecipe)
			lockPath := recipepin.LockPath(path)
			if !tc.seed.Empty() {
				if _, err := recipepin.WriteLock(path, tc.seed); err != nil {
					t.Fatalf("seed the lock: %v", err)
				}
			}
			before, beforeErr := os.ReadFile(lockPath)
			recipeBefore := mustRead(t, path)

			previewed, rewrites, err := recipepin.PreviewLock(path, tc.want)
			if err != nil {
				t.Fatalf("PreviewLock: %v", err)
			}
			if rewrites != tc.rewrites {
				t.Errorf("PreviewLock reported rewrites=%v; want %v", rewrites, tc.rewrites)
			}
			after, afterErr := os.ReadFile(lockPath)
			if (beforeErr == nil) != (afterErr == nil) || !bytes.Equal(after, before) {
				t.Fatalf("PreviewLock wrote to the lock;\nbefore:\n%s\nafter:\n%s", before, after)
			}

			wrote, err := recipepin.WriteLock(path, tc.want)
			if err != nil {
				t.Fatalf("WriteLock: %v", err)
			}
			if wrote != tc.rewrites {
				t.Errorf("WriteLock reported changed=%v; want the same %v PreviewLock reported", wrote, tc.rewrites)
			}
			written, werr := os.ReadFile(lockPath)
			if werr != nil {
				// A removal leaves no file, which is the write that corresponds to a
				// preview carrying no bytes.
				written = nil
			}
			if !bytes.Equal(previewed, written) {
				t.Errorf("the previewed bytes are not the written bytes;\npreviewed:\n%s\nwritten:\n%s", previewed, written)
			}

			// Neither half of the pair is allowed near the authored document.
			if got := mustRead(t, path); !bytes.Equal(got, recipeBefore) {
				t.Errorf("the authored document moved across the preview-then-write pair")
			}
		})
	}
}
