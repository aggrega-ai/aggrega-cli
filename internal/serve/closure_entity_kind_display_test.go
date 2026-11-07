// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestAnAggregateAndAManifestOfOneNameAreListedAsTwoKinds projects a closure
// reaching the Release's own Aggregate and its Manifest, which share one name, onto
// the listing a client reads. Each row names its entity's kind as an author writes
// it, so the two read as two entities; a projection that named no kind for either
// would list two identical rows.
func TestAnAggregateAndAManifestOfOneNameAreListedAsTwoKinds(t *testing.T) {
	t.Parallel()
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "imported/flow", ResolvedVersion: "1.0.0", KindHint: closure.ReleaseAggregateKindHint},
		{URI: "imported/flow", ResolvedVersion: "1.0.0", KindHint: closure.KindManifest},
		{URI: "core/web", ResolvedVersion: "1.0.0", KindHint: closure.KindComponent},
	}}
	got := closureEntities(cl, stateRows)
	want := []ClosureEntity{
		{URI: "imported/flow", Version: "1.0.0", Kind: "Aggregate"},
		{URI: "imported/flow", Version: "1.0.0", Kind: "Manifest"},
		{URI: "core/web", Version: "1.0.0", Kind: "Component"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("closure listing = %+v; want %+v", got, want)
	}
}
