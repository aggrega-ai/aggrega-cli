// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rule over the two providers a connection's ends render through is declared
// over what every plugin a render reaches reports about itself, so the render
// entry point is where it is invoked and this file drives it from there.
//
// # Why the render entry point rather than the predicate
//
// The unit rows beside this file drive the rule through the framework's traversal
// over a fact stub, which proves the predicate, the axis and the equivalence with
// the classifier the link pass still reads. What they cannot prove is that a
// BUILD reaches it: the presentation chain, the connection enumeration and the
// engine resolution are three separate things the render has to hold at the same
// seam, and a build missing any of them would carry the fallback silently.
//
// # The capability claim here is a real guest's, not a stub's
//
// Both ends render through real wasm engines over compiled test guests, and the
// linking claim is read off one of them by the same seam the link pass reads it
// through. The link guest reports artifacts-standalone support, so the end bound
// to it renders an artifacts-only Recipe; the other end renders a synthesized
// Recipe through the embedded provider guest. That pairing is what makes the row
// end-to-end rather than a second stub in a longer harness.

const (
	// crossProviderConsumer is the connection's consuming end, bound to the guest
	// that advertises cross-boundary linking.
	crossProviderConsumer = "gateway"

	// crossProviderTarget is the far end, bound to a provider that advertises no
	// linking. It is the "at least one advertises" gate: one claim is enough.
	crossProviderTarget = "api"
)

// crossProviderGraphFacts stands in for the presentation the graph finalisation
// produces, which the render embeds beneath the presentations built over it.
//
// It is supplied because the presentation chain is cumulative and refuses to
// assemble with a hole in it: the render publishes no plugin-report presentation
// unless the one it extends exists, and this rule reads two facts from below its
// own stage. Most fixtures in this package predate the declared render rules and
// omit it, which is exactly how a migrated reading becomes a silent pass — so it
// is supplied here rather than left to be discovered.
//
// It answers the two accessors this rule reads from below the plugin-report
// stage: the node a connection resolves to, and whether a name is in the
// flattened set. Every other accessor panics on the nil embedding rather than
// answering a zero value, which makes "reads declared facts and nothing else" a
// property this file enforces.
type crossProviderGraphFacts struct {
	constraint.GraphView

	targets map[string]string
	inSet   map[string]bool
}

func (g crossProviderGraphFacts) PerEdgeResolvedTarget(node, socket, alias string) string {
	return g.targets[node+"\x00"+socket+"\x00"+alias]
}

func (g crossProviderGraphFacts) FlattenedNodeComponentType(node string) (string, bool) {
	if !g.inSet[node] {
		return "", false
	}
	return "core/compute/api-server", true
}

// crossProviderCoordinates is the enumeration the finalised graph carries, as
// this fixture supplies it: the two nodes, no attachments, and the one connection
// the rule is invoked for.
type crossProviderCoordinates struct {
	nodes []constraint.NodeInstance
	edges []constraint.EdgeInstance
}

func (c crossProviderCoordinates) Nodes() []constraint.NodeInstance { return c.nodes }
func (c crossProviderCoordinates) Attachments(string) []string      { return nil }
func (c crossProviderCoordinates) Edges() []constraint.EdgeInstance { return c.edges }

