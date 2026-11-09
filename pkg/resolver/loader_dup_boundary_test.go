// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"
)

const dupBoundaryRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    outer:
      directory: ./outer
      components:
        api:
          infra:
            replicas: 1
      boundaries:
        inner:
          directory: ./inner
          components:
            api:
              uses:
                databases:
                  main:
                    infra:
                      pool_size: 9
`

// TestLoadRelease_DuplicateBoundaryComponentHalts pins the load-time rejection:
// one component declared in two nested boundaries halts with the sentinel, and
// the diagnostic names BOTH declaration sites plus the component.
func TestLoadRelease_DuplicateBoundaryComponentHalts(t *testing.T) {
	t.Parallel()
	_, err := LoadReleaseFromBytes([]byte(dupBoundaryRelease), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent; got %v", err)
	}
	for _, want := range []string{`"api"`, `"outer"`, `"outer/inner"`, "release.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
	// The locator points at the SECOND declaration — the one the author can
	// delete — while the message carries both.
	//
	// It spells the boundary nesting level by level, under the container's own key
	// each time, because that is how the document spells a nested boundary. A
	// locator joining the boundary path in as one segment would name a key no file
	// contains.
	var re *ResolverError
	if !errors.As(err, &re) {
		t.Fatalf("want a *ResolverError; got %T", err)
	}
	if re.Field != ".spec.boundaries.outer.boundaries.inner.components.api" {
		t.Errorf("locator must name the second declaration site; got %q", re.Field)
	}
}

// TestLoadRelease_DuplicateBoundaryComponentSiblingBoundariesHalt pins that the
// rule is about the boundary TREE, not just an ancestor chain: two sibling
// boundaries declaring one name diverge the same way and are rejected too.
func TestLoadRelease_DuplicateBoundaryComponentSiblingBoundariesHalt(t *testing.T) {
	t.Parallel()
	siblings := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    left:
      components:
        api: {}
    right:
      components:
        api: {}
`
	_, err := LoadReleaseFromBytes([]byte(siblings), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent for sibling boundaries; got %v", err)
	}
	for _, want := range []string{`"left"`, `"right"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
}

// TestLoadRelease_DistinctComponentsPerBoundaryAccepted is the counter-case: the
// rule rejects a duplicated NAME, not nesting itself. Distinct names across
// nested boundaries, and one name declared once, both load clean.
func TestLoadRelease_DistinctComponentsPerBoundaryAccepted(t *testing.T) {
	t.Parallel()
	ok := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    outer:
      components:
        api: {}
      boundaries:
        inner:
          components:
            worker: {}
`
	rel, err := LoadReleaseFromBytes([]byte(ok), "release.yaml")
	if err != nil {
		t.Fatalf("distinct names per boundary must load clean; got %v", err)
	}
	if rel == nil {
		t.Fatal("want a loaded Release")
	}
}

// TestLoadRelease_SynthesizedDefaultIsNotADuplicate pins that the folded
// top-level components surface does not trip the rule: normalization puts them
// in one synthesized boundary, so each name is declared exactly once.
func TestLoadRelease_SynthesizedDefaultIsNotADuplicate(t *testing.T) {
	t.Parallel()
	rootOnly := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  components:
    api: {}
    worker: {}
`
	rel, err := LoadReleaseFromBytes([]byte(rootOnly), "release.yaml")
	if err != nil {
		t.Fatalf("a root-only Release must load clean; got %v", err)
	}
	def, found := rel.Spec.Boundaries.Get("default")
	if !found || !def.Synthesized {
		t.Fatalf("want the synthesized default boundary; found=%v synthesized=%v", found, def.Synthesized)
	}
}

// A routed sub-module places every component it contains, so these three shapes
// each place one node twice. They are separate fixtures because the diagnostic has
// to phrase the fault in the terms of whichever axes produced it — two routings,
// or one routing against one named component.
const dupRoutedModuleRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
    west:
      directory: ./west
      aggregates:
        data: {}
`

const dupRoutedAncestorRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
    west:
      directory: ./west
      aggregates:
        data:
          aggregates:
            inner: {}
`

const dupRoutedAndNamedRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
    west:
      directory: ./west
      components:
        data.store: {}
`

// TestLoadRelease_RoutedModulePlacedTwiceHalts — the same sub-module routed into
// two boundaries. Every component it contains would take its output directory from
// whichever placement was written last.
func TestLoadRelease_RoutedModulePlacedTwiceHalts(t *testing.T) {
	t.Parallel()
	_, err := LoadReleaseFromBytes([]byte(dupRoutedModuleRelease), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent; got %v", err)
	}
	for _, want := range []string{"sub-module", `"data"`, `"east"`, `"west"`, "release.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
	var re *ResolverError
	if !errors.As(err, &re) {
		t.Fatalf("want a *ResolverError; got %T", err)
	}
	if re.Field != ".spec.boundaries.west.aggregates.data" {
		t.Errorf("the locator must point at the second routing entry; got %q", re.Field)
	}
}

// TestLoadRelease_RoutedModuleNestingElsewhereHalts — reaching a nested module in a
// second boundary halts, and the diagnostic names the OUTER module.
//
// Reaching `inner` requires writing an `aggregates: data:` entry to nest it under,
// and that entry routes `data` itself, so the two boundaries conflict at `data`
// before `data.inner` is ever compared. Naming the outer module is the better
// message anyway: it is the entry the author has to change.
func TestLoadRelease_RoutedModuleNestingElsewhereHalts(t *testing.T) {
	t.Parallel()
	_, err := LoadReleaseFromBytes([]byte(dupRoutedAncestorRelease), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent; got %v", err)
	}
	for _, want := range []string{"sub-module", `"data"`, `"east"`, `"west"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
}

// TestLoadRelease_RoutedModuleAndNamedComponentHalts — the mixed case: the module
// is routed into one boundary while one of its components is named in another.
func TestLoadRelease_RoutedModuleAndNamedComponentHalts(t *testing.T) {
	t.Parallel()
	_, err := LoadReleaseFromBytes([]byte(dupRoutedAndNamedRelease), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent; got %v", err)
	}
	for _, want := range []string{`"data.store"`, `"data"`, `"east"`, `"west"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
}

// TestLoadRelease_RoutedModuleAndNamedComponentInOneBoundaryBuilds is the
// counter-case the three halts are read against: routing a module and configuring
// one of its components under the SAME boundary agrees about where the component
// belongs, so it must load. Without it, a guard that rejected every co-occurrence
// would look like three successes.
func TestLoadRelease_RoutedModuleAndNamedComponentInOneBoundaryBuilds(t *testing.T) {
	t.Parallel()
	const release = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
      components:
        data.store: {}
`
	if _, err := LoadReleaseFromBytes([]byte(release), "release.yaml"); err != nil {
		t.Fatalf("routing a module and configuring one of its components under one boundary must load; got %v", err)
	}
}

// TestLoadRelease_RoutedComponentNamedTwiceInOneBoundaryHalts — one component
// named directly AND named again through a routing entry under the same boundary.
// Both entries carry a payload for one node and nothing decides between them, so
// this is a fault even though the two agree on the boundary.
func TestLoadRelease_RoutedComponentNamedTwiceInOneBoundaryHalts(t *testing.T) {
	t.Parallel()
	const release = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    east:
      directory: ./east
      components:
        data.store:
          infra:
            replicas: 1
      aggregates:
        data:
          components:
            store:
              infra:
                replicas: 5
`
	_, err := LoadReleaseFromBytes([]byte(release), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent for one component carrying two payloads; got %v", err)
	}
}
