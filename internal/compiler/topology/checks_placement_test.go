// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"fmt"
	"slices"
	"strings"
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

// TestBoundaryComponentResolutionReadsBothAxesAndNotTheSpelling proves the
// over-naming rule fires on whether a name resolves and never on what it looks
// like.
//
// The halting rows are one per axis a boundary names components on, because a
// rule reading only one of them would leave the other unchecked with nothing
// saying so: a boundary names a component under the id it carries once the
// composition is flattened, and a routing entry names one under the local name it
// carries inside the module it routes.
//
// The passing rows are what make the halting ones mean anything. A qualified
// dotted id that resolves must pass, or the rule would be refusing dotted names
// rather than unresolvable ones — and a sub-module's prefix legitimately carries a
// dot, so a spelling rule would refuse the routed axis's own required output. A
// routing entry naming a component the module does declare must pass for the same
// reason one coordinate in: the local name is resolved through the module's prefix
// before it is compared, and a rule comparing the local name against the flattened
// set would refuse every module-routed deployment that names components.
//
// The last row is the shape a stricter rule would wrongly halt. A component
// switched off by a false activation toggle is declared and deliberately removed,
// and the deployment that configures it is not rewritten in the same edit.
func TestBoundaryComponentResolutionReadsBothAxesAndNotTheSpelling(t *testing.T) {
	t.Parallel()

	const rootWithSubsystem = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  subsystems:
    data:
      components:
        reader:
          type: app/db:1.0.0
`
	const rootWithModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	const rootWithDeactivatedComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/db:1.0.0
      active: "false"
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

	// A declared subsystem contributes nothing to the id its components carry, so
	// the flattened id here is the bare `reader` and the second entry addresses
	// nothing. It is the shape the fault takes in practice.
	//
	// The real component is placed alongside it deliberately, so this fixture
	// carries one fault. An entry of this shape ordinarily carries two — it
	// addresses nothing AND leaves the component it was meant to place in no
	// boundary — and a fixture carrying both proves nothing about this rule,
	// because the placement rule reports such a document first.
	const boundaryNamingASubsystemQualifiedId = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        reader: {}
        data.reader: {}
`
	const boundaryNamingTheFlattenedId = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        reader: {}
`
	const boundaryRoutingAndNamingAComponentTheModuleLacks = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      aggregates:
        mod:
          components:
            reader: {}
`
	const boundaryRoutingAndNamingAComponentTheModuleDeclares = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      aggregates:
        mod:
          components:
            store: {}
`
	const boundaryNamingTheModuleComponentByQualifiedId = `apiVersion: aggrega.ai/v1alpha1
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
	const boundaryNamingTheDeactivatedComponent = `apiVersion: aggrega.ai/v1alpha1
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

	cases := []struct {
		name     string
		root     string
		module   string
		release  string
		sentinel error
	}{
		{
			name:     "the direct axis names an id the composition does not hold",
			root:     rootWithSubsystem,
			release:  boundaryNamingASubsystemQualifiedId,
			sentinel: ErrUnresolvedBoundaryComponent(),
		},
		{
			name:     "a routing entry names a component the module does not declare",
			root:     rootWithModule,
			module:   module,
			release:  boundaryRoutingAndNamingAComponentTheModuleLacks,
			sentinel: ErrUnresolvedBoundaryComponent(),
		},
		{
			name:    "the direct axis names the flattened id the subsystem's component carries",
			root:    rootWithSubsystem,
			release: boundaryNamingTheFlattenedId,
		},
		{
			name:    "a routing entry names a component the module declares",
			root:    rootWithModule,
			module:  module,
			release: boundaryRoutingAndNamingAComponentTheModuleDeclares,
		},
		{
			name:    "the direct axis names a module's component by its qualified dotted id",
			root:    rootWithModule,
			module:  module,
			release: boundaryNamingTheModuleComponentByQualifiedId,
		},
		{
			name:    "a component a false activation toggle removed is still nameable",
			root:    rootWithDeactivatedComponent,
			release: boundaryNamingTheDeactivatedComponent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildWith(t, tc.root, tc.release, tc.module)
			if tc.sentinel == nil {
				if err != nil {
					t.Fatalf("legitimate input must pass; got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("violating input must halt with the named rule; got %v", err)
			}
		})
	}
}

// TestBoundaryComponentResolutionNamesTheKeyAndTheIdItAddressed proves the
// diagnostic tells an author both halves of what they need: where the entry is,
// and what it asked the composition for.
//
// The two halves are separately assertable because they are separately losable. A
// finding carrying the id and no position leaves an author searching a deployment
// document for a key; a finding carrying a position and no id leaves them at the
// right block without the value to compare against the composition — and on the
// routed axis the id is not the name the file spells, so the position alone
// cannot supply it.
//
// The rows are one per authoring shape, and the direct-axis pair is what shows the
// locator dialect's own rule rather than a choice this rule makes: an id carrying
// the dialect's separator cannot be spelled as one segment, so the locator names
// the block that holds it and stops, while an id that carries none is named in
// full.
func TestBoundaryComponentResolutionNamesTheKeyAndTheIdItAddressed(t *testing.T) {
	t.Parallel()

	const rootWithSubsystem = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  subsystems:
    data:
      components:
        reader:
          type: app/db:1.0.0
`
	const rootWithModule = `apiVersion: aggrega.ai/v1alpha1
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

	const dottedIdUnderANestedBoundary = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      boundaries:
        edge:
          directory: ./out
          components:
            reader: {}
            data.reader: {}
`
	const bareIdUnderANamedBoundary = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        reader: {}
        raeder: {}
`
	const localNameInsideARoutingEntry = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      aggregates:
        mod:
          components:
            store: {}
            reader: {}
`
	// The deployment writes its entries at its own top level, which normalization
	// folds into a boundary the author never wrote. The locator has to name the
	// site the author did write.
	const topLevelEntries = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  components:
    reader: {}
    raeder: {}
`

	cases := []struct {
		name      string
		root      string
		module    string
		release   string
		wantField string
		wantValue string
	}{
		{
			name:      "a dotted id names the block that holds it and carries the id as the value",
			root:      rootWithSubsystem,
			release:   dottedIdUnderANestedBoundary,
			wantField: ".spec.boundaries.prod.boundaries.edge.components",
			wantValue: "data.reader",
		},
		{
			name:      "an id carrying no separator is named in full",
			root:      rootWithSubsystem,
			release:   bareIdUnderANamedBoundary,
			wantField: ".spec.boundaries.prod.components.raeder",
			wantValue: "raeder",
		},
		{
			name:      "a routing entry names the local key the author typed and reports the id it addressed",
			root:      rootWithModule,
			module:    module,
			release:   localNameInsideARoutingEntry,
			wantField: ".spec.boundaries.prod.aggregates.mod.components.reader",
			wantValue: "mod.reader",
		},
		{
			name:      "an entry written at the document's own top level names the top-level site",
			root:      rootWithSubsystem,
			release:   topLevelEntries,
			wantField: ".spec.components.raeder",
			wantValue: "raeder",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildWith(t, tc.root, tc.release, tc.module)
			if !errors.Is(err, ErrUnresolvedBoundaryComponent()) {
				t.Fatalf("the halt must carry the named rule; got %v", err)
			}
			var multi *resolver.MultiResolverError
			if !errors.As(err, &multi) {
				t.Fatalf("the halt must carry its violations; got %T", err)
			}
			if len(multi.Violations) != 1 {
				t.Fatalf("one unresolvable entry must raise exactly one violation; got %d (%v)",
					len(multi.Violations), multi.Violations)
			}
			if got := multi.Violations[0].Field; got != tc.wantField {
				t.Fatalf("the diagnostic must locate at %q, the key the author wrote; got %q",
					tc.wantField, got)
			}
			if got := multi.Violations[0].Message; !containsValue(got, tc.wantValue) {
				t.Fatalf("the diagnostic must name the id the entry addressed, %q; got %q",
					tc.wantValue, got)
			}
			if got := multi.Violations[0].Entity; got != "Release demo-prod" {
				t.Fatalf("the diagnostic must name the document the rule is about; got %q", got)
			}
		})
	}
}

// TestPlacementIsReportedBeforeResolution proves a document carrying both
// directions of the placement relation halts under the placement rule, which is
// the identity it halted under before the resolution rule existed.
//
// The ordering is a decision rather than an accident, and it is pinned here
// because nothing else would notice it moving. Such a document is repaired at the
// placement end, and reporting placement first leaves every document that already
// halted halting under the identity it always did — so the resolution rule adds
// refusals without re-identifying any.
func TestPlacementIsReportedBeforeResolution(t *testing.T) {
	t.Parallel()

	// The subsystem-qualified entry is both faults at once: it addresses nothing,
	// and the component it was meant to place is left in no boundary.
	const rootWithSubsystem = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  subsystems:
    data:
      components:
        reader:
          type: app/db:1.0.0
`
	const boundaryNamingASubsystemQualifiedId = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        data.reader: {}
`

	err := buildWith(t, rootWithSubsystem, boundaryNamingASubsystemQualifiedId, "")
	if !errors.Is(err, ErrUnplacedComponent()) {
		t.Fatalf("a document carrying both faults must halt under the placement rule; got %v", err)
	}
	if errors.Is(err, ErrUnresolvedBoundaryComponent()) {
		t.Fatalf("the placement halt must not also carry the resolution rule; got %v", err)
	}
}

// containsValue reports whether message carries value, so the assertions above
// read against the rule's own detail rather than against a composed sentence they
// would have to reproduce.
func containsValue(message, value string) bool {
	return strings.Contains(message, value)
}
