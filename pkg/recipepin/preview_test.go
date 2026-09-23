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

	for _, tc := range []struct {
		name string
		// seed, when non-nil, is written before the case runs, so the case starts
		// from an entity that already carries a lock.
		seedSrc  []types.SourceDigest
		seedArt  []types.ArtifactDigest
		src      []types.SourceDigest
		art      []types.ArtifactDigest
		rewrites bool
	}{
		{name: "adds a lock to an entity that carries none", src: first, art: firstArt, rewrites: true},
		{name: "changes pins that moved", seedSrc: first, seedArt: firstArt, src: second, art: firstArt, rewrites: true},
		{name: "leaves a lock whose pins already agree", seedSrc: first, seedArt: firstArt, src: first, art: firstArt, rewrites: false},
		{name: "removes a lock the entity no longer needs", seedSrc: first, seedArt: firstArt, rewrites: true},
		{name: "reports nothing for an entity that never had a lock", rewrites: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeRecipe(t, bareRecipe)
			lockPath := recipepin.LockPath(path)
			if tc.seedSrc != nil || tc.seedArt != nil {
				if _, err := recipepin.WriteLock(path, tc.seedSrc, tc.seedArt); err != nil {
					t.Fatalf("seed the lock: %v", err)
				}
			}
			before, beforeErr := os.ReadFile(lockPath)
			recipeBefore := mustRead(t, path)

			previewed, rewrites, err := recipepin.PreviewLock(path, tc.src, tc.art)
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

			wrote, err := recipepin.WriteLock(path, tc.src, tc.art)
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
