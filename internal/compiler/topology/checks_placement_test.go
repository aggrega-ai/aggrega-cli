// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestSubModulePlacementIsReadAcrossBothAxes proves the placement rule fires on
// placement rather than on a spelling.
//
// The two halves are what make that claim testable at all. A sub-module reaching
// no boundary on either axis halts; the same sub-module reaching one — by a
// routing entry, or by its components being named individually under a
// boundary's own map with their qualified dotted ids — passes. A rule that fired
// whenever no routing entry named the module would pass the first case and fail
// the third, which is exactly the reading this rule is not.
//
// The remaining rows are the two shapes that are absences rather than faults, and
// they are here because each is a case a stricter rule would wrongly halt: a
// module contributing no component has nothing that could land outside a
// boundary, and a module a false activation toggle removed was never composed.
func TestSubModulePlacementIsReadAcrossBothAxes(t *testing.T) {
	t.Parallel()

	const rootAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	const rootAggregateInactiveModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
      active: "false"
`
	// The module binds its own required socket, so a fixture built on it halts
	// only for the rule under test.
	const module = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: store
    store:
      type: app/db:1.0.0
`
	const emptyModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components: {}
`

	const boundaryPlacingNothing = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
`
	const boundaryRoutingTheModule = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      aggregates:
        mod: {}
`
	const boundaryNamingEveryComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        mod.consumer: {}
        mod.store: {}
`
	// One of the module's two components, and no routing entry. The module is
	// placed: which of its components carry their own entries is a separate
	// question from whether the deployment placed the module at all.
	const boundaryNamingOneComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        mod.store: {}
`

	cases := []struct {
		name     string
		root     string
		module   string
		release  string
		sentinel error
	}{
		{
			name:     "no boundary reaches the module on either axis",
			root:     rootAggregate,
			module:   module,
			release:  boundaryPlacingNothing,
			sentinel: ErrUnplacedSubModule(),
		},
		{
			name:    "a routing entry places the module",
			root:    rootAggregate,
			module:  module,
			release: boundaryRoutingTheModule,
		},
		{
			name:    "naming every component by qualified dotted path places the module",
			root:    rootAggregate,
			module:  module,
			release: boundaryNamingEveryComponent,
		},
		{
			name:    "naming one component by qualified dotted path places the module",
			root:    rootAggregate,
			module:  module,
			release: boundaryNamingOneComponent,
		},
		{
			name:    "a module contributing no component has nothing to place",
			root:    rootAggregate,
			module:  emptyModule,
			release: boundaryPlacingNothing,
		},
		{
			name:    "a module a false activation toggle removed is not asked about",
			root:    rootAggregateInactiveModule,
			module:  module,
			release: boundaryPlacingNothing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildWith(t, tc.root, tc.release, tc.module)
			if tc.sentinel != nil {
				if !errors.Is(err, tc.sentinel) {
					t.Fatalf("violating input must halt with the named rule; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("legitimate input must pass; got %v", err)
			}
		})
	}
}

// TestSubModulePlacementLocatesAtTheImportingDocument proves the halt points at
// the `aggregates` entry the author wrote rather than at a component the author
// did not.
//
// The locator is what a reader — or an editor placing a marker — navigates by, so
// a rule naming the right fault at the wrong site sends the author looking in a
// document that is correct as written. The remedy here is one key in the
// Aggregate's own import block, and that is the key the diagnostic names.
func TestSubModulePlacementLocatesAtTheImportingDocument(t *testing.T) {
	t.Parallel()

	const rootAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	const module = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    store:
      type: app/db:1.0.0
`
	const releasePlacingNothing = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
`

	err := buildWith(t, rootAggregate, releasePlacingNothing, module)
	if !errors.Is(err, ErrUnplacedSubModule()) {
		t.Fatalf("expected the placement rule to halt; got %v", err)
	}
	// The locator is read off the violation rather than out of the collected
	// error's summary text: the summary counts violations, and the field a
	// front end positions on is carried per violation.
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("the halt must carry its violations; got %T", err)
	}
	if len(multi.Violations) != 1 {
		t.Fatalf("one unplaced module must raise exactly one violation; got %d", len(multi.Violations))
	}
	const want = ".spec.aggregates.mod"
	if got := multi.Violations[0].Field; got != want {
		t.Fatalf("the diagnostic must locate at %q, the import entry the author wrote; got %q", want, got)
	}
	if got := multi.Violations[0].Entity; got != "Release demo-prod" {
		t.Fatalf("the diagnostic must name the document the rule is about; got %q", got)
	}
}

// TestSubModulePlacementAcrossNesting proves the two things nesting changes, and
// it is separate from the tables above because both need a composition two levels
// deep and a loader that answers per module rather than with one document.
//
// The first is that a module's placement covers what it contains: routing the
// outer module places the inner module's components too, so neither module is
// reported even though no entry anywhere names the inner one. Reporting it would
// send an author to add a routing entry for a module the deployment has already
// placed.
//
// The second is how a nested module is named when nothing places it. The entry an
// author edits is written in the document that IMPORTS the module, which for a
// nested one is the outer module's own document, and that document spells it with
// the one name it has inside that scope. So the locator carries the local name
// rather than the dotted path — a dotted path would name a key no document
// contains.
func TestSubModulePlacementAcrossNesting(t *testing.T) {
	t.Parallel()

	const rootAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    outer:
      type: ./outer.yaml
`
	const outerModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: outer
spec:
  aggregates:
    inner:
      type: ./inner.yaml
`
	const innerModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: inner
spec:
  components:
    store:
      type: app/db:1.0.0
`
	const placingNothing = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
`
	const routingTheOuterModule = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      aggregates:
        outer: {}
`

	build := func(t *testing.T, relYAML string) error {
		t.Helper()
		normalized := types.NormalizeRelease(mustRelease(t, relYAML))
		boundaryInfra, _, routedInfra := resolver.WireBoundaryContexts(normalized)
		// Each module answers with its own document and its own file, so the
		// importing document a locator is built from is the real one at each
		// level rather than one file standing in for every module.
		loader := func(ref string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
			switch ref {
			case "./outer.yaml":
				return mustAggregate(t, outerModule), "outer.yaml", nil, nil
			case "./inner.yaml":
				return mustAggregate(t, innerModule), "inner.yaml", nil, nil
			}
			return nil, "", nil, fmt.Errorf("fixture has no module %q", ref)
		}
		_, err := Build(BuildConfig{
			ResolveResult: &resolver.ResolveResult{
				Aggregate:            mustAggregate(t, rootAggregate),
				Release:              normalized.Release(),
				BoundaryInfra:        boundaryInfra,
				RoutedAggregateInfra: routedInfra,
			},
			CatalogLookup:    bindingCatalog(t),
			LoadSubAggregate: loader,
		})
		return err
	}

	t.Run("routing the outer module places what the inner one contributed", func(t *testing.T) {
		t.Parallel()
		if err := build(t, routingTheOuterModule); err != nil {
			t.Fatalf("routing the outer module must place the whole composition; got %v", err)
		}
	})

	t.Run("an unplaced nested module is named by its local key", func(t *testing.T) {
		t.Parallel()
		err := build(t, placingNothing)
		if !errors.Is(err, ErrUnplacedSubModule()) {
			t.Fatalf("expected the placement rule to halt; got %v", err)
		}
		var multi *resolver.MultiResolverError
		if !errors.As(err, &multi) {
			t.Fatalf("the halt must carry its violations; got %T", err)
		}
		// Both modules are reported: the inner one contributed the node, and the
		// outer one contains it, so each is a module the deployment placed
		// nowhere and each is one an author can fix at its own key.
		got := make([]string, 0, len(multi.Violations))
		for _, v := range multi.Violations {
			got = append(got, v.Field)
		}
		want := []string{".spec.aggregates.outer", ".spec.aggregates.inner"}
		if !slices.Equal(got, want) {
			t.Fatalf("locators must be the local keys their own importing documents spell, outer before inner; want %v, got %v", want, got)
		}
	})
}

