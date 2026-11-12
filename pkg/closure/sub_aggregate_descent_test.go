// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Aggregate URIs the descent fixtures share. The top-level aggregate is passed
// to ComputeClosure as the in-memory value AND named by the Release; A..D are
// the transitive sub-aggregates the injected loader serves.
const (
	uriTop = "hub.aggrega.ai/aggregate/example/top:1.0.0"
	uriA   = "hub.aggrega.ai/aggregate/example/a:1.0.0"
	uriB   = "hub.aggrega.ai/aggregate/example/b:1.0.0"
	uriC   = "hub.aggrega.ai/aggregate/example/c:1.0.0"
	uriD   = "hub.aggrega.ai/aggregate/example/d:1.0.0"
	uriSvc = "hub.aggrega.ai/component/core/svc:1.0.0"
)

const descentRelease = `
apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: prod
  version: 1.0.0
spec:
  aggregate: hub.aggrega.ai/aggregate/example/top:1.0.0
`

// fakeAggLoader is the in-memory AggregateLoader the descent tests inject. It
// serves parsed sub-aggregate bodies by URI and records the (uri, digest) pairs
// it was asked for, so a test can assert the descent threads each entity's pin
// digest to the loader (the contract a cache-backed loader verifies bytes
// against).
type fakeAggLoader struct {
	bodies map[string]*types.Aggregate
	seen   map[string]string
}

func (f *fakeAggLoader) LoadAggregate(uri, version, digest string) (*types.Aggregate, error) {
	if f.seen == nil {
		f.seen = map[string]string{}
	}
	f.seen[uri] = digest
	body, ok := f.bodies[uri]
	if !ok {
		return nil, fmt.Errorf("fake loader: no aggregate %q", uri)
	}
	return body, nil
}

// subAggregate builds an Aggregate fixture naming the given sub-aggregate URIs
// (and optionally one component) under author-ordered spec blocks.
func subAggregate(t *testing.T, name string, withComponent bool, subURIs ...string) *types.Aggregate {
	t.Helper()
	src := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: " + name + "\n  version: 1.0.0\nspec:\n"
	if withComponent {
		src += "  components:\n    svc:\n      type: " + uriSvc + "\n"
	}
	if len(subURIs) > 0 {
		src += "  aggregates:\n"
		for i, u := range subURIs {
			src += fmt.Sprintf("    sub%d:\n      type: %s\n", i, u)
		}
	}
	return loadAggregate(t, src)
}

// descentLookup resolves every aggregate/component URI the fixtures use, each to
// a deterministic per-URI digest, so a pin is stable across runs.
func descentLookup() *fakeLookup {
	ents := map[string]closure.Resolution{}
	for _, u := range []string{uriTop, uriA, uriB, uriC, uriD, uriSvc} {
		ents[u] = resolutionOK("1.0.0", "sha256:"+fakeBaseName(u), "tier-3-pull")
	}
	return &fakeLookup{entities: ents}
}

// runDescent computes the base closure for `top` then runs the transitive
// descent with the supplied loader, returning the augmented closure (or the
// descent error).
func runDescent(t *testing.T, top *types.Aggregate, lookup *fakeLookup, loader closure.AggregateLoader) (closure.Closure, error) {
	t.Helper()
	rel := loadRelease(t, descentRelease)
	cl, err := closure.ComputeClosure(context.Background(), rel, nil, types.Normalize(top), workspaceconfig.Config{}, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	derr := closure.DescendSubAggregates(context.Background(), &cl, loader, lookup)
	return cl, derr
}

func entityURIs(cl closure.Closure) []string {
	out := make([]string, 0, len(cl.Entities))
	for _, e := range cl.Entities {
		out = append(out, e.URI)
	}
	sort.Strings(out)
	return out
}

// TestDescendChain pins a transitive chain top→A→B→C: every level appears in
// the closure, and B's component is pinned too (descent re-walks the full
// aggregate, not only its aggregates: axis).
func TestDescendChain(t *testing.T) {
	top := subAggregate(t, "top", false, uriA)
	loader := &fakeAggLoader{bodies: map[string]*types.Aggregate{
		uriA: subAggregate(t, "a", false, uriB),
		uriB: subAggregate(t, "b", true, uriC),
		uriC: subAggregate(t, "c", false),
	}}
	lookup := descentLookup()

	cl, err := runDescent(t, top, lookup, loader)
	if err != nil {
		t.Fatalf("descent: %v", err)
	}

	got := entityURIs(cl)
	want := []string{uriA, uriB, uriC, uriTop, uriSvc}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chain entities:\n got=%v\nwant=%v", got, want)
	}

	// The descent threads each aggregate's pin digest to the loader so a
	// cache-backed loader can verify bytes against it.
	for _, u := range []string{uriA, uriB, uriC} {
		if loader.seen[u] != "sha256:"+fakeBaseName(u) {
			t.Errorf("loader for %q saw digest %q; want the pin digest", u, loader.seen[u])
		}
	}
}

