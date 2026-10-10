// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// hintRecordingLookup is fakeLookup that records the hint each catalog-entity
// resolution was asked with, and every URI asked of the Aggregate tier, so a test
// can tell which tier a reference went to.
type hintRecordingLookup struct {
	*fakeLookup
	catalogHints map[string]string
	aggregateAsk map[string]bool
}

func (r *hintRecordingLookup) ResolveCatalogEntity(ctx context.Context, uri, kindHint string) (closure.Resolution, error) {
	r.catalogHints[uri] = kindHint
	return r.fakeLookup.ResolveCatalogEntity(ctx, uri, kindHint)
}

func (r *hintRecordingLookup) ResolveAggregate(ctx context.Context, uri string) (closure.Resolution, error) {
	r.aggregateAsk[uri] = true
	return r.fakeLookup.ResolveAggregate(ctx, uri)
}

func releaseNaming(aggregate string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: prod\n  environment: prod\n  version: 1.0.0\nspec:\n  aggregate: " +
		aggregate + "\n"
}

func entityRow(t *testing.T, cl closure.Closure, uri string) closure.ResolvedRef {
	t.Helper()
	for _, e := range cl.Entities {
		if e.URI == uri {
			return e
		}
	}
	t.Fatalf("the closure holds no row for %q", uri)
	return closure.ResolvedRef{}
}

// TestTheReleasesOwnAggregateCarriesItsOwnKindHint holds the walk to recording the
// Aggregate a Release deploys under the hint that names it as one, whichever form
// the Release names it in, while resolving it exactly as a reference carrying no
// hint resolves: through the catalog-entity tier, never the sub-aggregate tier.
func TestTheReleasesOwnAggregateCarriesItsOwnKindHint(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"shop", "shop:1.0.0", "./aggregate.yaml"} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			top := subAggregate(t, "top", true)
			lookup := &hintRecordingLookup{
				fakeLookup: &fakeLookup{entities: map[string]closure.Resolution{
					ref:    resolutionOK("1.0.0", "sha256:top", "tier-3-pull"),
					uriSvc: resolutionOK("1.0.0", "sha256:svc", "tier-3-pull"),
				}},
				catalogHints: map[string]string{},
				aggregateAsk: map[string]bool{},
			}
			cl, err := closure.ComputeClosure(context.Background(), loadRelease(t, releaseNaming(ref)), nil,
				types.Normalize(top), workspaceconfig.Config{}, lookup)
			if err != nil {
				t.Fatalf("ComputeClosure: %v", err)
			}
			if got := entityRow(t, cl, ref).KindHint; got != closure.ReleaseAggregateKindHint {
				t.Errorf("the Release's own Aggregate is recorded with hint %q; want %q", got, closure.ReleaseAggregateKindHint)
			}
			if hint, asked := lookup.catalogHints[ref]; !asked || hint != closure.ReleaseAggregateKindHint {
				t.Errorf("the Release's own Aggregate must resolve through the catalog-entity tier with its hint; asked=%t hint=%q", asked, hint)
			}
			if lookup.aggregateAsk[ref] {
				t.Errorf("the Release's own Aggregate was resolved through the sub-aggregate tier")
			}
		})
	}
}

// TestAnyOtherKindForTheSameReferenceReplacesTheReleasesHint pins the precedence: the
// Release's own hint is the weakest, so a URI another site names under a kind of its
// own is recorded with that kind exactly as it would be without the Release's hint —
// an authoritative sub-aggregate hint and a weak Component hint alike.
func TestAnyOtherKindForTheSameReferenceReplacesTheReleasesHint(t *testing.T) {
	t.Parallel()
	const ref = "hub.aggrega.ai/aggregate/example/shared:1.0.0"
	rows := []struct {
		name string
		top  string
		want string
	}{
		{
			name: "a sub-aggregate reference to the same identity",
			top:  "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: top\n  version: 1.0.0\nspec:\n  aggregates:\n    again:\n      type: " + ref + "\n",
			want: "aggregate",
		},
		{
			name: "a component instance of the same identity",
			top:  "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: top\n  version: 1.0.0\nspec:\n  components:\n    app:\n      type: " + ref + "\n",
			want: "component",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			lookup := &fakeLookup{entities: map[string]closure.Resolution{ref: resolutionOK("1.0.0", "sha256:shared", "tier-3-pull")}}
			cl, err := closure.ComputeClosure(context.Background(), loadRelease(t, releaseNaming(ref)), nil,
				types.Normalize(loadAggregate(t, row.top)), workspaceconfig.Config{}, lookup)
			if err != nil {
				t.Fatalf("ComputeClosure: %v", err)
			}
			if got := entityRow(t, cl, ref).KindHint; got != row.want {
				t.Errorf("recorded with hint %q; want %q", got, row.want)
			}
		})
	}
}

// TestTheDescentNeverReloadsTheReleasesOwnAggregate holds the hint distinct from the
// sub-aggregate one: the descent loads and re-walks the sub-aggregates the Release's
// own Aggregate imports, and never that Aggregate itself, which the loader here
// cannot serve — so a hint the descent selected would fail the run.
func TestTheDescentNeverReloadsTheReleasesOwnAggregate(t *testing.T) {
	t.Parallel()
	top := subAggregate(t, "top", false, uriA)
	loader := &fakeAggLoader{bodies: map[string]*types.Aggregate{uriA: subAggregate(t, "a", true)}}

	cl, err := runDescent(t, top, descentLookup(), loader)
	if err != nil {
		t.Fatalf("descent: %v", err)
	}
	if _, loaded := loader.seen[uriTop]; loaded {
		t.Errorf("the descent loaded the Release's own Aggregate as a sub-aggregate")
	}
	if _, loaded := loader.seen[uriA]; !loaded {
		t.Errorf("the descent did not load the sub-aggregate the Release's own Aggregate imports")
	}
	if got := entityRow(t, cl, uriTop).KindHint; got != closure.ReleaseAggregateKindHint {
		t.Errorf("the Release's own Aggregate is recorded with hint %q; want %q", got, closure.ReleaseAggregateKindHint)
	}
	if got := entityRow(t, cl, uriA).KindHint; got != "aggregate" {
		t.Errorf("the sub-aggregate is recorded with hint %q; want the sub-aggregate hint", got)
	}
}
