// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// GraphCoordinates is the node, attachment and connection enumeration the
// finalised graph carries, as the described stage consumes it.
//
// The described stage takes it rather than deriving it. All three axes are
// settled once the graph is finalised — the node set is what the composition
// produced, a node's attachment set is what the whole attachment cascade folded
// to, and the connection set is every edge the graph holds however it was drawn
// — so deriving any of them here would be a second answer to a question the
// build has already answered, free to disagree with the one every earlier rule
// was invoked over.
//
// The connection axis is named here rather than accepted only where it is used,
// which is what makes a rule over connections structurally un-skippable for a
// caller that supplies coordinates at all: such a caller either satisfies the
// whole set or does not compile. A caller supplying no coordinates leaves every
// rule over them uninvoked, which is a different exposure and is stated at the
// field this value travels on.
type GraphCoordinates interface {
	constraint.NodeEnumerator
	constraint.AttachmentEnumerator
	constraint.EdgeEnumerator
}

// describedStageCoordinates derives the coordinates the described stage adds to
// the ones the finalised graph already carries: the steps of each pipeline a
// build declares, the keys each node's Recipe publishes, the locals it declares,
// the boundary set, and the attachments each boundary declares on itself.
//
// It extends the graph stage's enumeration rather than restating it, the way a
// stage's presentation extends the presentation below it. The node axis and the
// per-node attachment axis are promoted from the enumeration the compilation
// produced; what is derived here is exactly what that enumeration does not
// reach.
//
// Every axis returns a deterministic order and each states which. A pipeline's
// steps keep the order the entity declares them in; a Recipe's provides keys and
// its locals each keep the order that Recipe declares them in; the boundary set
// keeps the deployment's own order, enclosing scope before the scopes nested inside
// it; a boundary's attachments keep the order that boundary declares them in. No
// axis here ranges a plain Go map.
//
// The pipelines are read through the same binding the render resolves entities
// with, so a pipeline enumerated here is the pipeline the render runs. A step
// coordinate carries the entity that declares it rather than the document the
// attachment was written in: that entity is what an author edits to change the
// step.
type describedStageCoordinates struct {
	GraphCoordinates

	// graph is the finalised graph, read for the attachment-to-FeatureRecipe
	// binding — the same thing the presentation reads it for.
	graph *types.IRGraph

	// recipes is the Recipe bound to each node, as the render resolved it. It is
	// held rather than looked up again so a node's own pipeline is enumerated
	// from the binding the render will execute.
	recipes map[string]*types.Recipe

	// recipeLookup resolves an attachment to the FeatureRecipe it binds, by the
	// same instance-plus-type key the render resolves attachments with.
	recipeLookup RecipeLookup

	// release is the deployment document. The boundary set is read from it and
	// not from the graph's projection of boundary attachments: that projection
	// records a boundary only once something is attached at it, so an axis over
	// it would be narrower than the boundary set a rule is asked about, and it
	// would narrow silently — a boundary it dropped contributes no subject, so
	// nothing would fail.
	release *types.Release

	// releaseFile positions a boundary coordinate at the document the boundary
	// was declared in.
	releaseFile string
}

// newDescribedStageCoordinates derives the described stage's coordinates over
// the products a render holds.
//
// It returns nil when the graph or the enumeration it extends is absent. An
// enumeration built from nothing would present every rule an empty subject set,
// and a rule invoked zero times reports exactly what a rule invoked and
// satisfied reports — so a caller checks for nil rather than being handed a
// value that answers emptily.
func newDescribedStageCoordinates(
	graph *types.IRGraph,
	graphCoordinates GraphCoordinates,
	recipes map[string]*types.Recipe,
	recipeLookup RecipeLookup,
	resolved *resolver.ResolveResult,
) *describedStageCoordinates {
	if graph == nil || graphCoordinates == nil {
		return nil
	}
	c := &describedStageCoordinates{
		GraphCoordinates: graphCoordinates,
		graph:            graph,
		recipes:          recipes,
		recipeLookup:     recipeLookup,
	}
	if resolved != nil {
		c.release = resolved.Release
	}
	c.releaseFile = releaseFileOf(resolved)
	return c
}

// releaseFileOf is the deployment document a boundary coordinate is positioned
// at, empty when the render was given no resolve result.
//
// A boundary is declared in that document and nowhere else, so it is the only
// position such a coordinate can carry. An empty one leaves a finding
// unpositioned rather than mispositioned, which is what the position field admits
// where no document applies.
//
// One home because two stages need it: the boundary axis this file derives, and
// the axis over what a render assembled. A second reading of the same field would
// agree today and would be free to differ the next time the absent case is
// decided — one of them defaulting where the other leaves empty.
func releaseFileOf(resolved *resolver.ResolveResult) string {
	if resolved == nil {
		return ""
	}
	return resolved.ReleasePath
}

