// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rules refusing a document that declares omit entries rendered by a provider
// that does not declare it honours them are declared once per document that can
// carry the list, and this file runs the declared values through the framework's
// own traversal — the same run function the render invokes — over a fixture.
//
// # Why the rules are driven through the traversal rather than the predicates
//
// A call straight into a predicate would prove the comparison and nothing about the
// axis it is invoked over. These rules cover three owners, and one of them is asked
// of an attachment only where it renders — a node's attachment set carries the
// attachments a boundary declares as well as the component's own — so a rule
// invoked over the wrong axis, or ending on the wrong scope, would leave refusal
// rows quiet or report one document twice. Driving the traversal puts both under
// test.
//
// # The view stub embeds the view interface, and the nil embedding is the point
//
// omitsFactStub embeds [constraint.DescribedView] and overrides only the accessors
// these rules read. The embedded interface is nil, so any other accessor panics if a
// predicate reaches for it, which turns "the rules read declared facts and nothing
// else" into a property this file enforces.

// omitsFactStub answers the facts the three omits rules read, keyed by each owner's
// own coordinates, and counts how often each owner's claim is read.
type omitsFactStub struct {
	constraint.DescribedView

	declares map[string]bool
	claims   map[string]bool
	resolved map[string]bool
	provider map[string]string
	scope    map[string]string

	// claimReads counts the claim reads per coordinate, shared across the copies
	// the traversal hands a value-receiver stub.
	claimReads map[string]int
}

func newOmitsFactStub() omitsFactStub {
	return omitsFactStub{
		declares:   map[string]bool{},
		claims:     map[string]bool{},
		resolved:   map[string]bool{},
		provider:   map[string]string{},
		scope:      map[string]string{},
		claimReads: map[string]int{},
	}
}

func (s omitsFactStub) claim(key string) (bool, bool) {
	s.claimReads[key]++
	return s.claims[key], s.resolved[key]
}

func (s omitsFactStub) NodeDeclaresOmits(node string) bool { return s.declares[factKey(node)] }

func (s omitsFactStub) ProviderHonoursOmits(node string) (bool, bool) {
	return s.claim(factKey(node))
}

func (s omitsFactStub) NodeRenderingProvider(node string) string { return s.provider[factKey(node)] }

func (s omitsFactStub) AttachmentExecutionScope(node, attachment string) string {
	return s.scope[factKey(node, attachment)]
}

func (s omitsFactStub) AttachmentDeclaresOmits(node, attachment string) bool {
	return s.declares[factKey(node, attachment)]
}

func (s omitsFactStub) AttachmentProviderHonoursOmits(node, attachment string) (bool, bool) {
	return s.claim(factKey(node, attachment))
}

func (s omitsFactStub) AttachmentRenderingProvider(node, attachment string) string {
	return s.provider[factKey(node, attachment)]
}

func (s omitsFactStub) BoundaryAttachmentExecutionScope(boundary, attachment string) string {
	return s.scope[factKey(boundary, attachment)]
}

func (s omitsFactStub) BoundaryAttachmentDeclaresOmits(boundary, attachment string) bool {
	return s.declares[factKey(boundary, attachment)]
}

func (s omitsFactStub) BoundaryAttachmentProviderHonoursOmits(boundary, attachment string) (bool, bool) {
	return s.claim(factKey(boundary, attachment))
}

func (s omitsFactStub) BoundaryAttachmentRenderingProvider(boundary, attachment string) string {
	return s.provider[factKey(boundary, attachment)]
}

// omitsNode is the one component every fixture below places, authored at the top
// level of its Aggregate.
var omitsNode = constraint.NodeInstance{
	ID: "app", SourceFile: "aggregate.yaml", AuthoredName: "app",
	SubsystemPath: "default", SynthesizedSubsystem: true,
}

