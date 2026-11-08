// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// fakeExtendsSource serves Component and Feature documents by the reference a
// closure entity carries, as the lazy view's closure reads do.
type fakeExtendsSource struct {
	components map[string]types.Component
	features   map[string]types.Feature
}

func (f fakeExtendsSource) ClosureComponent(_ context.Context, ref closure.ResolvedRef) (types.Component, bool) {
	if !ref.ReadableAs(closure.KindComponent) {
		return types.Component{}, false
	}
	c, ok := f.components[ref.URI]
	return c, ok
}

// ExtendsChainBound hands the descent the bound the extends rules enforce, as the
// lazy view does, so a test of how far the descent follows a chain reads that one
// value.
func (fakeExtendsSource) ExtendsChainBound() int { return validator.ExtendsChainBound() }

func (f fakeExtendsSource) ClosureFeature(_ context.Context, ref closure.ResolvedRef) (types.Feature, bool) {
	if !ref.ReadableAs(closure.KindFeature) {
		return types.Feature{}, false
	}
	feat, ok := f.features[ref.URI]
	return feat, ok
}

func extendingComponent(name, version, extends string) types.Component {
	return types.Component{
		Metadata: types.ComponentMetadata{Name: name, Version: version},
		Spec:     types.ComponentSpec{Extends: extends},
	}
}

func extendingFeature(name, version, extends string) types.Feature {
	return types.Feature{
		Metadata: types.FeatureMetadata{Name: name, Version: version},
		Spec:     types.FeatureSpec{Extends: extends},
	}
}

// extendsRow is what a test reads back of one closure entity: its identity and the
// sites that reference it.
type extendsRow struct {
	Kind, URI string
	From      []string
}

func extendsRows(cl closure.Closure) []extendsRow {
	rows := make([]extendsRow, 0, len(cl.Entities))
	for _, e := range cl.Entities {
		rows = append(rows, extendsRow{Kind: e.Kind(), URI: e.URI, From: e.ReferencedFrom})
	}
	return rows
}