// PipelineSteps returns the steps of a node's own Recipe pipeline, in the order
// that Recipe declares them, identified by the identifier each step declares.
//
// The Recipe is the one the render bound to the node, keyed by the identity the
// composed graph gives it — so a component a sub-module contributed is reached
// here exactly as the facts about it are reached. An enumeration keyed by the
// name a root document typed would hold no entry for such a component and would
// present its steps to no rule.
func (c *describedStageCoordinates) PipelineSteps(node string) []constraint.AuthoredCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	return stepCoordinatesOf(recipe.Spec.Pipeline, recipe.Metadata.Name)
}

// ProvidesKeys returns the keys a node's Recipe publishes, in the order that
// Recipe declares them.
//
// Declared order is the fact rather than an incidental property of it: the
// entries are evaluated in the order the document writes them, so a rule invoked
// once per key reports its findings in the order an author reads the block.
//
// The Recipe is the one the render bound to the node, and it is the same map the
// presentation reads the authored text out of — so a key enumerated here is a key
// that presentation can answer, and a rule cannot be invoked about an entry whose
// text nothing would supply.
//
// Each coordinate is positioned at the Recipe rather than at the Aggregate the
// component was authored in, because the entry is written in the Recipe and that
// is the document an author edits. The Recipe's name stands in for its path: the
// catalog loader does not retain the path on the parsed document, and a logical
// identifier a reader can act on is what the position field admits where no file
// applies.
func (c *describedStageCoordinates) ProvidesKeys(node string) []constraint.AuthoredCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	keys := recipe.Spec.Provides.Keys()
	if len(keys) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(keys))
	for _, key := range keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: recipe.Metadata.Name})
	}
	return out
}

// LocalsKeys returns the locals a node's Recipe declares, in the order that
// Recipe declares them.
//
// Declared order is the fact rather than an incidental property of it, and here it
// is load-bearing twice over: the entries are evaluated in that order, so a rule
// invoked once per local is asked in the order the question is decided, and the
// prefix of this axis above a given entry IS the set that entry may read.
//
// The Recipe is the one the render bound to the node, and it is the same map the
// presentation reads the authored text out of — so a local enumerated here is a
// local that presentation can answer, and a rule cannot be invoked about an entry
// whose text nothing would supply.
//
// Each coordinate is positioned at the Recipe rather than at the Aggregate the
// component was authored in, because the entry is written in the Recipe and that is
// the document an author edits. The Recipe's name stands in for its path, for the
// reason the provides axis states.
func (c *describedStageCoordinates) LocalsKeys(node string) []constraint.AuthoredCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	keys := recipe.Spec.Locals.Keys()
	if len(keys) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(keys))
	for _, key := range keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: recipe.Metadata.Name})
	}
	return out
}

// AttachmentPipelineSteps returns the steps of the pipeline one attachment's
// FeatureRecipe carries on a node, in the order that FeatureRecipe declares
// them.
func (c *describedStageCoordinates) AttachmentPipelineSteps(
	node string, attachment string,
) []constraint.AuthoredCoordinate {
	fr, held := c.attachmentFeatureRecipe(node, attachment)
	if !held {
		return nil
	}
	return stepCoordinatesOf(fr.Spec.Pipeline, fr.Metadata.Name)
}

// Boundaries returns every boundary the deployment declares, at every depth,
// enclosing scope before the scopes nested inside it.
//
// A scope normalization synthesized carries no path of its own and contributes
// none: the operator declared no such scope, so a coordinate naming one would
// name a key the document does not contain.
func (c *describedStageCoordinates) Boundaries() []constraint.AuthoredCoordinate {
	var out []constraint.AuthoredCoordinate
	c.walkBoundaries(func(path string, _ types.Boundary) {
		out = append(out, constraint.AuthoredCoordinate{Value: path, SourceFile: c.releaseFile})
	})
	return out
}

// BoundaryAttachments returns the attachments one boundary declares on itself,
// in the order the deployment declares them.
func (c *describedStageCoordinates) BoundaryAttachments(boundary string) []string {
	var out []string
	c.walkBoundaries(func(path string, b types.Boundary) {
		if path == boundary {
			out = b.Features.Keys()
		}
	})
	return out
}

