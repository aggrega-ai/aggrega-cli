// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The producer-fulfilment rule reads two declared facts — the value a node's
// Recipe resolved for a provides key, and the component type the flattened node
// carries — and this file runs the declared value through the framework's own
// traversal.
//
// # Why the rows are driven through the traversal rather than the predicate
//
// The predicate is one presence test, so a call straight into it would prove
// almost nothing. What decides whether this rule works is the AXIS: the keys it is
// invoked over are the ones a component type's contract declares, not the ones the
// Recipe published, and a rule handed the published set is invoked zero times for
// exactly the inputs it exists to refuse. Driving the traversal is what puts the
// axis under test alongside the predicate.
//
// # The stub embeds the view interface, and the nil embedding is the point
//
// providesContractFactStub embeds [constraint.HydratedView] and overrides only the
// two accessors this rule reads. The embedded interface is nil, so any other
// accessor panics rather than returning a zero value — which turns "the predicate
// reads declared facts and nothing else" from a claim about the source into a
// property this file enforces.

// providesContractFactStub answers the two facts the rule reads and nothing else.
type providesContractFactStub struct {
	constraint.HydratedView

	// resolved is the render's resolved-provides bag, per node.
	resolved map[string]types.OrderedMap[string]

	// componentTypes is the type each flattened node carries.
	componentTypes map[string]string
}

func (s providesContractFactStub) ResolvedNodeProvides(node string, key string) (string, bool) {
	bag, held := s.resolved[node]
	if !held {
		return "", false
	}
	return bag.Get(key)
}

func (s providesContractFactStub) FlattenedNodeComponentType(node string) (string, bool) {
	componentType, held := s.componentTypes[node]
	return componentType, held
}

// bagOf builds a resolved-provides bag from key/value pairs, so a row can set an
// empty value under a key that WAS resolved — which is the pair the rule has to
// tell apart from a key that was not.
func bagOf(pairs ...string) types.OrderedMap[string] {
	bag := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		bag.Set(pairs[i], pairs[i+1])
	}
	return bag
}

// contractOf builds one node's declared contract.
func contractOf(keys ...string) map[string]bool {
	out := map[string]bool{}
	for _, key := range keys {
		out[key] = true
	}
	return out
}

// recipeNamed is the render's binding for one node, which is what a finding is
// positioned at.
func recipeNamed(name string) *types.Recipe {
	return &types.Recipe{Metadata: types.RecipeMetadata{Name: name}}
}

// providesContractHalt drives the declared rule over a render's retained
// contracts through the framework's own traversal and returns what it raised as
// the render's halt.
//
// It is the shape the build takes rather than a shortcut into it: the same
// traversal, the same axis value assembled from the same three products, and the
// same rendering into a categorised halt.
func providesContractHalt(
	t *testing.T,
	nodeOrder []string,
	declared NodeDeclaredProvides,
	recipes map[string]*types.Recipe,
	resolved map[string]types.OrderedMap[string],
	componentTypes map[string]string,
) error {
	t.Helper()
	view := providesContractFactStub{resolved: resolved, componentTypes: componentTypes}
	return runProvidesContractRule(nil, true, view, declaredProvidesContract{
		nodeOrder: nodeOrder,
		declared:  declared,
		recipes:   recipes,
	})
}

// providesContractFindings reads a halt's findings structurally rather than
// through its outermost text.
//
// The outermost frame of a halt joining several findings reports the envelope
// alone and drops what is underneath, so a substring assertion on it would pass on
// the envelope and see none of the detail. Both branches of the halt's
// count-branching construction are handled: one finding arrives on its own, and
// several arrive joined.
func providesContractFindings(t *testing.T, halt error) []string {
	t.Helper()
	if halt == nil {
		return nil
	}
	var out []string
	var walk func(err error)
	walk = func(err error) {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, sub := range joined.Unwrap() {
				walk(sub)
			}
			return
		}
		var ce *cerrors.CompilerError
		if stderrors.As(err, &ce) && ce.Message() != "" {
			if inner := ce.Unwrap(); inner != nil {
				if _, joinedInner := inner.(interface{ Unwrap() []error }); joinedInner {
					walk(inner)
					return
				}
			}
			out = append(out, ce.Message())
		}
	}
	walk(halt)
	return out
}

