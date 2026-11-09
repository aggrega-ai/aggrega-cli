// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestAnUnsatisfiedPinIsReportedOnlyForTheKindTheReferenceWasCollectedAs drives the
// closure lookup of both backings over a catalog holding the Component core/base at
// 1.0.0 only, with core/base:9.9.9 asked as a Component and as a Recipe. Asked as a
// Component, the miss carries the unsatisfiable-version condition: the catalog holds
// that Component at other versions only. Asked as a Recipe it carries none, because
// the catalog holds no Recipe of that name at any version; the Component sharing the
// name is another entity, and a reason about it would send the author to the wrong
// document.
func TestAnUnsatisfiedPinIsReportedOnlyForTheKindTheReferenceWasCollectedAs(t *testing.T) {
	t.Parallel()
	ws, cd := unsatisfiedPinWorkspace(t)
	const cache = ".aggrega/cache"
	eager, err := LoadAll(ws, cd)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	backings := map[string]closure.Lookup{
		"eager": eager.ClosureLookupWithReuse(nil, ws, cache, nil, nil, nil),
		"lazy":  NewLazyLookup(ws, cache, []string{cd}, nil).ClosureLookup(nil, ws, cache, nil, nil, nil),
	}
	for _, backing := range []string{"eager", "lazy"} {
		for _, tc := range []struct {
			hint        string
			wantRefusal bool
		}{
			{hint: closure.KindComponent, wantRefusal: true},
			{hint: closure.KindRecipe, wantRefusal: false},
		} {
			t.Run(backing+"/asked as "+tc.hint, func(t *testing.T) {
				res, err := backings[backing].ResolveCatalogEntity(t.Context(), "core/base:9.9.9", tc.hint)
				if err != nil {
					t.Fatalf("ResolveCatalogEntity: %v", err)
				}
				if !res.NotFound {
					t.Fatalf("core/base:9.9.9 resolved; the catalog holds no entity at that version")
				}
				if got := errors.Is(res.UnsatisfiedPin, ErrUnsatisfiableEntityVersion()); got != tc.wantRefusal {
					t.Errorf("unsatisfied-pin condition carried = %v, want %v (%v)", got, tc.wantRefusal, res.UnsatisfiedPin)
				}
			})
		}
	}
}

// TestTwoRefusedSlotsOfOneReferenceAreRecordedApart pins a Recipe and a
// FeatureRecipe under one reference, each to a cache slot whose archive yields no
// entity, and reads the view. Each refusal is recorded under its own label — the
// reference and the kind it is pinned as — so the reader is told of two refused
// artifacts rather than of one, and neither refusal overwrites the other.
func TestTwoRefusedSlotsOfOneReferenceAreRecordedApart(t *testing.T) {
	ws := t.TempDir()
	blob := bundleWith(t, map[string]string{
		"frontend.yaml": pinnedRecipeYAML("frontend"),
		"database.yaml": pinnedRecipeYAML("database"),
	})
	recipePin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "helm/shared:1.0.0", oci.MediaTypeRecipe, blob)
	featurePin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "helm/shared:1.0.0", oci.MediaTypeFeatureRecipe, blob)
	recipeKey := PinKey{Kind: "Recipe", URI: "helm/shared"}
	featureKey := PinKey{Kind: "FeatureRecipe", URI: "helm/shared"}

	subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
	subject.SetPins(Pins{recipeKey: recipePin, featureKey: featurePin})
	var skips map[string]error
	captureNotices(t, subject, func() { skips = subject.Skips() })

	for _, key := range []PinKey{recipeKey, featureKey} {
		if _, recorded := skips[pinnedSlotLabel(key)]; !recorded {
			t.Errorf("the refused %s slot is not recorded under its own label %q; keys present: %v",
				key.Kind, pinnedSlotLabel(key), skipKeysOf(skips))
		}
	}
	if pinnedSlotLabel(recipeKey) == pinnedSlotLabel(featureKey) {
		t.Errorf("the two slots share one label, %q", pinnedSlotLabel(recipeKey))
	}
}