// TestPlacementRulesDivideTheGroundAndEachReportsOnce proves the two placement
// rules cover the ground between them without either reporting what the other
// already reported.
//
// The case that separates a correct pair from a naive one is a module the
// deployment placed nowhere: it is one authoring fault with one remedy at one
// key, and a component rule that did not pass a module's own components would
// report it once for the module and again for every component the module
// contributed. The first row pins that it is reported once.
//
// The second row is the ground the module rule does not cover at all — a
// component of the application's own document, which belongs to no module and so
// is reported by nothing until the component rule exists.
//
// The third row is the exclusion's own cost, asserted rather than left implied:
// a module the deployment placed only in part passes both rules, because the
// module rule reads the module as placed and the component rule passes every
// component a module contributed. Both rules staying silent here is the
// behaviour, not an oversight.
func TestPlacementRulesDivideTheGroundAndEachReportsOnce(t *testing.T) {
	t.Parallel()

	const rootWithModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	// Two components, so a rule reporting per component rather than per module
	// would report twice where one report is right.
	const twoComponentModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: store
    store:
      type: app/db:1.0.0
`
	// No module at all: both components are the application's own, and the
	// boundary names one of them.
	const rootWithOwnComponents = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: db
    db:
      type: app/db:1.0.0
`
	const boundaryPlacingNothing = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
`
	const boundaryNamingOnlyTheConsumer = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        api: {}
`
	const boundaryNamingOneModuleComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        mod.store: {}
`

	cases := []struct {
		name       string
		root       string
		module     string
		release    string
		sentinel   error
		wantFields []string
	}{
		{
			name:       "a module placed nowhere is reported once, at the module and not per component",
			root:       rootWithModule,
			module:     twoComponentModule,
			release:    boundaryPlacingNothing,
			sentinel:   ErrUnplacedSubModule(),
			wantFields: []string{".spec.aggregates.mod"},
		},
		{
			name:       "a component of the application's own document is reported once, by the component rule",
			root:       rootWithOwnComponents,
			release:    boundaryNamingOnlyTheConsumer,
			sentinel:   ErrUnplacedComponent(),
			wantFields: []string{".spec.components.db"},
		},
		{
			name:    "a module placed in part is passed by both rules",
			root:    rootWithModule,
			module:  twoComponentModule,
			release: boundaryNamingOneModuleComponent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildWith(t, tc.root, tc.release, tc.module)
			if tc.sentinel == nil {
				if err != nil {
					t.Fatalf("both rules must pass this shape; got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("the halt must carry the named rule; got %v", err)
			}
			var multi *resolver.MultiResolverError
			if !errors.As(err, &multi) {
				t.Fatalf("the halt must carry its violations; got %T", err)
			}
			got := make([]string, 0, len(multi.Violations))
			for _, v := range multi.Violations {
				got = append(got, v.Field)
			}
			if !slices.Equal(got, tc.wantFields) {
				t.Fatalf("one fault must be reported once, at the key its author edits; want %v, got %v", tc.wantFields, got)
			}
		})
	}
}