// TestProvidesContractRule walks the rule over the rows that make it
// discriminate, refusals and silences together.
//
// The silent rows are not padding. A rule that refused every declared key would
// satisfy every refusal row here, and the only thing that can see it is an input
// whose Recipe does fulfil its contract and has to keep rendering.
func TestProvidesContractRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		nodeOrder      []string
		declared       NodeDeclaredProvides
		recipes        map[string]*types.Recipe
		resolved       map[string]types.OrderedMap[string]
		componentTypes map[string]string
		// want is one expected finding per entry, each a set of substrings the
		// message has to carry, separated by a pipe.
		want []string
	}{
		{
			name:           "REFUSAL: a Recipe short of one of two declared keys",
			nodeOrder:      []string{"store"},
			declared:       NodeDeclaredProvides{"store": contractOf("endpoint", "dsn")},
			recipes:        map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			resolved:       map[string]types.OrderedMap[string]{"store": bagOf("endpoint", "svc.local")},
			componentTypes: map[string]string{"store": "core/store"},
			want:           []string{`"dsn"|declared by component type "core/store"|bound to component instance "store"`},
		},
		{
			name:           "REFUSAL: two missing keys are two findings, in sorted key order",
			nodeOrder:      []string{"store"},
			declared:       NodeDeclaredProvides{"store": contractOf("endpoint", "dsn")},
			recipes:        map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			resolved:       map[string]types.OrderedMap[string]{"store": bagOf()},
			componentTypes: map[string]string{"store": "core/store"},
			want:           []string{`"dsn"`, `"endpoint"`},
		},
		{
			name:      "REFUSAL: two nodes each short, in the walk's node order",
			nodeOrder: []string{"alpha", "beta"},
			declared: NodeDeclaredProvides{
				"alpha": contractOf("a"),
				"beta":  contractOf("b"),
			},
			recipes: map[string]*types.Recipe{
				"alpha": recipeNamed("alpha-recipe"),
				"beta":  recipeNamed("beta-recipe"),
			},
			resolved: map[string]types.OrderedMap[string]{
				"alpha": bagOf(),
				"beta":  bagOf(),
			},
			componentTypes: map[string]string{"alpha": "core/a", "beta": "core/b"},
			want: []string{
				`"a"|bound to component instance "alpha"`,
				`"b"|bound to component instance "beta"`,
			},
		},
		{
			name:      "REFUSAL: the fact is keyed by node, so a sibling's resolution does not fulfil it",
			nodeOrder: []string{"other", "store"},
			declared:  NodeDeclaredProvides{"store": contractOf("dsn")},
			recipes:   map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			// The key IS resolved — on a different node. A predicate reading the
			// bag without its node key would report this contract fulfilled.
			resolved:       map[string]types.OrderedMap[string]{"other": bagOf("dsn", "postgres://x")},
			componentTypes: map[string]string{"store": "core/store", "other": "core/other"},
			want:           []string{`"dsn"|bound to component instance "store"`},
		},
		{
			name:      "REFUSAL: a node whose render resolved no provides bag at all",
			nodeOrder: []string{"store"},
			declared:  NodeDeclaredProvides{"store": contractOf("dsn")},
			recipes:   map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			// No entry for the node: the render reached it and it resolved nothing.
			resolved:       map[string]types.OrderedMap[string]{},
			componentTypes: map[string]string{"store": "core/store"},
			want:           []string{`"dsn"`},
		},
		{
			name:           "SILENT: every declared key resolved",
			nodeOrder:      []string{"store"},
			declared:       NodeDeclaredProvides{"store": contractOf("endpoint", "dsn")},
			recipes:        map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			resolved:       map[string]types.OrderedMap[string]{"store": bagOf("endpoint", "svc.local", "dsn", "postgres://x")},
			componentTypes: map[string]string{"store": "core/store"},
		},
		{
			name:      "SILENT: a Recipe publishing MORE than the contract declares",
			nodeOrder: []string{"store"},
			// The contract is subset-required and superset-permitted, so the extra
			// key is not this rule's finding. The advisory that names it reads the
			// published side of the same contract and is a different rule.
			declared: NodeDeclaredProvides{"store": contractOf("endpoint")},
			recipes:  map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			resolved: map[string]types.OrderedMap[string]{
				"store": bagOf("endpoint", "svc.local", "internal_handle", "x"),
			},
			componentTypes: map[string]string{"store": "core/store"},
		},
		{
			name: "SILENT: a declared key resolved to the EMPTY string is still resolved",
			// This is the row that makes presence rather than value the question.
			// A predicate testing the value instead of the second result would
			// refuse this, and every refusal row above would still pass.
			nodeOrder:      []string{"store"},
			declared:       NodeDeclaredProvides{"store": contractOf("endpoint")},
			recipes:        map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			resolved:       map[string]types.OrderedMap[string]{"store": bagOf("endpoint", "")},
			componentTypes: map[string]string{"store": "core/store"},
		},
		{
			name:      "SILENT: a node with no bound Recipe is not on the axis",
			nodeOrder: []string{"store"},
			// The retention holds the contract, and the render bound no Recipe. A
			// node with no realizer has no realizer to hold to a contract, which
			// is the reading the retention makes of the same absence.
			declared:       NodeDeclaredProvides{"store": contractOf("dsn")},
			recipes:        map[string]*types.Recipe{},
			resolved:       map[string]types.OrderedMap[string]{},
			componentTypes: map[string]string{"store": "core/store"},
		},
		{
			name:      "SILENT: a node absent from the retention is not on the axis",
			nodeOrder: []string{"store"},
			// A component type the catalog did not answer for leaves the node out
			// of the retention. No declaration was read, which is not a
			// declaration of nothing.
			declared:       NodeDeclaredProvides{},
			recipes:        map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			resolved:       map[string]types.OrderedMap[string]{},
			componentTypes: map[string]string{"store": "core/store"},
		},
		{
			name:           "SILENT: a contract declaring no keys has nothing to enforce",
			nodeOrder:      []string{"store"},
			declared:       NodeDeclaredProvides{"store": contractOf()},
			recipes:        map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
			resolved:       map[string]types.OrderedMap[string]{"store": bagOf()},
			componentTypes: map[string]string{"store": "core/store"},
		},
		{
			name:           "SILENT: a render with no nodes at all",
			nodeOrder:      nil,
			declared:       NodeDeclaredProvides{},
			recipes:        map[string]*types.Recipe{},
			resolved:       map[string]types.OrderedMap[string]{},
			componentTypes: map[string]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			halt := providesContractHalt(
				t, tc.nodeOrder, tc.declared, tc.recipes, tc.resolved, tc.componentTypes,
			)
			if len(tc.want) == 0 {
				if halt != nil {
					t.Fatalf("must stay silent; raised %v", halt)
				}
				return
			}
			if halt == nil {
				t.Fatalf("must refuse; stayed silent")
			}
			if !stderrors.Is(halt, ErrProvidesContractUnfulfilled()) {
				t.Fatalf("halt must carry the condition; got %v", halt)
			}
			got := providesContractFindings(t, halt)
			if len(got) != len(tc.want) {
				t.Fatalf("want %d finding(s), got %d: %q", len(tc.want), len(got), got)
			}
			for i, want := range tc.want {
				for _, part := range strings.Split(want, "|") {
					if !strings.Contains(got[i], part) {
						t.Errorf("finding %d must carry %q; got %q", i, part, got[i])
					}
				}
			}
		})
	}
}