// TestADriftedSlotIsPassedOverOnlyForAnAuthoredCopyOfItsOwnKind drives the pinned
// integrity gate over an Aggregate pin whose cache slot holds other bytes, in a
// catalog directory that holds a FeatureRecipe and an Aggregate of the same
// reference. The authored Aggregate holds the pinned bytes, so the slot is passed
// over with a notice naming that document and the gate does not halt.
//
// The FeatureRecipe sharing the reference is another entity, and it sits on an
// earlier rung of the catalog tier than the Aggregate: a gate reading copies of any
// kind would meet the FeatureRecipe first, offer it as the copy, find its digest is
// not the pin, and halt. Two positive controls hold the fixture to that shape: the
// reference resolves at the FeatureRecipe's rung and at the Aggregate's.
func TestADriftedSlotIsPassedOverOnlyForAnAuthoredCopyOfItsOwnKind(t *testing.T) {
	t.Parallel()
	const (
		cacheDir = ".aggrega/cache"
		slot     = "helm/shared:1.0.0"
	)
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	featurePath := filepath.Join(cat, "featurerecipe", "helm", "shared", "1.0.0", "recipe.yaml")
	aggregatePath := filepath.Join(cat, "aggregate", "helm", "shared", "aggregate.yaml")
	for path, body := range map[string]string{
		featurePath: "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: shared\n  version: 1.0.0\n" +
			"spec:\n  capability: helm\n  implements: core/shared-feature:1.0.0\n",
		aggregatePath: "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: helm/shared\n  version: \"1.0.0\"\n" +
			"spec:\n  components:\n    app:\n      type: core/web\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The slot holds an Aggregate body other than the authored one.
	drifted := writePinnedSlot(t, ws, cacheDir, slot, oci.MediaTypeAggregate, []byte(
		"apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: helm/shared\n  version: \"1.0.0\"\n"+
			"spec:\n  components:\n    app:\n      type: core/api\n"))

	view := NewLazyLookup(ws, cacheDir, []string{cat}, nil)
	view.mu.Lock()
	featureCopies := view.catalogDirectoryCopies("FeatureRecipe", slot)
	aggregateCopies := view.catalogDirectoryCopies("Aggregate", slot)
	view.mu.Unlock()
	if len(featureCopies) != 1 || featureCopies[0].path != featurePath {
		t.Fatalf("fixture is inert: the reference does not resolve at the FeatureRecipe's rung (copies %+v), so a "+
			"kind-blind gate would not meet it first", featureCopies)
	}
	if len(aggregateCopies) != 1 || aggregateCopies[0].path != aggregatePath {
		t.Fatalf("catalog-directory copies of the Aggregate = %+v; want exactly the authored Aggregate %s", aggregateCopies, aggregatePath)
	}
	if aggregateCopies[0].digest == drifted.Digest || featureCopies[0].digest == aggregateCopies[0].digest {
		t.Fatalf("fixture is inert: the slot, the Aggregate and the FeatureRecipe must each hold different bytes")
	}

	var notices bytes.Buffer
	view.WriteNoticesTo(&notices)
	view.SetPins(Pins{{Kind: "Aggregate", URI: slot}: {
		ResolvedVersion: drifted.ResolvedVersion, Digest: aggregateCopies[0].digest, ManifestDigest: drifted.ManifestDigest,
	}})
	if err := view.VerifyPinnedIntegrity(); err != nil {
		t.Fatalf("the authored Aggregate holds the pinned bytes, so the drifted slot must be passed over, not halted on: %v", err)
	}
	if !strings.Contains(notices.String(), "passed over for "+aggregatePath) {
		t.Errorf("the gate did not name the authored Aggregate it passed the slot over for; notices:\n%s", notices.String())
	}
}
