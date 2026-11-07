// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestAReferenceIsReadableAsTheKindItWasCollectedAsOrAsAnyWhenItHasNone holds the
// rule a pass reading an entity by kind applies, cell by cell: a reference collected
// as a kind is readable as that kind and as no other, and one collected with no kind
// is readable as any. Each cell is checked through both statements of the rule — the
// kind form and a closure entity collected under a hint naming that kind — so the
// two cannot come to disagree.
func TestAReferenceIsReadableAsTheKindItWasCollectedAsOrAsAnyWhenItHasNone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		hint, kind string
		want       bool
	}{
		{closure.KindComponent, closure.KindComponent, true},
		{closure.KindRecipe, closure.KindRecipe, true},
		{closure.KindRecipe, closure.KindComponent, false},
		{closure.KindComponent, closure.KindFeature, false},
		{closure.KindManifest, closure.KindAggregate, false},
		{closure.KindAggregate, closure.KindManifest, false},
		{closure.ReleaseAggregateKindHint, closure.KindAggregate, true},
		{"", closure.KindComponent, true},
		{"", closure.KindAggregate, true},
		{"", closure.KindPlugin, true},
	} {
		collected := closure.EntityKindOf(tc.hint)
		if got := closure.KindReadableAs(collected, tc.kind); got != tc.want {
			t.Errorf("KindReadableAs(%q, %q) = %v; want %v", collected, tc.kind, got, tc.want)
		}
		ref := closure.ResolvedRef{URI: "core/shared", KindHint: tc.hint}
		if got := ref.ReadableAs(tc.kind); got != tc.want {
			t.Errorf("an entity collected under hint %q: ReadableAs(%q) = %v; want %v", tc.hint, tc.kind, got, tc.want)
		}
	}
}