// TestDescendExtendsBasesPinsEveryBaseAlongTheChain drives the descent over
// closures holding an extending entity and reads back the entities it adds: every
// base along every chain, under the kind of the entity that names it, referenced
// from the extending entity's `spec.extends`.
func TestDescendExtendsBasesPinsEveryBaseAlongTheChain(t *testing.T) {
	ok := func(uri string) closure.Resolution { return resolutionOK("1.0.0", "sha256:"+uri, "tier-3-pull") }
	cases := []struct {
		name    string
		start   []closure.ResolvedRef
		source  fakeExtendsSource
		resolve []string
		want    []extendsRow
	}{
		{
			name:  "a Component extending a registry base pins the base and its own base",
			start: []closure.ResolvedRef{{URI: "local/db:0.1.0", KindHint: closure.KindComponent, ReferencedFrom: []string{"aggregate.spec.components.db.type"}}},
			source: fakeExtendsSource{components: map[string]types.Component{
				"local/db:0.1.0":      extendingComponent("local/db", "0.1.0", "core/db:1.0.0"),
				"core/db:1.0.0":       extendingComponent("core/db", "1.0.0", "core/base-db:1.0.0"),
				"core/base-db:1.0.0":  extendingComponent("core/base-db", "1.0.0", ""),
				"unreached/db:1.0.0":  extendingComponent("unreached/db", "1.0.0", ""),
				"unreached/x:1.0.0":   extendingComponent("unreached/x", "1.0.0", "unreached/db:1.0.0"),
				"core/never-read:1.0": extendingComponent("core/never-read", "1.0", ""),
			}},
			resolve: []string{"core/db:1.0.0", "core/base-db:1.0.0"},
			want: []extendsRow{
				{Kind: closure.KindComponent, URI: "core/base-db:1.0.0", From: []string{"component(core/db:1.0.0).spec.extends"}},
				{Kind: closure.KindComponent, URI: "core/db:1.0.0", From: []string{"component(local/db:0.1.0).spec.extends"}},
				{Kind: closure.KindComponent, URI: "local/db:0.1.0", From: []string{"aggregate.spec.components.db.type"}},
			},
		},
		{
			name:  "a Feature extending a base pins the base as a Feature",
			start: []closure.ResolvedRef{{URI: "local/queue:1.0.0", KindHint: closure.KindFeature, ReferencedFrom: []string{"aggregate.spec.components.api.features.q.type"}}},
			source: fakeExtendsSource{features: map[string]types.Feature{
				"local/queue:1.0.0": extendingFeature("local/queue", "1.0.0", "core/queue:1.0.0"),
				"core/queue:1.0.0":  extendingFeature("core/queue", "1.0.0", ""),
			}},
			resolve: []string{"core/queue:1.0.0"},
			want: []extendsRow{
				{Kind: closure.KindFeature, URI: "core/queue:1.0.0", From: []string{"feature(local/queue:1.0.0).spec.extends"}},
				{Kind: closure.KindFeature, URI: "local/queue:1.0.0", From: []string{"aggregate.spec.components.api.features.q.type"}},
			},
		},
		{
			// The held row is the walk's own resolution, so it carries the digest the
			// walk recorded, and the lookup is given nothing for the base: the descent
			// must keep that row and add its site without asking for the base again.
			name: "a base the closure already holds is recorded once, with the extending entity as one more site",
			start: []closure.ResolvedRef{
				{URI: "core/db:1.0.0", KindHint: closure.KindComponent, Digest: "sha256:core/db:1.0.0", ReferencedFrom: []string{"manifest.spec.mappings.components.type.core/db:1.0.0"}},
				{URI: "local/db:0.1.0", KindHint: closure.KindComponent, ReferencedFrom: []string{"aggregate.spec.components.db.type"}},
			},
			source: fakeExtendsSource{components: map[string]types.Component{
				"local/db:0.1.0": extendingComponent("local/db", "0.1.0", "core/db:1.0.0"),
				"core/db:1.0.0":  extendingComponent("core/db", "1.0.0", ""),
			}},
			resolve: nil,
			want: []extendsRow{
				{Kind: closure.KindComponent, URI: "core/db:1.0.0", From: []string{"component(local/db:0.1.0).spec.extends", "manifest.spec.mappings.components.type.core/db:1.0.0"}},
				{Kind: closure.KindComponent, URI: "local/db:0.1.0", From: []string{"aggregate.spec.components.db.type"}},
			},
		},
		{
			name: "one entity met under two spellings records one site",
			start: []closure.ResolvedRef{
				{URI: "local/db", KindHint: closure.KindComponent, ReferencedFrom: []string{"aggregate.spec.components.db.type"}},
				{URI: "local/db:0.1.0", KindHint: closure.KindComponent, ReferencedFrom: []string{"manifest.spec.mappings.components.type.local/db:0.1.0"}},
			},
			source: fakeExtendsSource{components: map[string]types.Component{
				"local/db":       extendingComponent("local/db", "0.1.0", "core/db:1.0.0"),
				"local/db:0.1.0": extendingComponent("local/db", "0.1.0", "core/db:1.0.0"),
				"core/db:1.0.0":  extendingComponent("core/db", "1.0.0", ""),
			}},
			resolve: []string{"core/db:1.0.0"},
			want: []extendsRow{
				{Kind: closure.KindComponent, URI: "core/db:1.0.0", From: []string{"component(local/db:0.1.0).spec.extends"}},
				{Kind: closure.KindComponent, URI: "local/db", From: []string{"aggregate.spec.components.db.type"}},
				{Kind: closure.KindComponent, URI: "local/db:0.1.0", From: []string{"manifest.spec.mappings.components.type.local/db:0.1.0"}},
			},
		},
		{
			name:  "a chain that loops back ends at the first reference it meets again",
			start: []closure.ResolvedRef{{URI: "core/a:1.0.0", KindHint: closure.KindComponent, ReferencedFrom: []string{"aggregate.spec.components.a.type"}}},
			source: fakeExtendsSource{components: map[string]types.Component{
				"core/a:1.0.0": extendingComponent("core/a", "1.0.0", "core/b:1.0.0"),
				"core/b:1.0.0": extendingComponent("core/b", "1.0.0", "core/a:1.0.0"),
			}},
			resolve: []string{"core/a:1.0.0", "core/b:1.0.0"},
			want: []extendsRow{
				{Kind: closure.KindComponent, URI: "core/a:1.0.0", From: []string{"aggregate.spec.components.a.type", "component(core/b:1.0.0).spec.extends"}},
				{Kind: closure.KindComponent, URI: "core/b:1.0.0", From: []string{"component(core/a:1.0.0).spec.extends"}},
			},
		},
		{
			name:  "a Recipe reference is never read as a Component",
			start: []closure.ResolvedRef{{URI: "helm/db:1.0.0", KindHint: closure.KindRecipe, ReferencedFrom: []string{"manifest.spec.mappings.components.type.local/db:0.1.0"}}},
			source: fakeExtendsSource{components: map[string]types.Component{
				"helm/db:1.0.0": extendingComponent("helm/db", "1.0.0", "core/db:1.0.0"),
			}},
			resolve: []string{"core/db:1.0.0"},
			want: []extendsRow{
				{Kind: closure.KindRecipe, URI: "helm/db:1.0.0", From: []string{"manifest.spec.mappings.components.type.local/db:0.1.0"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := &fakeLookup{entities: map[string]closure.Resolution{}}
			for _, uri := range tc.resolve {
				lookup.entities[uri] = ok(uri)
			}
			cl := closure.Closure{Entities: append([]closure.ResolvedRef(nil), tc.start...)}
			if err := closure.DescendExtendsBases(context.Background(), &cl, tc.source, lookup); err != nil {
				t.Fatalf("DescendExtendsBases: %v", err)
			}
			if got := extendsRows(cl); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("entities =\n\t%+v\nwant\n\t%+v", got, tc.want)
			}
			for _, e := range cl.Entities {
				if e.URI == "core/db:1.0.0" && e.Digest != "sha256:core/db:1.0.0" {
					t.Errorf("the base is pinned at digest %q; want the digest its resolution reported", e.Digest)
				}
			}
		})
	}
}

// TestDescendExtendsBasesFollowsAChainAsFarAsTheExtendsRulesAdmit drives the
// descent over a chain two links longer than the extends rules admit and reads
// back how far it went: a chain may reach validator.ExtendsChainBound() entities
// counting the one it starts from, so the descent pins exactly that many and asks
// for nothing beyond — the rules refuse the chain wherever it is merged.
func TestDescendExtendsBasesFollowsAChainAsFarAsTheExtendsRulesAdmit(t *testing.T) {
	bound := validator.ExtendsChainBound()
	name := func(i int) string { return fmt.Sprintf("core/link-%02d:1.0.0", i) }
	source := fakeExtendsSource{components: map[string]types.Component{}}
	lookup := &fakeLookup{entities: map[string]closure.Resolution{}}
	for i := 0; i <= bound+1; i++ {
		extends := name(i + 1)
		source.components[name(i)] = extendingComponent(strings.TrimSuffix(name(i), ":1.0.0"), "1.0.0", extends)
		lookup.entities[name(i)] = resolutionOK("1.0.0", "sha256:"+name(i), "tier-3-pull")
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: name(0), KindHint: closure.KindComponent, ReferencedFrom: []string{"aggregate.spec.components.c.type"}},
	}}
	if err := closure.DescendExtendsBases(context.Background(), &cl, source, lookup); err != nil {
		t.Fatalf("DescendExtendsBases: %v", err)
	}
	var got, want []string
	for _, e := range cl.Entities {
		got = append(got, e.URI)
	}
	for i := 0; i < bound; i++ {
		want = append(want, name(i))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the descent pinned %q; want the %d entities a chain may reach, %q", got, bound, want)
	}
}

