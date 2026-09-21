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

// TestPreviewPinsIsTheWriteWithoutTheWrite holds the two halves of the freeze to
// each other over the three cases a freeze can be in: it adds pins, it changes
// pins, and it leaves a recipe whose pins already agree alone.
//
// The comparison is what the split exists for. The preview and the write reach
// their bytes through one splice, so a caller reporting what a freeze would do
// and a caller performing it cannot come to disagree — and the assertion below
// is the thing that would fail if a second rendering were ever introduced.
//
// Each case also asserts that the preview left the file alone. That is not the
// same claim as "the bytes matched": a preview that produced the right answer and
// wrote it on the way past would satisfy the byte comparison and be exactly the
// defect the mode exists to avoid.
func TestPreviewPinsIsTheWriteWithoutTheWrite(t *testing.T) {
	first := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	firstArt := []types.ArtifactDigest{{Name: "extra.yaml", Digest: "sha256:bbb"}}
	second := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "2.0.0", Digest: "sha256:ccc"}}

	for _, tc := range []struct {
		name string
		// seed, when non-nil, is frozen into the recipe before the case runs, so
		// the case starts from a recipe that already carries pins.
		seedSrc  []types.SourceDigest
		seedArt  []types.ArtifactDigest
		src      []types.SourceDigest
		art      []types.ArtifactDigest
		rewrites bool
	}{
		{name: "adds pins to a recipe that carries none", src: first, art: firstArt, rewrites: true},
		{name: "changes pins that moved", seedSrc: first, seedArt: firstArt, src: second, art: firstArt, rewrites: true},
		{name: "leaves a recipe whose pins already agree", seedSrc: first, seedArt: firstArt, src: first, art: firstArt, rewrites: false},
		{name: "removes pins a recipe no longer needs", seedSrc: first, seedArt: firstArt, rewrites: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeRecipe(t, bareRecipe)
			if tc.seedSrc != nil || tc.seedArt != nil {
				if _, err := recipepin.WritePins(path, tc.seedSrc, tc.seedArt); err != nil {
					t.Fatalf("seed the recipe: %v", err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the seeded recipe: %v", err)
			}

			previewed, rewrites, err := recipepin.PreviewPins(path, tc.src, tc.art)
			if err != nil {
				t.Fatalf("PreviewPins: %v", err)
			}
			if rewrites != tc.rewrites {
				t.Errorf("PreviewPins reported rewrites=%v; want %v", rewrites, tc.rewrites)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the recipe after the preview: %v", err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("PreviewPins rewrote the file;\nbefore:\n%s\nafter:\n%s", before, after)
			}

			wrote, err := recipepin.WritePins(path, tc.src, tc.art)
			if err != nil {
				t.Fatalf("WritePins: %v", err)
			}
			if wrote != tc.rewrites {
				t.Errorf("WritePins reported changed=%v; want the same %v PreviewPins reported", wrote, tc.rewrites)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the written recipe: %v", err)
			}
			if !bytes.Equal(previewed, written) {
				t.Errorf("the previewed bytes are not the written bytes;\npreviewed:\n%s\nwritten:\n%s", previewed, written)
			}
		})
	}
}