// omitsOwner is one of the three documents that can carry an omits list, with the
// fixture that places exactly one of it, the coordinate its facts are keyed by, and
// the position and detail a refusal of it carries.
type omitsOwner struct {
	name     string
	sites    providerSiteFixture
	key      string
	file     string
	locator  string
	captions []string
}

func omitsOwners() []omitsOwner {
	return []omitsOwner{
		{
			name:     "a node's Recipe",
			sites:    providerSiteFixture{nodes: []constraint.NodeInstance{omitsNode}},
			key:      factKey("app"),
			file:     "aggregate.yaml",
			locator:  ".spec.components.app",
			captions: []string{`node "app"`},
		},
		{
			name: "an attachment's FeatureRecipe on a node",
			sites: providerSiteFixture{
				nodes:       []constraint.NodeInstance{omitsNode},
				attachments: map[string][]string{factKey("app"): {"sidecar"}},
			},
			key:      factKey("app", "sidecar"),
			file:     "aggregate.yaml",
			locator:  ".spec.components.app.features.sidecar",
			captions: []string{`node "app"`, `attachment "sidecar"`},
		},
		{
			name: "a boundary-scope attachment's FeatureRecipe",
			sites: providerSiteFixture{
				boundaries:     []constraint.AuthoredCoordinate{{Value: "main", SourceFile: "release.yaml"}},
				boundaryAttach: map[string][]string{factKey("main"): {"guard"}},
			},
			key:      factKey("main", "guard"),
			file:     "release.yaml",
			locator:  ".spec.boundaries.main.features.guard",
			captions: []string{`boundary "main"`, `attachment "guard"`},
		},
	}
}

// TestOmitsProviderRules_RefuseOnlyADeclaringDocumentWhoseProviderDoesNotClaimTheKey
// is the refusal grid, per owner: a document declaring omits or not, against a
// provider claiming the key, one not claiming it, and a binding that resolves to no
// provider at all. Only a declaring document whose resolved provider does not claim
// the key is refused; a document declaring nothing never has its claim read; and an
// unresolved binding is left to the render that reaches for the provider, so it is
// refused by nothing here.
func TestOmitsProviderRules_RefuseOnlyADeclaringDocumentWhoseProviderDoesNotClaimTheKey(t *testing.T) {
	t.Parallel()

	type provider struct {
		name              string
		claims, resolved  bool
		refusesDeclaring  bool
		readsWhenDeclared int
	}
	providers := []provider{
		{name: "claimed", claims: true, resolved: true, readsWhenDeclared: 1},
		{name: "not claimed", resolved: true, refusesDeclaring: true, readsWhenDeclared: 1},
		{name: "no provider resolves", readsWhenDeclared: 1},
	}

	for _, owner := range omitsOwners() {
		for _, p := range providers {
			for _, declares := range []bool{true, false} {
				name := owner.name + ", " + p.name
				if declares {
					name += ", declaring"
				} else {
					name += ", not declaring"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					view := newOmitsFactStub()
					view.declares[owner.key] = declares
					view.claims[owner.key] = p.claims
					view.resolved[owner.key] = p.resolved
					view.provider[owner.key] = "helm-chart"
					// The boundary owner is asked only of a FeatureRecipe that generates
					// once for its boundary, which is the one such an attachment renders as.
					if owner.key == factKey("main", "guard") {
						view.scope[owner.key] = executionScopeBoundary
					}

					err := runOmitsProviderRule(context.Background(), view, owner.sites)

					wantReads := 0
					if declares {
						wantReads = p.readsWhenDeclared
					}
					if got := view.claimReads[owner.key]; got != wantReads {
						t.Errorf("the claim was read %d time(s); want %d", got, wantReads)
					}
					if !declares || !p.refusesDeclaring {
						if err != nil {
							t.Fatalf("refused: %v", err)
						}
						return
					}
					assertOmitsRefusal(t, err, owner)
				})
			}
		}
	}
}

