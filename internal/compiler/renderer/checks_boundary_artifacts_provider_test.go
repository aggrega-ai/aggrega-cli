// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The rule refusing a Recipe that declares files for its boundary's shared output
// rendered by a provider that does not declare it accepts them is run here through
// the framework's own traversal — the run function the render invokes — over a
// fixture, so the axis it is invoked over is under test as well as the predicate.
//
// The view stub embeds [constraint.DescribedView] and overrides only the accessors
// the rule reads. The embedded interface is nil, so any other accessor panics if the
// predicate reaches for it.

// boundaryArtifactsFactStub answers the facts the rule reads, keyed by node, and
// counts how often each node's claim is read.
type boundaryArtifactsFactStub struct {
	constraint.DescribedView

	declares map[string]bool
	claims   map[string]bool
	resolved map[string]bool
	provider map[string]string

	// claimReads counts the claim reads per node, shared across the copies the
	// traversal hands a value-receiver stub.
	claimReads map[string]int
}

func newBoundaryArtifactsFactStub() boundaryArtifactsFactStub {
	return boundaryArtifactsFactStub{
		declares:   map[string]bool{},
		claims:     map[string]bool{},
		resolved:   map[string]bool{},
		provider:   map[string]string{},
		claimReads: map[string]int{},
	}
}

func (s boundaryArtifactsFactStub) RecipeBoundaryArtifactsDeclared(node, block string) bool {
	return block == recipeFieldBoundary && s.declares[node]
}

func (s boundaryArtifactsFactStub) ProviderAcceptsBoundaryArtifacts(node string) (bool, bool) {
	s.claimReads[node]++
	return s.claims[node], s.resolved[node]
}

func (s boundaryArtifactsFactStub) NodeRenderingProvider(node string) string { return s.provider[node] }

// boundaryBlockSites is the node axis and the position each node's Recipe writes
// its boundary block at, each position in the Recipe the node binds.
type boundaryBlockSites struct {
	nodes  []constraint.NodeInstance
	recipe map[string]string
}

func (f boundaryBlockSites) Nodes() []constraint.NodeInstance { return f.nodes }

func (f boundaryBlockSites) RecipeBoundaryBlocks(node string) []constraint.AuthoredCoordinate {
	file, ok := f.recipe[node]
	if !ok {
		return nil
	}
	return []constraint.AuthoredCoordinate{{Value: recipeFieldBoundary, SourceFile: file}}
}

// boundaryBlockNode is one component a fixture below places.
func boundaryBlockNode(id string) constraint.NodeInstance {
	return constraint.NodeInstance{
		ID: id, SourceFile: "aggregate.yaml", AuthoredName: id,
		SubsystemPath: "default", SynthesizedSubsystem: true,
	}
}

// TestBoundaryArtifactsProviderRule_RefusesOnlyADeclaringRecipeWhoseProviderDoesNotClaimTheFiles
// is the refusal grid over every pairing of a Recipe declaring the block or not
// with a provider claiming the files, one declining them, and a binding that
// resolves to no provider at all. Only a declaring Recipe whose resolved provider
// declines is refused; a Recipe declaring nothing never has its provider's claim
// read; and an unresolved binding is left to the render that reaches for the
// provider, so nothing here refuses it.
func TestBoundaryArtifactsProviderRule_RefusesOnlyADeclaringRecipeWhoseProviderDoesNotClaimTheFiles(t *testing.T) {
	t.Parallel()

	providers := []struct {
		name             string
		claims, resolved bool
		refusesDeclaring bool
	}{
		{name: "claiming", claims: true, resolved: true},
		{name: "declining", resolved: true, refusesDeclaring: true},
		{name: "no provider resolves"},
	}
	for _, p := range providers {
		for _, declares := range []bool{true, false} {
			name := p.name + ", not declaring"
			if declares {
				name = p.name + ", declaring"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				view := newBoundaryArtifactsFactStub()
				view.declares["app"] = declares
				view.claims["app"] = p.claims
				view.resolved["app"] = p.resolved
				view.provider["app"] = "compose"
				sites := boundaryBlockSites{
					nodes:  []constraint.NodeInstance{boundaryBlockNode("app")},
					recipe: map[string]string{"app": "server"},
				}

				err := runBoundaryArtifactsProviderRule(context.Background(), view, sites)

				wantReads := 0
				if declares {
					wantReads = 1
				}
				if got := view.claimReads["app"]; got != wantReads {
					t.Errorf("the claim was read %d time(s); want %d", got, wantReads)
				}
				if !declares || !p.refusesDeclaring {
					if err != nil {
						t.Fatalf("refused: %v", err)
					}
					return
				}
				assertBoundaryArtifactsRefusal(t, err, "server", "app")
			})
		}
	}
}

