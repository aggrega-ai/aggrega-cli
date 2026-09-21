// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// One rule refuses two of a render's input-kind blocks meeting on one output name,
// and it runs over THREE separately authored documents. This file drives a real
// build once per document and asserts the halt each time.
//
// # Why the proof is per owner and not once
//
// The three documents emit through one render path, and two of them emit under the
// HOST component's node identity. So a reading keyed by the node alone would answer
// about the host's own emission while the blocks in question are an attachment's:
// it would refuse none of the attachment's collisions and would refuse the host's
// more than once. Neither direction shows up in an outcome over one owner — a
// build that halts for the wrong reason halts, and a build that admits an
// attachment's collision looks exactly like a build with no collision in it.
//
// So each owner gets its own build, its own collision, and its own assertion that
// the halt names the document the colliding blocks are in. A proof over one owner
// would leave the other two indistinguishable from unenforced.
//
// # Why a source and a workload
//
// They are the two blocks whose output names are chosen furthest apart: a source
// render's names come from the provider's own output and a workload synthesis's from
// what the synthesizer produces, and nothing declares either distinct from the
// other. A collision between them is therefore the shape an author actually meets,
// and it is reachable at every one of the three sites, which is what makes one
// fixture shape serve all three.

// collidingName is the output name both blocks of a colliding site produce a file
// under. It carries a prefix no other fixture in this package uses, so a render
// driven by another test cannot satisfy an assertion here.
const collidingName = "emitted-collision-proof.yaml"

// collidingProvider emits the same output name from its source render and from its
// workload synthesis, which is the collision the rule refuses.
//
// It is a provider of this file's own rather than the shared fixture's, because the
// shared one's source render emits nothing at all — so a site declaring both blocks
// there produces one file and no collision. Reaching for the shared provider and
// widening it would change what every other proof over that fixture drives.
type collidingProvider struct {
	stubEngine

	// collide reports whether this provider's two blocks meet. A provider that does
	// not collide emits distinct names, which is what the negative control reads
	// through: without it, a build that halts for some unrelated reason would be
	// indistinguishable from the rule firing.
	collide bool
}

func (p collidingProvider) Render(
	_ context.Context, _ map[string]any, _ NodeRenderContext,
) ([]RenderedFile, error) {
	return []RenderedFile{{Path: collidingName, Content: []byte("kind: FromSource\n")}}, nil
}

func (p collidingProvider) SynthesizeWorkload(
	_ context.Context, _ *types.TemplateContext, _ *types.Workload, _ NodeRenderContext,
) ([]EmittedFile, error) {
	name := collidingName
	if !p.collide {
		name = "emitted-collision-proof-distinct.yaml"
	}
	return []EmittedFile{{Name: name, Body: []byte("kind: FromWorkload\n")}}, nil
}

// collisionSite names which of the three documents the proof gives both blocks to.
type collisionSite int

const (
	// collideNodesOwn gives the host component's own Recipe a source and a workload.
	collideNodesOwn collisionSite = iota

	// collideNodeAttachment gives the FeatureRecipe one attachment binds on the node
	// a source and a workload.
	collideNodeAttachment

	// collideBoundaryAttachment gives the FeatureRecipe the boundary-scope
	// attachment binds a source and a workload.
	collideBoundaryAttachment

	// collideNothing gives no site both blocks, which is the negative control.
	collideNothing
)

// The identities the proof's own fixture declares. They are separate from the shared
// four-site fixture's so a change to either cannot silently alter the other.
const (
	collisionNode             = "api"
	collisionBoundaryPath     = "cluster"
	collisionCapability       = "collision-provider"
	collisionAttachInstance   = "mon"
	collisionBoundaryInstance = "bridge"
	collisionAttachFeature    = "core/monitoring"
	collisionBoundaryFeature  = "core/bridge"
	collisionHostRecipeName   = "api-recipe"
	collisionAttachRecipeName = "mon-fr"
	collisionBridgeRecipeName = "bridge-fr"
	collisionComponentType    = "core/compute/api-server"
)