// assertOmitsRefusal pins one refusal to the condition's own identity and sentence,
// the provider it names bare, the owner's coordinates captioned, the remedy, and the
// position the owner was written at.
func assertOmitsRefusal(t *testing.T, err error, owner omitsOwner) {
	t.Helper()
	if !errors.Is(err, ErrUnsupportedOmits()) {
		t.Fatalf("the refusal does not carry %v: %v", ErrUnsupportedOmits(), err)
	}
	sentence, _ := cerrors.MessageOf(ErrUnsupportedOmits())
	for _, want := range append([]string{sentence + ` ("helm-chart"`, unsupportedOmitsAdvice}, owner.captions...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	var positioned *cerrors.CompilerError
	if !errors.As(err, &positioned) {
		t.Fatalf("the refusal carries no position: %v", err)
	}
	if got := positioned.Source(); got.File != owner.file || got.Locator != owner.locator {
		t.Errorf("the refusal is positioned at %q at %q; want %q at %q",
			got.File, got.Locator, owner.file, owner.locator)
	}
	if got := positioned.Category(); got != cerrors.CatalogError {
		t.Errorf("the refusal is filed under %q; want %q", got, cerrors.CatalogError)
	}
}

// TestOmitsProviderRules_AskEachFeatureRecipeWhereItRenders holds the partition
// between the two attachment rules. A boundary holding two components declares two
// attachments: one whose FeatureRecipe generates per component, which renders once
// for each of the two and so is refused once for each, and one whose FeatureRecipe
// generates once for the boundary, which renders there and so is refused once, at
// the boundary — never again for each component carrying it in its merged set.
func TestOmitsProviderRules_AskEachFeatureRecipeWhereItRenders(t *testing.T) {
	t.Parallel()

	cache := constraint.NodeInstance{
		ID: "cache", SourceFile: "aggregate.yaml", AuthoredName: "cache",
		SubsystemPath: "default", SynthesizedSubsystem: true,
	}
	sites := providerSiteFixture{
		nodes:      []constraint.NodeInstance{omitsNode, cache},
		boundaries: []constraint.AuthoredCoordinate{{Value: "main", SourceFile: "release.yaml"}},
		attachments: map[string][]string{
			factKey("app"):   {"per-component", "once"},
			factKey("cache"): {"per-component", "once"},
		},
		boundaryAttach: map[string][]string{factKey("main"): {"per-component", "once"}},
	}
	view := newOmitsFactStub()
	for _, key := range []string{
		factKey("app", "per-component"), factKey("cache", "per-component"), factKey("main", "per-component"),
		factKey("app", "once"), factKey("cache", "once"), factKey("main", "once"),
	} {
		view.declares[key] = true
		view.resolved[key] = true
		view.provider[key] = "helm-chart"
	}
	for _, key := range []string{factKey("app", "once"), factKey("cache", "once"), factKey("main", "once")} {
		view.scope[key] = executionScopeBoundary
	}

	err := runOmitsProviderRule(context.Background(), view, sites)
	if !errors.Is(err, ErrUnsupportedOmits()) {
		t.Fatalf("got %v, want the refusal of a provider that does not support omits", err)
	}
	refusals := map[string]int{}
	for _, cause := range joinedCauses(err) {
		var positioned *cerrors.CompilerError
		if errors.As(cause, &positioned) {
			refusals[positioned.Source().Locator]++
		}
	}
	want := map[string]int{
		".spec.components.app.features.per-component":   1,
		".spec.components.cache.features.per-component": 1,
		".spec.boundaries.main.features.once":           1,
	}
	if len(refusals) != len(want) {
		t.Errorf("refused at %v; want %v", refusals, want)
	}
	for locator, n := range want {
		if refusals[locator] != n {
			t.Errorf("refused %d time(s) at %s; want %d (all refusals: %v)", refusals[locator], locator, n, refusals)
		}
	}
	// The FeatureRecipe that generates per component is not asked at the boundary,
	// and the one that generates once is not asked per component.
	for _, key := range []string{factKey("main", "per-component"), factKey("app", "once"), factKey("cache", "once")} {
		if view.claimReads[key] != 0 {
			t.Errorf("the claim at %q was read %d time(s); it renders nowhere it would be asked there",
				strings.ReplaceAll(key, "\x00", "/"), view.claimReads[key])
		}
	}
}

// joinedCauses returns the findings several refusals were joined under, or the one
// refusal itself.
func joinedCauses(err error) []error {
	var joined interface{ Unwrap() []error }
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		if errors.As(cur, &joined) {
			return joined.Unwrap()
		}
	}
	return []error{err}
}