// TestBoundaryArtifactsProviderRule_RefusesEveryOffendingNodeAtItsOwnRecipe holds
// the rule's reach across nodes: two components bound to two Recipes that both
// declare the block, both rendered by a declining provider, are each refused, each
// at its own Recipe's key and naming its own node, and a third declaring nothing is
// not asked.
func TestBoundaryArtifactsProviderRule_RefusesEveryOffendingNodeAtItsOwnRecipe(t *testing.T) {
	t.Parallel()

	view := newBoundaryArtifactsFactStub()
	for _, node := range []string{"api", "web", "worker"} {
		view.resolved[node] = true
		view.provider[node] = "compose"
	}
	view.declares["api"] = true
	view.declares["web"] = true
	sites := boundaryBlockSites{
		nodes: []constraint.NodeInstance{boundaryBlockNode("api"), boundaryBlockNode("web"), boundaryBlockNode("worker")},
		recipe: map[string]string{
			"api": "api-recipe", "web": "web-recipe", "worker": "worker-recipe",
		},
	}

	err := runBoundaryArtifactsProviderRule(context.Background(), view, sites)
	if !errors.Is(err, ErrUnsupportedBoundaryArtifacts()) {
		t.Fatalf("got %v, want the refusal of a provider that does not support boundary artifacts", err)
	}
	positions := map[string]bool{}
	for _, cause := range joinedCauses(err) {
		var positioned *cerrors.CompilerError
		if errors.As(cause, &positioned) {
			positions[positioned.Source().File+" at "+positioned.Source().Locator] = true
		}
	}
	for _, want := range []string{"api-recipe at .spec.boundary", "web-recipe at .spec.boundary"} {
		if !positions[want] {
			t.Errorf("no refusal positioned %s; refused at %v", want, positions)
		}
	}
	if len(positions) != 2 {
		t.Errorf("refused at %v; want exactly the two declaring Recipes", positions)
	}
	if view.claimReads["worker"] != 0 {
		t.Errorf("the claim of a node whose Recipe declares no block was read %d time(s)", view.claimReads["worker"])
	}
}

// assertBoundaryArtifactsRefusal pins one refusal to the condition's identity and
// sentence, the provider named bare, the node captioned, the remedy, and the
// position at the block in the Recipe.
func assertBoundaryArtifactsRefusal(t *testing.T, err error, recipe, node string) {
	t.Helper()
	if !errors.Is(err, ErrUnsupportedBoundaryArtifacts()) {
		t.Fatalf("the refusal does not carry %v: %v", ErrUnsupportedBoundaryArtifacts(), err)
	}
	sentence, _ := cerrors.MessageOf(ErrUnsupportedBoundaryArtifacts())
	for _, want := range []string{sentence + ` ("compose"`, `node "` + node + `"`, "remove the boundary block"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	var positioned *cerrors.CompilerError
	if !errors.As(err, &positioned) {
		t.Fatalf("the refusal carries no position: %v", err)
	}
	if got := positioned.Source(); got.File != recipe || got.Locator != ".spec.boundary" {
		t.Errorf("the refusal is positioned at %q at %q; want %q at %q", got.File, got.Locator, recipe, ".spec.boundary")
	}
	if got := positioned.Category(); got != cerrors.CatalogError {
		t.Errorf("the refusal is filed under %q; want %q", got, cerrors.CatalogError)
	}
}