// crossProviderLinkInput builds a two-node render whose connection crosses a
// provider seam, with each end's capability supplied so a row can make the two
// differ or agree.
//
// Each end's input kind FOLLOWS its capability rather than being chosen per row,
// because the input-kind gate is real: the linking guest advertises
// artifacts-only and the embedded provider guests advertise sourced and
// synthesized, so a row pairing a capability with an input kind its guest refuses
// would be halted by that gate before this rule was ever asked. Both ends are
// real renders either way — the row asserts on what the build reports, not on
// what a stub was told to say.
func crossProviderLinkInput(
	consumerCapability, targetCapability string,
) (renderer.RendererInput, renderer.RecipeLookup) {
	input := testInput()

	// The far end is the package's own "api" node; the consuming end is added
	// beside it with its own boundary so the two land in different dispatch
	// groups whichever capabilities a row supplies.
	nodes := input.Graph.Spec.Nodes
	nodes.Set(crossProviderConsumer, types.IRNode{
		Component:       "core/compute/api-server",
		ResolvedVersion: "1.0.0",
		BoundaryPath:    "local/edge",
	})
	input.Graph.Spec.Nodes = nodes

	values := input.Graph.Spec.ValuesMapping
	values.Set(crossProviderConsumer, json.RawMessage(`{"port":9090}`))
	input.Graph.Spec.ValuesMapping = values

	input.Graph.Spec.Edges = []types.IREdge{{
		Source: crossProviderConsumer,
		Target: crossProviderTarget,
		Socket: "db",
		Alias:  crossProviderTarget,
	}}

	input.GraphView = crossProviderGraphFacts{
		targets: map[string]string{
			crossProviderConsumer + "\x00db\x00" + crossProviderTarget: crossProviderTarget,
		},
		inSet: map[string]bool{crossProviderConsumer: true, crossProviderTarget: true},
	}
	input.GraphCoordinates = crossProviderCoordinates{
		nodes: []constraint.NodeInstance{
			{ID: crossProviderConsumer, SourceFile: "aggregate.yaml", AuthoredName: crossProviderConsumer},
			{ID: crossProviderTarget, SourceFile: "aggregate.yaml", AuthoredName: crossProviderTarget},
		},
		edges: []constraint.EdgeInstance{{
			Node: constraint.NodeInstance{
				ID: crossProviderConsumer, SourceFile: "aggregate.yaml",
				AuthoredName: crossProviderConsumer,
			},
			Socket: "db",
			Alias:  crossProviderTarget,
		}},
	}

	base := testRecipeLookup()
	lookup := renderer.RecipeLookup{
		Recipe: func(node, componentType string) (*types.Recipe, bool) {
			capability := targetCapability
			if node == crossProviderConsumer {
				capability = consumerCapability
			}
			if capability == linkProvider {
				return crossProviderEndRecipe(node, capability), true
			}
			recipe, found := base.Recipe(node, componentType)
			if !found || recipe == nil {
				return recipe, found
			}
			clone := *recipe
			clone.Spec.Capability = capability
			if node == crossProviderConsumer {
				clone.Metadata.Name = node + "-recipe"
			}
			return &clone, true
		},
		FeatureRecipe: base.FeatureRecipe,
	}
	return input, lookup
}