// TestProvidesContractRuleIsPositionedAtTheRecipesProvidesEntry asserts the
// finding is positioned at the entry the author adds to the Recipe's provides
// block, and that it carries the condition's own category.
//
// The position is what the axis was chosen for. The shortfall is an omission from
// the Recipe's provides block, so the document a reader opens is that Recipe and
// the key inside it is the entry to write — which is exactly the coordinate pair
// this subject kind carries.
func TestProvidesContractRuleIsPositionedAtTheRecipesProvidesEntry(t *testing.T) {
	t.Parallel()
	halt := providesContractHalt(t,
		[]string{"store"},
		NodeDeclaredProvides{"store": contractOf("endpoint", "dsn")},
		map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
		map[string]types.OrderedMap[string]{"store": bagOf("endpoint", "svc.local")},
		map[string]string{"store": "core/store"},
	)
	var ce *cerrors.CompilerError
	if !stderrors.As(halt, &ce) {
		t.Fatalf("halt is not a compiler diagnostic: %v", halt)
	}
	if ce.Category() != cerrors.CatalogError {
		t.Errorf("category = %s, want %s", ce.Category(), cerrors.CatalogError)
	}
	if ce.Source().File != "store-recipe" {
		t.Errorf("source file = %q, want the Recipe the shortfall is in", ce.Source().File)
	}
	if want := ".spec.provides.dsn"; ce.Source().Locator != want {
		t.Errorf("locator = %q, want %q", ce.Source().Locator, want)
	}
}