// TestDescendExtendsBasesLeavesABaseNoSourceHoldsToTheChainWalk pins the two ways
// a base can fail to resolve. A base no source holds is left out, with no error:
// the extends resolution that merges the chain is where a missing parent is
// reported, in one wording on every surface. A lookup that fails — a registry that
// cannot be reached, say — halts the descent, naming the base, as it halts the walk.
func TestDescendExtendsBasesLeavesABaseNoSourceHoldsToTheChainWalk(t *testing.T) {
	start := func() closure.Closure {
		return closure.Closure{Entities: []closure.ResolvedRef{
			{URI: "local/db:0.1.0", KindHint: closure.KindComponent, ReferencedFrom: []string{"aggregate.spec.components.db.type"}},
		}}
	}
	source := fakeExtendsSource{components: map[string]types.Component{
		"local/db:0.1.0": extendingComponent("local/db", "0.1.0", "core/absent:1.0.0"),
	}}

	t.Run("a base no source holds is left out", func(t *testing.T) {
		cl := start()
		if err := closure.DescendExtendsBases(context.Background(), &cl, source, &fakeLookup{entities: map[string]closure.Resolution{}}); err != nil {
			t.Fatalf("a base no source holds is the extends resolution's to report; the descent refused it: %v", err)
		}
		want := []extendsRow{{Kind: closure.KindComponent, URI: "local/db:0.1.0", From: []string{"aggregate.spec.components.db.type"}}}
		if got := extendsRows(cl); !reflect.DeepEqual(got, want) {
			t.Errorf("entities = %+v; want the closure unchanged, %+v", got, want)
		}
	})

	t.Run("a lookup that fails halts naming the base", func(t *testing.T) {
		cl := start()
		unreachable := errors.New("registry unreachable")
		err := closure.DescendExtendsBases(context.Background(), &cl, source, failingLookup{fakeLookup: &fakeLookup{}, err: unreachable})
		if !errors.Is(err, unreachable) {
			t.Fatalf("a failing lookup must halt the descent with its failure; got %v", err)
		}
		var unresolved *closure.UnresolvedEntityError
		if !errors.As(err, &unresolved) || unresolved.URI != "core/absent:1.0.0" || unresolved.KindHint != closure.KindComponent {
			t.Errorf("the halt must carry the base and its kind; got %#v", unresolved)
		}
		if !strings.Contains(err.Error(), `"core/absent:1.0.0"`) {
			t.Errorf("halt %q does not name the base", err.Error())
		}
	})
}

