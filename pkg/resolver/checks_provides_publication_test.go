// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The reading over what a bound Recipe publishes, asserted through the surface a
// build reaches rather than by calling its predicate.
//
// Two properties are separable and both are asserted. What the reading REPORTS is
// asserted through [RunAdvisories]. That the suite actually INVOKES it is a
// distinct claim — a rule nothing invokes raises nothing, and a table of silence
// cases would pass over it in perfect green — so every silence row below is paired
// with a positive row over the same fixture shape.

// recipePublishing is a Recipe publishing the named keys, in the order given. The
// order is the fixture's point in one row below, so it is written rather than
// sorted here.
func recipePublishing(name string, keys ...string) *types.Recipe {
	provides := types.OrderedMap[string]{}
	for _, k := range keys {
		provides.Set(k, "{{ .instance }}-"+k)
	}
	return &types.Recipe{
		Metadata: types.RecipeMetadata{Name: name, Version: "1.0.0"},
		Spec:     types.RecipeSpec{Provides: provides},
	}
}

// bindEvery answers one Recipe for every instance, which is the shape a type
// mapping produces.
func bindEvery(r *types.Recipe) RecipeBinding {
	return func(string, string) (*types.Recipe, bool) { return r, true }
}

// componentsNamed is an Aggregate declaring one instance of the fixture type per
// name given, in that order.
func componentsNamed(t *testing.T, names ...string) types.NormalizedAggregate {
	t.Helper()
	comps := types.OrderedMap[types.ComponentInstance]{}
	for _, n := range names {
		comps.Set(n, types.ComponentInstance{Type: componentTypeURI})
	}
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "publication-app"},
		Spec:     types.AggregateSpec{Components: comps},
	})
}

// declaresProvides is a catalog whose one type declares the named provides keys
// and nothing else — no sockets and no slots, so the near-miss readings sharing
// the suite have nothing to report and every finding belongs to this rule.
func declaresProvides(t *testing.T, keys ...string) CatalogLookup {
	t.Helper()
	var b strings.Builder
	b.WriteString("provides:\n")
	for _, k := range keys {
		b.WriteString("  " + k + ":\n    type: string\n")
	}
	return singleTypeCatalog(schemaComponent(t, "app/svc", b.String()))
}

// publicationFindings asks the whole suite and narrows to this rule's findings,
// failing on any other advisory: a fixture that started reporting a second rule
// would otherwise make this rule's own count unreadable.
func publicationFindings(
	t *testing.T,
	na types.NormalizedAggregate,
	catalog CatalogLookup,
	recipeFor RecipeBinding,
) []*ResolverError {
	t.Helper()
	var mine []*ResolverError
	for _, f := range advisoriesBoundTo(t, na, catalog, recipeFor) {
		if f.Err != ErrProvidesKeyNotDeclared() {
			t.Fatalf("the suite reported an unrelated advisory over this fixture: %v", f)
		}
		mine = append(mine, f)
	}
	return mine
}