// TestProvidesContractRuleWithoutACallerPresentation records the one bound on this
// refusal, so the bound is a tested property rather than something a later reader
// has to rediscover.
//
// A caller that assembles a render's input without the graph presentation leaves
// the presentation chain unbuilt, and this reading is then not invoked at all — so
// a Recipe short of its contract renders for that caller. Every build path
// supplies the presentation. The row asserting the other half is beside it: a
// caller that DID supply one and still reaches a broken chain is refused, because
// there silence would be indistinguishable from approval.
func TestProvidesContractRuleWithoutACallerPresentation(t *testing.T) {
	t.Parallel()
	axes := declaredProvidesContract{
		nodeOrder: []string{"store"},
		declared:  NodeDeclaredProvides{"store": contractOf("dsn")},
		recipes:   map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
	}

	if err := runProvidesContractRule(nil, false, nil, axes); err != nil {
		t.Fatalf("a caller supplying no graph view leaves the reading uninvoked; got %v", err)
	}

	err := runProvidesContractRule(nil, true, nil, axes)
	if err == nil {
		t.Fatal("a caller view standing over a broken chain must be refused, not passed")
	}
	if stderrors.Is(err, ErrProvidesContractUnfulfilled()) {
		t.Errorf("the broken-arrangement refusal must not masquerade as the author's condition; got %v", err)
	}
	if !strings.Contains(err.Error(), "fault in the compiler") {
		t.Errorf("the refusal must attribute the fault away from the catalog; got %v", err)
	}
}

// TestProvidesContractAxisEnumeratesDeclaredKeysNotPublishedOnes pins the axis
// itself, because the axis is the part of this rule that a plausible
// simplification gets wrong.
//
// The framework's own provides-key enumeration yields what a Recipe published.
// This rule needs the opposite side of the contract, and a key the Recipe never
// published is absent from the published set by construction — so an axis taken
// from there would yield nothing for exactly the inputs this rule refuses. The
// assertion is that the axis yields a declared key the resolved bag does not hold.
func TestProvidesContractAxisEnumeratesDeclaredKeysNotPublishedOnes(t *testing.T) {
	t.Parallel()
	axes := declaredProvidesContract{
		nodeOrder: []string{"store"},
		declared:  NodeDeclaredProvides{"store": contractOf("endpoint", "dsn")},
		recipes:   map[string]*types.Recipe{"store": recipeNamed("store-recipe")},
	}
	got := axes.ProvidesKeys("store")
	if len(got) != 2 {
		t.Fatalf("the axis must yield every declared key; got %d: %v", len(got), got)
	}
	if got[0].Value != "dsn" || got[1].Value != "endpoint" {
		t.Errorf("the axis must yield declared keys sorted; got %q then %q", got[0].Value, got[1].Value)
	}
	for _, coordinate := range got {
		if coordinate.SourceFile != "store-recipe" {
			t.Errorf("key %q is positioned at %q, want the bound Recipe", coordinate.Value, coordinate.SourceFile)
		}
	}
	if nodes := axes.Nodes(); len(nodes) != 1 || nodes[0].ID != "store" {
		t.Errorf("the node axis must be the retention's own key set; got %v", nodes)
	}
}