// crossProviderEndRecipe is one end's binding to the linking guest: an
// artifacts-only Recipe, which is the one input kind that guest advertises.
func crossProviderEndRecipe(node, capability string) *types.Recipe {
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set(node+"-config.yaml", types.ArtifactEntry{Inline: fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-config
`, node)})
	return &types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: node + "-recipe"},
		Spec: types.RecipeSpec{
			Capability: capability,
			Implements: "core/compute/api-server",
			Artifacts:  artifacts,
		},
	}
}

// crossProviderLinkFactory routes the consuming end's capability to the guest
// that advertises cross-boundary linking, and everything else to the embedded
// provider guests the package already renders through.
//
// The linking claim a row turns on is therefore a real guest's self-report, read
// through the same seam the render reads it through, rather than a value the test
// supplied.
func crossProviderLinkFactory(t *testing.T, linkingCapability string) renderer.EngineFactory {
	t.Helper()
	embeddedFactory := wasmEngineFactory(t)
	loader := wasm.NewCachingLoader(nil)
	var linking renderer.Provider
	return func(providerRef string) (renderer.Provider, error) {
		if renderer.ProviderEngineKind(providerRef) != linkingCapability {
			return embeddedFactory(providerRef)
		}
		if linking == nil {
			linking = fixtureEngine(t, loader, linkFixture)
		}
		return linking, nil
	}
}

// TestRenderAll_ConnectionAcrossAProviderSeamIsReported is the REFUSAL row
// through the render entry point: the consuming end renders through a provider
// that advertises cross-boundary linking, the far end through one that does not,
// and the build reports the connection while still producing output.
//
// This is the row that proves a BUILD reaches the rule, which no unit row can.
// It also pins the severity at the invocation: the render returns no error, so a
// rule routed to the halt conversion instead of the advisory stream would fail
// here rather than quietly turning a working build into a failing one.
func TestRenderAll_ConnectionAcrossAProviderSeamIsReported(t *testing.T) {
	t.Parallel()
	input, lookup := crossProviderLinkInput(linkProvider, "helm")

	res, err := renderer.RenderAll(
		context.Background(), input, lookup, crossProviderLinkFactory(t, linkProvider))
	if err != nil {
		t.Fatalf("a connection across a provider seam must not halt the build; got %v", err)
	}

	var found []string
	for _, w := range res.Warnings {
		if strings.Contains(w, renderer.ErrCrossProviderLinkUnsupported().Error()) {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one advisory naming the condition, got %d of %d warning(s): %v",
			len(found), len(res.Warnings), res.Warnings)
	}
	// Every coordinate an operator needs, plus the position of the authored site.
	for _, want := range []string{
		"aggregate.yaml",
		".spec.components." + crossProviderConsumer + ".uses.db",
		`"` + crossProviderTarget + `"`,
		`socket "db"`,
		`consumer provider "` + linkProvider + `"`,
		`target provider "helm"`,
	} {
		if !strings.Contains(found[0], want) {
			t.Errorf("advisory does not carry %q:\n%s", want, found[0])
		}
	}
	// The build produced output: the connection carries the scalar value the
	// injection contract pipes, so the deployment stands.
	if len(res.Files) == 0 {
		t.Error("the render produced no file, so the row proves nothing about a build that finishes")
	}
}

// TestRenderAll_ConnectionWithinOneProviderIsSilent is the SILENT row on the same
// fixture: both ends render through the same provider, so there is no seam.
//
// It is the row a refusal-only corpus cannot see. A rule that reported every
// connection would satisfy the row above and fail here.
//
// The provider here is a non-linking one deliberately. Driving the both-ends-link
// case through the render entry point would engage the link pass itself, whose
// guest publishes and resolves against file names of its own contract — so the row
// would measure that contract rather than this rule's silence. That silence is
// pinned at the unit tier beside this file, where the claim is present on both
// ends and the rule still reports nothing; the pass's own rows cover the wiring.
func TestRenderAll_ConnectionWithinOneProviderIsSilent(t *testing.T) {
	t.Parallel()
	input, lookup := crossProviderLinkInput("helm", "helm")

	res, err := renderer.RenderAll(
		context.Background(), input, lookup, crossProviderLinkFactory(t, linkProvider))
	if err != nil {
		t.Fatalf("RenderAll must render a connection within one provider; got %v", err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, renderer.ErrCrossProviderLinkUnsupported().Error()) {
			t.Errorf("a connection within one provider must report nothing; got %q", w)
		}
	}
}

// TestRenderAll_ConnectionAcrossTwoNonLinkingProvidersIsSilent is the second
// SILENT row, and it pins the capability half of the condition rather than the
// provider half.
//
// Both ends render through providers that advertise no linking, so nothing was
// going to be wired natively and there is nothing to tell the author. A predicate
// that reported every provider seam would refuse this and would pass every other
// row in this file.
func TestRenderAll_ConnectionAcrossTwoNonLinkingProvidersIsSilent(t *testing.T) {
	t.Parallel()
	// Neither capability routes to the linking guest, so both ends' claims are
	// their real guests' — and neither embedded provider advertises linking.
	input, lookup := crossProviderLinkInput("compose", "helm")

	res, err := renderer.RenderAll(
		context.Background(), input, lookup, crossProviderLinkFactory(t, linkProvider))
	if err != nil {
		t.Fatalf("RenderAll must render a seam neither provider can link across; got %v", err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, renderer.ErrCrossProviderLinkUnsupported().Error()) {
			t.Errorf("a seam neither provider links across must report nothing; got %q", w)
		}
	}
}