// failingLookup answers every catalog-entity resolution with err.
type failingLookup struct {
	*fakeLookup
	err error
}

func (f failingLookup) ResolveCatalogEntity(context.Context, string, string) (closure.Resolution, error) {
	return closure.Resolution{}, f.err
}

// TestAugmentClosureDescendsExtendsBasesBeforeTheLibraries pins that the one fold
// every closure computation runs reaches the descent: a base a Component extends is
// in the closure AugmentClosure returns.
func TestAugmentClosureDescendsExtendsBasesBeforeTheLibraries(t *testing.T) {
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "local/db:0.1.0", KindHint: closure.KindComponent, ReferencedFrom: []string{"aggregate.spec.components.db.type"}},
	}}
	src := augmentExtendsSource{fakeExtendsSource{components: map[string]types.Component{
		"local/db:0.1.0": extendingComponent("local/db", "0.1.0", "core/db:1.0.0"),
		"core/db:1.0.0":  extendingComponent("core/db", "1.0.0", ""),
	}}}
	lookup := &fakeLookup{entities: map[string]closure.Resolution{"core/db:1.0.0": resolutionOK("1.0.0", "sha256:base", "tier-3-pull")}}
	if err := closure.AugmentClosure(context.Background(), &cl, src, lookup); err != nil {
		t.Fatalf("AugmentClosure: %v", err)
	}
	var found bool
	for _, e := range cl.Entities {
		if e.Key() == (closure.EntityKey{Kind: closure.KindComponent, URI: "core/db:1.0.0"}) {
			found = true
		}
	}
	if !found {
		t.Errorf("AugmentClosure returned %+v; want the base core/db:1.0.0 pinned as a Component", extendsRows(cl))
	}
}

// augmentExtendsSource is a LibrarySource that serves Components and Features from
// a fakeExtendsSource and holds no library, so AugmentClosure's library discovery
// finds nothing to resolve.
type augmentExtendsSource struct{ fakeExtendsSource }

func (augmentExtendsSource) SchemaLibrary(string) (types.SchemaLibrary, bool) {
	return types.SchemaLibrary{}, false
}
func (augmentExtendsSource) RecipeByURI(string) (types.Recipe, bool) { return types.Recipe{}, false }
func (augmentExtendsSource) FeatureRecipeByURI(string) (types.FeatureRecipe, bool) {
	return types.FeatureRecipe{}, false
}
func (augmentExtendsSource) MappingLibrary(string) (types.MappingLibrary, bool) {
	return types.MappingLibrary{}, false
}
func (augmentExtendsSource) WorkloadLibrary(string) (types.WorkloadLibrary, bool) {
	return types.WorkloadLibrary{}, false
}
func (augmentExtendsSource) ArtifactLibrary(string) (types.ArtifactLibrary, bool) {
	return types.ArtifactLibrary{}, false
}
func (augmentExtendsSource) HoldResolvedLibrary(closure.LibraryAxis, closure.ResolvedLibrary) {}