// TestOmitsProviderRules_ReadTheBuildsOwnFactsThroughTheDescribedView drives the
// rules over the presentation and the enumeration a render builds from its own
// products — a graph, the Recipe bound to a node, the FeatureRecipe an attachment
// binds, and an engine factory — rather than over a stub, so the facts the rules
// read are the ones the render's own bindings produce. A Recipe and an attachment's
// FeatureRecipe each declaring entries are refused under a provider that declines
// them, admitted under one that claims them, and left alone where the binding
// resolves to no provider.
func TestOmitsProviderRules_ReadTheBuildsOwnFactsThroughTheDescribedView(t *testing.T) {
	t.Parallel()

	const (
		node       = "app"
		attachment = "sidecar"
		feature    = "core/sidecar"
	)
	entries := []types.OmitEntry{{Kind: "Secret", Name: "token"}}

	for _, tc := range []struct {
		name                  string
		recipeCapability      string
		recipeDeclares        bool
		attachmentCapability  string
		attachmentDeclares    bool
		wantLocator, provider string
	}{
		{name: "a Recipe under a provider declining the key", recipeCapability: "terraform", recipeDeclares: true,
			wantLocator: ".spec.components.app", provider: "terraform"},
		{name: "a Recipe under a provider claiming the key", recipeCapability: "helm", recipeDeclares: true},
		{name: "a Recipe whose binding resolves to no provider", recipeCapability: "nowhere", recipeDeclares: true},
		{name: "an attachment's FeatureRecipe under a provider declining the key", recipeCapability: "helm",
			attachmentCapability: "terraform", attachmentDeclares: true,
			wantLocator: ".spec.components.app.features.sidecar", provider: "terraform"},
		{name: "an attachment's FeatureRecipe under a provider claiming the key", recipeCapability: "terraform",
			attachmentCapability: "helm", attachmentDeclares: true},
		{name: "an attachment's FeatureRecipe declaring nothing, under a provider declining the key",
			recipeCapability: "helm", attachmentCapability: "terraform"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reads := 0
			factory := func(capability string) (Provider, error) {
				switch capability {
				case "helm":
					return omitsClaimEngine{claims: true, reads: &reads}, nil
				case "terraform":
					return omitsClaimEngine{reads: &reads}, nil
				}
				return nil, errors.New("no provider serves " + capability)
			}

			recipe := &types.Recipe{}
			recipe.Spec.Capability = tc.recipeCapability
			if tc.recipeDeclares {
				recipe.Spec.Omits = entries
			}
			fr := &types.FeatureRecipe{}
			fr.Spec.Capability = tc.attachmentCapability
			if tc.attachmentDeclares {
				fr.Spec.Omits = entries
			}

			graph := &types.IRGraph{}
			graph.Spec.Nodes.Set(node, types.IRNode{})
			var attachments []string
			if tc.attachmentCapability != "" {
				bound := types.OrderedMap[types.IRFeatureEntry]{}
				bound.Set(attachment, types.IRFeatureEntry{Type: feature})
				graph.Spec.FeatureValuesMapping.Set(node, bound)
				attachments = []string{attachment}
			}
			recipes := map[string]*types.Recipe{node: recipe}
			lookup := RecipeLookup{
				FeatureRecipe: func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
					return fr, instanceName == attachment && featureType == feature
				},
			}

			view := newDescribedView(
				context.Background(), graph, stubGraphFacts{},
				recipes, lookup, nil, newStepDescriptorMemo(), nil, factory,
				NodeRenderedNames{}, nil, nil, nil,
			)
			if view == nil {
				t.Fatal("the view was not built; there is nothing for the rules to read")
			}
			axes := newDescribedStageCoordinates(
				graph, localsAxis{nodes: []string{node}, attachments: attachments}, recipes, lookup, nil,
			)
			if axes == nil {
				t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
			}

			err := runOmitsProviderRule(context.Background(), view, axes)
			if tc.wantLocator == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrUnsupportedOmits()) {
				t.Fatalf("got %v, want the refusal of a provider that does not support omits", err)
			}
			var positioned *cerrors.CompilerError
			if !errors.As(err, &positioned) {
				t.Fatalf("the refusal carries no position: %v", err)
			}
			if got := positioned.Source(); got.File != "aggregate.yaml" || got.Locator != tc.wantLocator {
				t.Errorf("the refusal is positioned at %q at %q; want %q at %q",
					got.File, got.Locator, "aggregate.yaml", tc.wantLocator)
			}
			if !strings.Contains(err.Error(), `("`+tc.provider+`", node "app"`) {
				t.Errorf("the refusal does not name provider %q and the node: %v", tc.provider, err)
			}
			if reads == 0 {
				t.Errorf("the refusal was reached without the engine's own claim being read")
			}
		})
	}
}

