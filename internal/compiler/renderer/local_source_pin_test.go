// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestALocalSourceIsComparedWithItsPinsBeforeItIsForwarded drives the build's
// local-source comparison through the one body every render site resolves a
// source through, over the cells it has to tell apart.
//
// The two halves are both read rather than computed by the render: the recorded
// pins from the input's record of the lock, keyed by the entity's document, and
// the current ones from the catalog's function on the lookup. So each cell sets
// exactly those two and asserts what reaches the provider — the local reference,
// forwarded unchanged — or the halt.
func TestALocalSourceIsComparedWithItsPinsBeforeItIsForwarded(t *testing.T) {
	t.Parallel()
	const document = "/ws/catalog/recipe/helm/app/1.0.0/recipe.yaml"
	pinned := []types.LocalSourceDigest{{Path: "chart/Chart.yaml", Digest: "sha256:aa"}, {Path: "chart/templates/cm.yaml", Digest: "sha256:bb"}}
	drifted := []types.LocalSourceDigest{{Path: "chart/Chart.yaml", Digest: "sha256:aa"}, {Path: "chart/templates/cm.yaml", Digest: "sha256:cc"}}

	cases := []struct {
		name string
		// recordedUnder files the pins under a document, or records none when empty.
		recordedUnder string
		// current is what the catalog's function answers; nil installs no function.
		current []types.LocalSourceDigest
		noFunc  bool
		// wantErr is the condition the halt carries; nil means the reference forwards.
		wantErr     error
		wantUnnamed bool
	}{
		{name: "an entity nobody pinned is forwarded without reading its files", current: drifted},
		{name: "pins recorded for another document are not this entity's", recordedUnder: "/ws/catalog/recipe/helm/other/1.0.0/recipe.yaml", current: drifted},
		{name: "files that match their pins are forwarded", recordedUnder: document, current: pinned},
		{name: "a file that moved halts before the provider reads it", recordedUnder: document, current: drifted,
			wantErr: recipepin.ErrLocalSourceContentMismatch()},
		{name: "recorded pins with no way to read the source halt rather than admit", recordedUnder: document, noFunc: true, wantUnnamed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			asked := false
			input := RendererInput{}
			if tc.recordedUnder != "" {
				input.RecordedPins = recipepin.Lookup{tc.recordedUnder: recipepin.Pins{LocalSourceDigests: pinned}}
			}
			if !tc.noFunc {
				current := tc.current
				input.CatalogLookup = resolver.CatalogLookup{LocalSourcePins: func(documentPath string) ([]types.LocalSourceDigest, error) {
					asked = true
					if documentPath != document {
						t.Errorf("the catalog was asked about %q; want the entity's own document %q", documentPath, document)
					}
					return current, nil
				}}
			}
			got, err := resolveEntitySource(input, "/ws/catalog/recipe/helm/app/1.0.0/chart", types.OrderedMap[string]{}, "app:1.0.0", map[string]any{}, map[string]any{}, document, sourceOrigin{})

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("resolveEntitySource = %v; want %v", err, tc.wantErr)
				}
			case tc.wantUnnamed:
				if err == nil {
					t.Fatal("recorded pins with no function to read the source against were admitted")
				}
				if errors.Is(err, recipepin.ErrLocalSourceContentMismatch()) {
					t.Errorf("a missing function is a fault in the compiler, not a drifted source; got %v", err)
				}
			default:
				if err != nil {
					t.Fatalf("resolveEntitySource: %v", err)
				}
				if got != "/ws/catalog/recipe/helm/app/1.0.0/chart" {
					t.Errorf("the local reference forwarded as %v; want it unchanged", got)
				}
			}
			// The source's files are read exactly when this document has pins to
			// compare them with: never for an entity nobody pinned.
			if wantAsked := tc.recordedUnder == document && !tc.noFunc; asked != wantAsked {
				t.Errorf("the catalog's function was asked = %v; want %v", asked, wantAsked)
			}
		})
	}
}