// TestDescendDiamond pins a diamond top→A→{B,C}, B→D, C→D: D resolves through
// two paths but is pinned exactly once.
func TestDescendDiamond(t *testing.T) {
	top := subAggregate(t, "top", false, uriA)
	loader := &fakeAggLoader{bodies: map[string]*types.Aggregate{
		uriA: subAggregate(t, "a", false, uriB, uriC),
		uriB: subAggregate(t, "b", false, uriD),
		uriC: subAggregate(t, "c", false, uriD),
		uriD: subAggregate(t, "d", false),
	}}
	lookup := descentLookup()

	cl, err := runDescent(t, top, lookup, loader)
	if err != nil {
		t.Fatalf("descent: %v", err)
	}

	got := entityURIs(cl)
	want := []string{uriA, uriB, uriC, uriD, uriTop}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diamond entities:\n got=%v\nwant=%v", got, want)
	}
	// D appears once in the entity slice (dedup), not twice.
	count := 0
	for _, e := range cl.Entities {
		if e.URI == uriD {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("D pinned %d times; want exactly 1", count)
	}
}

// TestDescendCycle terminates a cycle top→A→B→A cleanly via the visited set —
// no error, and certainly not a spurious depth-exceeded error.
func TestDescendCycle(t *testing.T) {
	top := subAggregate(t, "top", false, uriA)
	loader := &fakeAggLoader{bodies: map[string]*types.Aggregate{
		uriA: subAggregate(t, "a", false, uriB),
		uriB: subAggregate(t, "b", false, uriA),
	}}
	lookup := descentLookup()

	cl, err := runDescent(t, top, lookup, loader)
	if err != nil {
		t.Fatalf("cycle must halt cleanly via visited-set, got error: %v", err)
	}
	if errors.Is(err, closure.ErrSubAggregateDepth()) {
		t.Fatalf("cycle halted via depth bound, not the visited set")
	}

	got := entityURIs(cl)
	want := []string{uriA, uriB, uriTop}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cycle entities:\n got=%v\nwant=%v", got, want)
	}
}

// TestDescendDepthBound trips ErrSubAggregateDepth on a chain longer than the
// bound that evades the visited set (each level a distinct identity).
func TestDescendDepthBound(t *testing.T) {
	top := subAggregate(t, "top", false, "hub.aggrega.ai/aggregate/example/n0:1.0.0")
	bodies := map[string]*types.Aggregate{}
	ents := map[string]closure.Resolution{
		uriTop: resolutionOK("1.0.0", "sha256:top", "tier-3-pull"),
	}
	// 0..11 — twelve distinct levels, each referencing the next, exceeds the
	// depth bound of 10.
	for i := 0; i <= 11; i++ {
		u := fmt.Sprintf("hub.aggrega.ai/aggregate/example/n%d:1.0.0", i)
		ents[u] = resolutionOK("1.0.0", fmt.Sprintf("sha256:n%d", i), "tier-3-pull")
		var subs []string
		if i < 11 {
			subs = []string{fmt.Sprintf("hub.aggrega.ai/aggregate/example/n%d:1.0.0", i+1)}
		}
		bodies[u] = subAggregate(t, fmt.Sprintf("n%d", i), false, subs...)
	}
	lookup := &fakeLookup{entities: ents}
	loader := &fakeAggLoader{bodies: bodies}

	_, err := runDescent(t, top, lookup, loader)
	if !errors.Is(err, closure.ErrSubAggregateDepth()) {
		t.Fatalf("deep chain must trip ErrSubAggregateDepth, got: %v", err)
	}
}

// TestDescendDeterministic descends the same diamond twice and asserts the
// pinned entity set is byte-identical — the lock==build guard for the new
// recursion. Independent walk/resolve order must not perturb the canonical
// closure the lockfile is written from.
func TestDescendDeterministic(t *testing.T) {
	build := func() closure.Closure {
		top := subAggregate(t, "top", false, uriA)
		loader := &fakeAggLoader{bodies: map[string]*types.Aggregate{
			uriA: subAggregate(t, "a", false, uriB, uriC),
			uriB: subAggregate(t, "b", true, uriD),
			uriC: subAggregate(t, "c", false, uriD),
			uriD: subAggregate(t, "d", false),
		}}
		cl, err := runDescent(t, top, descentLookup(), loader)
		if err != nil {
			t.Fatalf("descent: %v", err)
		}
		return cl
	}

	first := build()
	second := build()
	if !reflect.DeepEqual(first.Entities, second.Entities) {
		t.Fatalf("descent is non-deterministic:\n first=%#v\nsecond=%#v", first.Entities, second.Entities)
	}
}

// TestDescendNilLoaderNoop confirms a nil loader leaves the closure untouched
// (the caller wired no registry tier).
func TestDescendNilLoaderNoop(t *testing.T) {
	top := subAggregate(t, "top", false, uriA)
	cl, err := runDescent(t, top, descentLookup(), nil)
	if err != nil {
		t.Fatalf("nil-loader descent: %v", err)
	}
	got := entityURIs(cl)
	want := []string{uriA, uriTop}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nil-loader must not descend:\n got=%v\nwant=%v", got, want)
	}
}