// TestDescribedViewOmitsFacts_ReadTheProvidersOwnClaimAndTheAuthoredList holds the
// facts the rules read to their sources: the claim is read through the engine the
// render's own factory resolves, from the engine's own report, and a binding the
// factory cannot resolve answers that no provider resolved; whether a document
// declares entries is read from the list it wrote.
func TestDescribedViewOmitsFacts_ReadTheProvidersOwnClaimAndTheAuthoredList(t *testing.T) {
	t.Parallel()

	reads := 0
	engines := map[string]Provider{
		"helm":       omitsClaimEngine{claims: true, reads: &reads},
		"terraform":  omitsClaimEngine{reads: &reads},
		"helm-chart": claimlessEngine{},
	}
	declaring := func(capability string) *types.Recipe {
		r := &types.Recipe{}
		r.Spec.Capability = capability
		r.Spec.Omits = []types.OmitEntry{{Kind: "Secret", Name: "token"}}
		return r
	}
	silent := &types.Recipe{}
	silent.Spec.Capability = "helm"
	view := &describedView{
		recipes: map[string]*types.Recipe{
			"claims": declaring("helm"), "denies": declaring("terraform"),
			"says-nothing": declaring("helm-chart"), "unresolved": declaring("nowhere"),
			"declares-none": silent,
		},
		engineFactory: func(capability string) (Provider, error) {
			if e, ok := engines[capability]; ok {
				return e, nil
			}
			return nil, errors.New("no such provider")
		},
	}

	for _, tc := range []struct {
		node                       string
		declares, honours, resolve bool
	}{
		{node: "claims", declares: true, honours: true, resolve: true},
		{node: "denies", declares: true, resolve: true},
		{node: "says-nothing", declares: true, resolve: true},
		{node: "unresolved", declares: true},
		{node: "declares-none", honours: true, resolve: true},
		{node: "no-recipe"},
	} {
		if got := view.NodeDeclaresOmits(tc.node); got != tc.declares {
			t.Errorf("%s: declares %v; want %v", tc.node, got, tc.declares)
		}
		honours, resolved := view.ProviderHonoursOmits(tc.node)
		if honours != tc.honours || resolved != tc.resolve {
			t.Errorf("%s: claim (%v, %v); want (%v, %v)", tc.node, honours, resolved, tc.honours, tc.resolve)
		}
	}
	// Three of the rows above resolve to an engine reporting the claim, and each was
	// asked through that report once.
	if reads != 3 {
		t.Errorf("the engines' own report was read %d time(s); want 3", reads)
	}
}
