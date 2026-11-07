// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestEveryDeclaredKindNamesTheKindItsDocumentIsWrittenAs holds the one translation
// of an entity kind into the kind an author writes to the declared set: every
// declared kind has a document kind, no two share one, and a word naming no
// declared kind has none.
func TestEveryDeclaredKindNamesTheKindItsDocumentIsWrittenAs(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	for _, kind := range closure.DeclaredEntityKinds() {
		doc := closure.DocumentKind(kind)
		if doc == "" {
			t.Errorf("declared kind %q names no document kind", kind)
			continue
		}
		if other, dup := seen[doc]; dup {
			t.Errorf("declared kinds %q and %q both name the document kind %q", other, kind, doc)
		}
		seen[doc] = kind
	}
	if got := closure.DocumentKind("release_aggregate_typo"); got != "" {
		t.Errorf("an undeclared word names the document kind %q; want none", got)
	}
}

// TestACacheSlotHoldsEveryDeclaredKindARegistryPublishes holds the one statement of
// which kinds a content-cache slot can hold to the declared set, kind by kind: every
// declared kind but the Manifest, which no registry publishes. A row recording no
// kind — a row of a section holding one kind — is not refused by it. The expected
// answer is spelled per kind, so a declared kind with no row here fails, as does a
// kind the predicate answers differently.
func TestACacheSlotHoldsEveryDeclaredKindARegistryPublishes(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		closure.KindAggregate:     true,
		closure.KindComponent:     true,
		closure.KindFeature:       true,
		closure.KindFeatureRecipe: true,
		closure.KindManifest:      false,
		closure.KindPlugin:        true,
		closure.KindRecipe:        true,
	}
	for _, kind := range closure.DeclaredEntityKinds() {
		w, listed := want[kind]
		if !listed {
			t.Errorf("declared kind %q has no expected answer here", kind)
			continue
		}
		if got := closure.KindReadFromCacheSlot(kind); got != w {
			t.Errorf("KindReadFromCacheSlot(%q) = %v; want %v", kind, got, w)
		}
	}
	if !closure.KindReadFromCacheSlot("") {
		t.Errorf("a row recording no kind is refused a cache slot; want it admitted")
	}
}