// collisionWorkload is the workload body a site declaring one carries.
const collisionWorkload = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: collision:latest
`

// collisionGraphFacts stands in for the presentation the graph finalisation
// produces, which the render's later presentations are built over.
//
// It is supplied because the presentation chain refuses to assemble with a hole in
// it, and because the rule under test halts rather than skipping when a caller
// supplied a presentation the chain then lost — so a build without it would exercise
// the wrong arm.
type collisionGraphFacts struct{ constraint.GraphView }

// collisionCoordinates is the node enumeration the finalised graph carries, which the
// rule over a node's own emission is invoked across.
//
// It is supplied because the production build supplies it, and because the node's own
// rule HALTS rather than skipping when a caller that handed in a presentation hands in
// no enumeration. A fixture omitting it would exercise that halt instead of the rule,
// which is how the first run of this file read: the build completed, and the reason
// was an absent node axis rather than an admitted collision.
//
// Only the node axis carries a value. The other two are named by the interface and
// read by no rule this file drives, so they answer empty rather than standing in for
// an enumeration this fixture does not model.
type collisionCoordinates struct{}

func (collisionCoordinates) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{
		ID:           collisionNode,
		AuthoredName: collisionNode,
		SourceFile:   "aggregate.yaml",
	}}
}

func (collisionCoordinates) Attachments(string) []string { return nil }

func (collisionCoordinates) Edges() []constraint.EdgeInstance { return nil }

var _ GraphCoordinates = collisionCoordinates{}

// collisionLookup binds the host component and the two attachments, giving both
// input-kind blocks to exactly the site named.
//
// Every other site gets ONE block, so a halt can only come from the named one. That
// is what makes each build's halt attributable: a fixture giving two sites both
// blocks would halt on whichever the walk reached first, and the proof could not say
// which owner refused.
func collisionLookup(site collisionSite) RecipeLookup {
	hostWorkload := types.WorkloadTemplate{}
	if site == collideNodesOwn {
		hostWorkload = types.WorkloadTemplate{Inline: collisionWorkload}
	}
	attachSource := any(nil)
	if site == collideNodeAttachment {
		attachSource = "./mon-chart"
	}
	bridgeSource := any(nil)
	if site == collideBoundaryAttachment {
		bridgeSource = "./bridge-chart"
	}

	return RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != collisionComponentType {
				return nil, false
			}
			return &types.Recipe{
				APIVersion: "aggrega.ai/v1alpha1",
				Kind:       "Recipe",
				Metadata:   types.RecipeMetadata{Name: collisionHostRecipeName, Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: collisionCapability,
					Implements: collisionComponentType,
					Source:     "./host-chart",
					Workload:   hostWorkload,
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			switch featureType {
			case collisionAttachFeature:
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: collisionAttachRecipeName, Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability: collisionCapability,
						Implements: collisionAttachFeature,
						Source:     attachSource,
						Workload:   types.WorkloadTemplate{Inline: collisionWorkload},
					},
				}, true
			case collisionBoundaryFeature:
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: collisionBridgeRecipeName, Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability:     collisionCapability,
						Implements:     collisionBoundaryFeature,
						ExecutionScope: "boundary",
						Source:         bridgeSource,
						Workload:       types.WorkloadTemplate{Inline: collisionWorkload},
					},
				}, true
			}
			return nil, false
		},
	}
}

// collisionInput assembles the one build shape all three owners are reached from:
// a single node carrying one attachment, inside a boundary carrying one
// boundary-scope attachment.
func collisionInput() RendererInput {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(collisionNode, types.IRNode{
		Component:       collisionComponentType,
		BoundaryPath:    collisionBoundaryPath,
		ResolvedVersion: "1.0.0",
	})

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set(collisionAttachInstance, types.IRFeatureEntry{Type: collisionAttachFeature})
	attachments.Set(collisionBoundaryInstance, types.IRFeatureEntry{Type: collisionBoundaryFeature})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(collisionNode, attachments)

	boundaryProjection := types.OrderedMap[types.IRBoundaryFeature]{}
	boundaryProjection.Set(collisionBoundaryInstance, types.IRBoundaryFeature{
		Type:  collisionBoundaryFeature,
		Infra: types.OrderedMap[any]{},
	})
	boundaryFeatures := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	boundaryFeatures.Set(collisionBoundaryPath, boundaryProjection)

	authoredFeatures := types.OrderedMap[types.BoundaryFeature]{}
	authoredFeatures.Set(collisionBoundaryInstance, types.BoundaryFeature{
		Type:  collisionBoundaryFeature,
		Infra: types.OrderedMap[any]{},
	})
	boundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   authoredFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	boundary.Components.Set(collisionNode, types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(collisionBoundaryPath, boundary)

	return RendererInput{
		GraphView:        collisionGraphFacts{},
		GraphCoordinates: collisionCoordinates{},
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "collision-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     boundaryFeatures,
				Provenance:           types.IRProvenance{CompilerVersion: "test", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{
			collisionNode: {collisionAttachInstance, collisionBoundaryInstance},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "collision-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{
					Name: "collision-release", Version: "1.0.0", Environment: "test",
				},
				Spec: types.ReleaseSpec{Boundaries: boundaries},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				collisionNode: {BoundaryPath: collisionBoundaryPath, Directory: "dist/cluster"},
			},
		},
	}
}

// collisionFactory resolves the one capability every site declares to the provider
// whose two blocks meet, or do not.
func collisionFactory(collide bool) EngineFactory {
	return func(capability string) (Provider, error) {
		if capability != collisionCapability {
			return nil, errors.New("no provider for that capability")
		}
		return collidingProvider{collide: collide}, nil
	}
}

// TestTheNegativeControlBuildCompletes establishes that the fixture builds at all
// when no site's blocks meet.
//
// Without it every halt below would be unattributable: a fixture that cannot build
// for an unrelated reason halts on all four cases, and a proof reading only "it
// halted" would report the rule as enforcing when nothing had reached it.
func TestTheNegativeControlBuildCompletes(t *testing.T) {
	t.Parallel()

	res, err := RenderAll(
		context.Background(), collisionInput(), collisionLookup(collideNothing), collisionFactory(false),
	)
	if err != nil {
		t.Fatalf("the control build did not complete, so no halt below is attributable to the rule: %+v", err)
	}
	if res.EmittedView == nil {
		t.Fatal("the control build published no emitted view, so the rule could not have been asked")
	}
	// The control must actually emit under the name the colliding cases collide on.
	// A control that emitted nothing there would pass while proving that the rule
	// was never reachable rather than that it approved.
	if origins := res.EmittedView.EmittedFileOriginSet(collisionNode, collidingName); len(origins) != 1 {
		t.Fatalf("the control's node emitted %d block(s) under %q, want exactly 1; "+
			"a control that does not reach the name proves nothing about the rule approving it",
			len(origins), collidingName)
	}
}

// assertCollisionHalt reads one build's halt as the declared rule's, by identity and
// by content.
//
// The identity is asserted rather than the message text alone, because a message can
// be produced by anything: a build failing for an unrelated reason carries a
// different condition, and only the condition tells the two apart. The content is
// then asserted as well, because the identity alone cannot show the diagnostic is
// actionable — an author needs the output name and both contributing blocks.
func assertCollisionHalt(t *testing.T, err error, wantDocument string) string {
	t.Helper()
	if err == nil {
		t.Fatal("the build completed; two blocks meeting on one output name must halt it")
	}
	if !errors.Is(err, validator.ErrRecipeFilenameCollisionAcrossInputKinds()) {
		t.Fatalf("the halt does not carry the filename-collision condition: %+v", err)
	}
	// The whole cause chain, not the outermost message. The boundary-scope dispatch
	// wraps whatever its render returned in a message naming the node and the
	// attachment, so the outermost text carries none of the rule's own finding —
	// pre-existing behaviour of that path, and the reason the sibling owner proof
	// beside this file reads the chain too.
	msg := cerrors.CauseChain(err)
	if !strings.Contains(msg, collidingName) {
		t.Errorf("the halt does not name the output name %q: %s", collidingName, msg)
	}
	// Both blocks, because a diagnostic saying two things collided without saying
	// which is not something an author can act on.
	if !strings.Contains(msg, "source") {
		t.Errorf("the halt does not name the source block: %s", msg)
	}
	if !strings.Contains(msg, "workload") {
		t.Errorf("the halt does not name the workload block: %s", msg)
	}
	// The document the colliding blocks are in, which is what separates the three
	// owners' findings from one another.
	if wantDocument != "" && !strings.Contains(msg, wantDocument) {
		t.Errorf("the halt does not position the finding in %q: %s", wantDocument, msg)
	}
	return msg
}

// TestANodesOwnTwoBlocksMeetingOnOneNameHalts drives the owner a node's own Recipe
// is, and is the one owner the reading this replaced already covered.
func TestANodesOwnTwoBlocksMeetingOnOneNameHalts(t *testing.T) {
	t.Parallel()

	_, err := RenderAll(
		context.Background(), collisionInput(), collisionLookup(collideNodesOwn), collisionFactory(true),
	)
	msg := assertCollisionHalt(t, err, "")
	t.Logf("node's own owner halted: %s", msg)
}

// TestAnAttachmentsTwoBlocksMeetingOnOneNameHalts drives the owner the
// FeatureRecipe one attachment binds on a node is.
//
// 🔴 This is one of the two owners the reading this replaced covered and the fact
// clause 2 originally scoped did NOT: an attachment's rendered files never joined the
// per-node emission the earlier fact was keyed by, so a rule declared over that fact
// alone would have left this build to succeed. The halt asserted here is what makes
// the migration a migration rather than a coverage loss.
func TestAnAttachmentsTwoBlocksMeetingOnOneNameHalts(t *testing.T) {
	t.Parallel()

	_, err := RenderAll(
		context.Background(), collisionInput(), collisionLookup(collideNodeAttachment), collisionFactory(true),
	)
	msg := assertCollisionHalt(t, err, collisionAttachRecipeName)
	// The attachment is named because the locator cannot carry it, and a node may
	// bind several attachments whose FeatureRecipes are separate documents.
	if !strings.Contains(msg, collisionAttachInstance) {
		t.Errorf("the halt does not name the attachment %q: %s", collisionAttachInstance, msg)
	}
	t.Logf("attachment-on-a-node owner halted: %s", msg)
}

// TestABoundaryScopeAttachmentsTwoBlocksMeetingOnOneNameHalts drives the owner the
// FeatureRecipe a boundary-scope attachment binds is.
//
// 🔴 This is the second owner the earlier fact did not reach. Its render carries a
// composite dispatch marker rather than any component's node identity, so no
// node-rooted key could have named it at all.
func TestABoundaryScopeAttachmentsTwoBlocksMeetingOnOneNameHalts(t *testing.T) {
	t.Parallel()

	_, err := RenderAll(
		context.Background(), collisionInput(),
		collisionLookup(collideBoundaryAttachment), collisionFactory(true),
	)
	msg := assertCollisionHalt(t, err, collisionBridgeRecipeName)
	if !strings.Contains(msg, collisionBoundaryInstance) {
		t.Errorf("the halt does not name the attachment %q: %s", collisionBoundaryInstance, msg)
	}
	t.Logf("boundary-scope attachment owner halted: %s", msg)
}