// BoundaryAttachmentPipelineSteps returns the steps of the pipeline one
// boundary-scope attachment's FeatureRecipe carries, in the order that
// FeatureRecipe declares them.
func (c *describedStageCoordinates) BoundaryAttachmentPipelineSteps(
	boundary string, attachment string,
) []constraint.AuthoredCoordinate {
	fr, held := c.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !held {
		return nil
	}
	return stepCoordinatesOf(fr.Spec.Pipeline, fr.Metadata.Name)
}

// walkBoundaries visits every declared boundary with the path it is named by,
// descending into nested scopes, in the deployment's own order.
func (c *describedStageCoordinates) walkBoundaries(visit func(path string, b types.Boundary)) {
	if c.release == nil {
		return
	}
	rel := types.NormalizeRelease(c.release).Release()
	if rel == nil {
		return
	}
	var walk func(path string, b types.Boundary)
	walk = func(path string, b types.Boundary) {
		if !b.Synthesized {
			visit(path, b)
		}
		for name, child := range b.Boundaries.All() {
			next := name
			if path != "" {
				next = path + "/" + name
			}
			walk(next, child)
		}
	}
	for name, b := range rel.Spec.Boundaries.All() {
		start := name
		if b.Synthesized {
			start = ""
		}
		walk(start, b)
	}
}

// attachmentFeatureRecipe resolves the FeatureRecipe one attachment binds on a
// node, through the same binding and the same instance-plus-type key the render
// resolves attachments with.
func (c *describedStageCoordinates) attachmentFeatureRecipe(
	node string, attachment string,
) (*types.FeatureRecipe, bool) {
	if c.recipeLookup.FeatureRecipe == nil {
		return nil, false
	}
	entries, held := c.graph.Spec.FeatureValuesMapping.Get(node)
	if !held {
		return nil, false
	}
	entry, held := entries.Get(attachment)
	if !held {
		return nil, false
	}
	fr, found := c.recipeLookup.FeatureRecipe(attachment, entry.Type)
	if !found || fr == nil {
		return nil, false
	}
	return fr, true
}

// boundaryAttachmentFeatureRecipe resolves the FeatureRecipe one boundary-scope
// attachment binds, through the same binding the render resolves it with.
func (c *describedStageCoordinates) boundaryAttachmentFeatureRecipe(
	boundary string, attachment string,
) (*types.FeatureRecipe, bool) {
	if c.recipeLookup.FeatureRecipe == nil {
		return nil, false
	}
	feats, held := c.graph.Spec.BoundaryFeatures.Get(boundary)
	if !held {
		return nil, false
	}
	feat, held := feats.Get(attachment)
	if !held {
		return nil, false
	}
	fr, found := c.recipeLookup.FeatureRecipe(attachment, feat.Type)
	if !found || fr == nil {
		return nil, false
	}
	return fr, true
}

// stepCoordinatesOf presents a declared pipeline as the coordinate shape the
// step traversals take, keeping the order the document declares.
func stepCoordinatesOf(steps []types.PipelineStep, sourceFile string) []constraint.AuthoredCoordinate {
	if len(steps) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(steps))
	for _, s := range steps {
		out = append(out, constraint.AuthoredCoordinate{Value: s.ID, SourceFile: sourceFile})
	}
	return out
}

// The enumeration is held to every axis the described stage's rules are keyed
// by, so an axis that stops being satisfied fails to compile here rather than at
// the invocation that needed it.
var (
	_ describedStageAxes                                  = (*describedStageCoordinates)(nil)
	_ authoredProvidesAxes                                = (*describedStageCoordinates)(nil)
	_ authoredLocalsAxes                                  = (*describedStageCoordinates)(nil)
	_ crossBoundaryLinkAxis                               = (*describedStageCoordinates)(nil)
	_ constraint.NodeEnumerator                           = (*describedStageCoordinates)(nil)
	_ constraint.EdgeEnumerator                           = (*describedStageCoordinates)(nil)
	_ constraint.ProvidesKeyEnumerator                    = (*describedStageCoordinates)(nil)
	_ constraint.LocalsKeyEnumerator                      = (*describedStageCoordinates)(nil)
	_ constraint.AttachmentEnumerator                     = (*describedStageCoordinates)(nil)
	_ constraint.PipelineStepEnumerator                   = (*describedStageCoordinates)(nil)
	_ constraint.AttachmentPipelineStepEnumerator         = (*describedStageCoordinates)(nil)
	_ constraint.BoundaryEnumerator                       = (*describedStageCoordinates)(nil)
	_ constraint.BoundaryAttachmentEnumerator             = (*describedStageCoordinates)(nil)
	_ constraint.BoundaryAttachmentPipelineStepEnumerator = (*describedStageCoordinates)(nil)
)