// TestPublishedProvidesKeyIsDeclared covers what the reading reports, its silence
// cases, and the document each finding is attributed to.
func TestPublishedProvidesKeyIsDeclared(t *testing.T) {
	t.Parallel()

	threeKeys := recipePublishing("svc-recipe", "port", "host", "dsn")

	for _, tc := range []struct {
		name     string
		instance []string
		declared []string
		recipe   *types.Recipe
		binding  func(*types.Recipe) RecipeBinding
		// wantSubjects is the entity-and-locator sequence, written out in full: it
		// carries the count, the order and the attribution in one assertion, and a
		// sequence compared against a second run of the same code would carry none
		// of them.
		wantSubjects []string
		// wantNames are spellings every finding must name between them.
		wantNames []string
	}{
		{
			// The declared order is the reported order, and it is NOT the sorted
			// one: the Recipe writes port before dsn, and both are reported in that
			// order.
			name:     "each undeclared key, in the order the Recipe declares them",
			instance: []string{"svc"},
			declared: []string{"host"},
			recipe:   threeKeys,
			binding:  bindEvery,
			wantSubjects: []string{
				"Recipe svc-recipe|.spec.provides.port",
				"Recipe svc-recipe|.spec.provides.dsn",
			},
			wantNames: []string{`"port"`, `"dsn"`, `"app/svc:1.0.0"`, `"svc"`},
		},
		{
			// 🔴 The multiplicity this rule reports, pinned so a change to it is
			// visible. One Recipe serving three instances of one type raises per
			// instance: the entry at fault is written once, so the three findings
			// for one key share a locator and are told apart by the instance each
			// names.
			name:     "one Recipe on three instances reports per instance",
			instance: []string{"svc-a", "svc-b", "svc-c"},
			declared: []string{"host"},
			recipe:   threeKeys,
			binding:  bindEvery,
			wantSubjects: []string{
				"Recipe svc-recipe|.spec.provides.port",
				"Recipe svc-recipe|.spec.provides.dsn",
				"Recipe svc-recipe|.spec.provides.port",
				"Recipe svc-recipe|.spec.provides.dsn",
				"Recipe svc-recipe|.spec.provides.port",
				"Recipe svc-recipe|.spec.provides.dsn",
			},
			wantNames: []string{`"svc-a"`, `"svc-b"`, `"svc-c"`},
		},
		{
			name:         "a Recipe covering the contract exactly is silent",
			instance:     []string{"svc"},
			declared:     []string{"host", "port", "dsn"},
			recipe:       threeKeys,
			binding:      bindEvery,
			wantSubjects: nil,
		},
		{
			name:         "a Recipe publishing nothing is silent",
			instance:     []string{"svc"},
			declared:     []string{"host"},
			recipe:       recipePublishing("bare-recipe"),
			binding:      bindEvery,
			wantSubjects: nil,
		},
		{
			// No declaration was read, which is not the same as a declaration of
			// nothing: reporting every published key here would turn a catalog the
			// view cannot answer into an advisory about the author's Recipe.
			name:         "a component type the catalog does not answer for is silent",
			instance:     []string{"other"},
			declared:     []string{"host"},
			recipe:       threeKeys,
			binding:      bindEvery,
			wantSubjects: nil,
		},
		{
			name:     "a caller offering no binding is silent",
			instance: []string{"svc"},
			declared: []string{"host"},
			recipe:   threeKeys,
			binding: func(*types.Recipe) RecipeBinding {
				return nil
			},
			wantSubjects: nil,
		},
		{
			name:     "a node the binding resolves no Recipe for is silent",
			instance: []string{"svc"},
			declared: []string{"host"},
			recipe:   threeKeys,
			binding: func(*types.Recipe) RecipeBinding {
				return func(string, string) (*types.Recipe, bool) { return nil, false }
			},
			wantSubjects: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			na := componentsNamed(t, tc.instance...)
			// The "catalog does not answer" row declares its instance under a type
			// the fixture catalog does not hold, which is what singleTypeCatalog
			// answers false for.
			if tc.instance[0] == "other" {
				comps := types.OrderedMap[types.ComponentInstance]{}
				comps.Set("other", types.ComponentInstance{Type: "app/unknown:1.0.0"})
				na = normalized(t, &types.Aggregate{
					Metadata: types.AggregateMetadata{Name: "publication-app"},
					Spec:     types.AggregateSpec{Components: comps},
				})
			}

			got := publicationFindings(t, na, declaresProvides(t, tc.declared...), tc.binding(tc.recipe))

			var subjects []string
			for _, f := range got {
				subjects = append(subjects, f.Entity+"|"+f.Field)
			}
			if len(subjects) != len(tc.wantSubjects) {
				t.Fatalf("reported %d finding(s) %v; want %d %v",
					len(subjects), subjects, len(tc.wantSubjects), tc.wantSubjects)
			}
			for i := range subjects {
				if subjects[i] != tc.wantSubjects[i] {
					t.Errorf("finding %d is attributed to %q; want %q", i, subjects[i], tc.wantSubjects[i])
				}
			}
			joined := strings.Join(messagesOfAdvisories(got), " | ")
			for _, name := range tc.wantNames {
				if !strings.Contains(joined, name) {
					t.Errorf("no finding names %s; the set reads %q", name, joined)
				}
			}
		})
	}
}

// TestPublishedProvidesKeyIsDeclared_ReachesASubsystemInstance pins that the axis
// walks the resolved subsystem tree rather than the root component block alone.
// The instance below is declared nowhere else, so a walk that missed it would
// report nothing and read exactly like a Recipe that covers its contract.
func TestPublishedProvidesKeyIsDeclared_ReachesASubsystemInstance(t *testing.T) {
	t.Parallel()

	inner := types.OrderedMap[types.ComponentInstance]{}
	inner.Set("deep", types.ComponentInstance{Type: componentTypeURI})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("tier", types.Subsystem{Components: inner})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "publication-app"},
		Spec:     types.AggregateSpec{Subsystems: subsystems},
	})

	got := publicationFindings(t, na, declaresProvides(t, "host"),
		bindEvery(recipePublishing("svc-recipe", "port")))

	if len(got) != 1 {
		t.Fatalf("reported %d finding(s); want 1 over the nested instance", len(got))
	}
	if !strings.Contains(got[0].Message, `"deep"`) {
		t.Errorf("the finding does not name the nested instance: %q", got[0].Message)
	}
}

// TestProvidesKeysEnumeratesInDeclaredOrder asserts the axis order directly, which
// is what makes the reported order above a property of the enumeration rather than
// of the predicate. Sorted order is the control: the fixture declares its keys in
// an order sorting would change, so an implementation that sorted would fail here.
func TestProvidesKeysEnumeratesInDeclaredOrder(t *testing.T) {
	t.Parallel()

	na := componentsNamed(t, "svc")
	view := newNormalizedView(na, nil, declaresProvides(t, "host"), nil)
	coordinates := newCoordinateSet(view).
		withRecipeBinding(bindEvery(recipePublishing("svc-recipe", "port", "host", "dsn")))

	var got []string
	for _, c := range coordinates.ProvidesKeys("svc") {
		got = append(got, c.Value)
		if c.SourceFile != "svc-recipe" {
			t.Errorf("coordinate %q is positioned at %q; want the Recipe it was written in", c.Value, c.SourceFile)
		}
	}
	want := []string{"port", "host", "dsn"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the axis enumerates %v; want the declared order %v", got, want)
	}

	// A set built without a binding enumerates this axis emptily, which is what
	// makes the nil-binding silence row above a property of the axis rather than
	// of the predicate.
	if unbound := newCoordinateSet(view).ProvidesKeys("svc"); unbound != nil {
		t.Errorf("a set with no binding enumerated %v; want nothing", unbound)
	}
}
