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
// keeps the Release's own order, enclosing scope before the scopes nested inside
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

	// release is the Release document. The boundary set is read from it and
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
	c.release = releaseOf(resolved)
	c.releaseFile = releaseFileOf(resolved)
	return c
}

// releaseOf is the Release document a render was given, nil when it was given
// no resolve result.
//
// One home because two consumers need it: the boundary axes this file derives,
// and the presentation that answers a fact keyed by a boundary. They must read
// the same document — a rule invoked over a scope the axis found has to be
// answerable by the presentation — and two readings of the absent case would be
// free to differ, one substituting an empty document where the other left nil.
func releaseOf(resolved *resolver.ResolveResult) *types.Release {
	if resolved == nil {
		return nil
	}
	return resolved.Release
}

// releaseFileOf is the Release document a boundary coordinate is positioned
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

// ArtifactsEntries returns the keys a node's Recipe writes in its artifacts
// block, in the order that Recipe declares them.
//
// Declared order is the fact rather than an incidental property of it: the
// entries are walked in that order and each contributes its outputs in turn, so a
// rule invoked once per entry reports its findings in the order an author reads
// the block and in the same order on every run.
//
// The axis is what the author WROTE and not what the block projected, which is
// the whole reason it stands beside the artifact axis rather than being reached
// through it. An entry importing a library bundle contributes one output per file
// that bundle holds, so an entry whose import fails contributes none — and a rule
// about the import itself would be invoked no times for exactly the entries it
// exists to refuse, which reports what a rule that ran and approved reports.
//
// The Recipe is the one the render bound to the node, and it is the same map the
// presentation reads each entry's authored reference out of — so an entry
// enumerated here is an entry that presentation can answer.
//
// Each coordinate is positioned at the Recipe rather than at the Aggregate the
// component was authored in, because the entry is written in the Recipe and that
// is the document an author edits. The Recipe's name stands in for its path, for
// the reason the provides axis states.
func (c *describedStageCoordinates) ArtifactsEntries(node string) []constraint.AuthoredCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	keys := recipe.Spec.Artifacts.Keys()
	if len(keys) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(keys))
	for _, key := range keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: recipe.Metadata.Name})
	}
	return out
}

// RecipeImportFields returns the spec-root fields a node's own Recipe supplies by
// `$import:`, in the fixed order the enumeration declares them.
//
// The enumeration is [RecipeImportFields] — the one home of that set — so a field
// presented here is a field the presentation beside it can answer, and a field
// added to the set arrives on both surfaces at once. Reproducing the set here
// would let the axis and the presentation cover different fields, and the field
// whichever of them omitted would be unchecked with nothing reporting it.
//
// The axis is what the author WROTE and not what the reference resolved to, which
// is the whole reason it stands beside the template-field axis rather than being
// reached through it. An imported field's content exists only once the import has
// succeeded, so a rule about the import itself would be invoked no times for
// exactly the fields it exists to refuse — which reports what a rule that ran and
// approved reports.
//
// The Recipe is the one the render bound to the node, and it is the same map the
// presentation reads each field's authored reference out of — so a field
// enumerated here is a field that presentation can answer.
//
// Each coordinate is positioned at the Recipe rather than at the Aggregate the
// component was authored in, because the field is written in the Recipe and that
// is the document an author edits. The Recipe's name stands in for its path, for
// the reason the provides axis states.
func (c *describedStageCoordinates) RecipeImportFields(node string) []constraint.AuthoredCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	fields := RecipeImportFields(recipe)
	if len(fields) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(fields))
	for _, field := range fields {
		out = append(out, constraint.AuthoredCoordinate{Value: field, SourceFile: recipe.Metadata.Name})
	}
	return out
}

// RecipeSourceFields returns the position a node's own Recipe declares its source
// entry at — one coordinate for a node with a bound Recipe, and none for a node
// with no Recipe to declare one.
//
// The coordinate is presented whether or not the author wrote anything at the
// position, and that is the axis rather than an oversight: a rule about an entry
// left behind with nothing in it has to be invoked where the entry would be. An
// enumeration yielding only the entries that carry something would be invoked no
// times for exactly the documents such a rule exists to refuse, which reports what
// a rule that ran and approved reports. Whether the entry was written is a declared
// fact over this coordinate, where a reader of the rule can see it.
//
// The Recipe is the one the render bound to the node, and it is the same map the
// view reads the entry's presence out of — so a position enumerated here is a
// position that view can answer.
//
// Each coordinate is positioned at the Recipe rather than at the Aggregate the
// component was authored in, because the entry is written in the Recipe and that is
// the document an author edits. The Recipe's name stands in for its path, for the
// reason the provides axis states.
func (c *describedStageCoordinates) RecipeSourceFields(node string) []constraint.AuthoredCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	return []constraint.AuthoredCoordinate{
		{Value: recipeFieldSource, SourceFile: recipe.Metadata.Name},
	}
}

// RecipeBoundaryBlocks returns the position a node's own Recipe writes its
// boundary block at — one coordinate for a node with a bound Recipe, and none for
// a node with no Recipe to write one.
//
// The coordinate is presented whether or not the Recipe declares the block, on the
// reading [describedStageCoordinates.RecipeSourceFields] gives for its own
// position: whether the block was declared is a declared fact over this coordinate,
// where a reader of the rule can see it. The Recipe is the one the render bound to
// the node, and its name stands in for its path, for the reason the provides axis
// states.
func (c *describedStageCoordinates) RecipeBoundaryBlocks(node string) []constraint.AuthoredCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	return []constraint.AuthoredCoordinate{
		{Value: recipeFieldBoundary, SourceFile: recipe.Metadata.Name},
	}
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
	return localsCoordinatesOf(recipe.Spec.Locals, recipe.Metadata.Name)
}

// AttachmentLocalsKeys returns the locals the FeatureRecipe one attachment binds
// on a node declares, in the order that FeatureRecipe declares them.
//
// Declared order is the fact rather than an incidental property of it, and here it
// is load-bearing twice over, exactly as it is on a node's own block: the entries
// are evaluated in that order, and the prefix of this axis above a given entry IS
// the set that entry may read.
//
// The FeatureRecipe is resolved through the same binding the render evaluates the
// attachment with, and it is the same document the presentation reads the authored
// text out of — so a local enumerated here is a local that presentation can
// answer.
//
// Each coordinate is positioned at the FeatureRecipe rather than at the Aggregate
// the attachment was written in, because the entry is written in the FeatureRecipe
// and that is the document an author edits. The FeatureRecipe's name stands in for
// its path, for the reason the provides axis states.
func (c *describedStageCoordinates) AttachmentLocalsKeys(
	node string, attachment string,
) []constraint.AuthoredCoordinate {
	fr, held := c.attachmentFeatureRecipe(node, attachment)
	if !held {
		return nil
	}
	return localsCoordinatesOf(fr.Spec.Locals, fr.Metadata.Name)
}

// BoundaryAttachmentLocalsKeys returns the locals the FeatureRecipe one
// boundary-scope attachment binds declares, in the order that FeatureRecipe
// declares them.
//
// The axis is per boundary rather than per node because that is where such an
// attachment is declared: it is written once and reaches every component the
// boundary holds, so a per-node axis would present one authored block once per
// component.
func (c *describedStageCoordinates) BoundaryAttachmentLocalsKeys(
	boundary string, attachment string,
) []constraint.AuthoredCoordinate {
	fr, held := c.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !held {
		return nil
	}
	return localsCoordinatesOf(fr.Spec.Locals, fr.Metadata.Name)
}

// localsCoordinatesOf presents a declared locals block as the coordinate shape the
// locals traversals take, keeping the order the document declares.
//
// One home because three axes need it — a node's own Recipe block, an attachment's
// FeatureRecipe block, and a boundary-scope attachment's. A second reading would
// be free to differ on the order it preserves or on what it does with an empty
// block, and the prefix of this order is what a rule over the axis decides from.
func localsCoordinatesOf(
	locals types.OrderedMap[string], sourceFile string,
) []constraint.AuthoredCoordinate {
	keys := locals.Keys()
	if len(keys) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(keys))
	for _, key := range keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: sourceFile})
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

// Boundaries returns every boundary the Release declares, at every depth,
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
// in the order the Release declares them.
func (c *describedStageCoordinates) BoundaryAttachments(boundary string) []string {
	var out []string
	c.walkBoundaries(func(path string, b types.Boundary) {
		if path == boundary {
			out = b.Features.Keys()
		}
	})
	return out
}

// BoundaryAttachmentPipelineSteps returns the steps of the pipeline the
// FeatureRecipe of one attachment declared on a boundary carries, in the order
// that FeatureRecipe declares them. The execution scope that FeatureRecipe
// declares is a separate fact, read through BoundaryAttachmentExecutionScope;
// this answers for every attachment the boundary declares, whatever it is.
func (c *describedStageCoordinates) BoundaryAttachmentPipelineSteps(
	boundary string, attachment string,
) []constraint.AuthoredCoordinate {
	fr, held := c.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !held {
		return nil
	}
	return stepCoordinatesOf(fr.Spec.Pipeline, fr.Metadata.Name)
}

// NodeTemplateFields returns the inline template fields a node's own Recipe
// writes, in the order that Recipe's fields are enumerated in.
//
// The enumeration is [RecipeTemplateFields] — the one home of that set — so a
// field presented here is a field the presentation beside it can answer, and a
// field added to the set arrives on both surfaces at once. Reproducing the set
// here would let the axis and the presentation cover different fields, and the
// field whichever of them omitted would be unchecked with nothing reporting it.
//
// The Recipe is the one the render bound to the node, keyed by the identity the
// composed graph gives it, so a component a sub-module contributed is reached here
// exactly as the facts about it are reached.
//
// Each coordinate is positioned at the Recipe rather than at the Aggregate the
// component was authored in, because the field is written in the Recipe and that
// is the document an author edits. The Recipe's name stands in for its path, for
// the reason the provides axis states.
func (c *describedStageCoordinates) NodeTemplateFields(node string) []constraint.TemplateFieldCoordinate {
	recipe, held := c.recipes[node]
	if !held || recipe == nil {
		return nil
	}
	return templateFieldCoordinatesOf(RecipeTemplateFields(recipe), recipe.Metadata.Name)
}

// templateFieldCoordinatesOf presents an enumerated inline-template-field set as
// the coordinate shape the template-field traversals take, keeping the order the
// enumeration declared.
//
// One home because two axes need it — a node's own Recipe and an attachment's
// FeatureRecipe, which are enumerated through one shared body and therefore arrive
// in one shape. A second reading would be free to differ on which of the three
// carried readings it promotes to the coordinate's value, and the traversal spells
// its locator from the authored keys rather than from that value, so a divergence
// there would leave a finding positioned at a path the file does not contain.
//
// The boundary-rooted axis beside them keeps its own loop, and that is a property
// of what it enumerates rather than an inconsistency: it walks a different struct
// type, over a set that includes two blocks these two deliberately leave to their
// own axes.
func templateFieldCoordinatesOf(
	fields []RecipeTemplateField, sourceFile string,
) []constraint.TemplateFieldCoordinate {
	if len(fields) == 0 {
		return nil
	}
	out := make([]constraint.TemplateFieldCoordinate, 0, len(fields))
	for _, field := range fields {
		out = append(out, constraint.TemplateFieldCoordinate{
			Value:        field.Label,
			SourceFile:   sourceFile,
			AuthoredKeys: field.AuthoredKeys,
		})
	}
	return out
}

// AttachmentTemplateFields returns the inline template fields the FeatureRecipe
// one attachment binds on a node writes, in the order that FeatureRecipe's fields
// are enumerated in.
//
// The enumeration is [AttachmentTemplateFields] in the renderer — the one home of
// that set — so a field presented here is a field the presentation beside it can
// answer, and a field added to the set arrives on both surfaces at once.
// Reproducing the set here would let the axis and the presentation cover different
// fields, and the field whichever of them omitted would be unchecked with nothing
// reporting it.
//
// Every attachment the node carries is enumerated, whatever scope its FeatureRecipe
// states. The axis presents what the document writes; which of those attachments a
// given rule is about is the rule's to select, from the execution-scope fact keyed
// at this same coordinate. Narrowing here instead would make the axis mean one rule
// rather than the document.
//
// Each coordinate is positioned at the FeatureRecipe rather than at the Aggregate
// the attachment was written in, because the field is written in the FeatureRecipe
// and that is the document an author edits. The FeatureRecipe's name stands in for
// its path, for the reason the provides axis states.
func (c *describedStageCoordinates) AttachmentTemplateFields(
	node string, attachment string,
) []constraint.TemplateFieldCoordinate {
	fr, held := c.attachmentFeatureRecipe(node, attachment)
	if !held {
		return nil
	}
	return templateFieldCoordinatesOf(AttachmentTemplateFields(fr), fr.Metadata.Name)
}

// BoundaryAttachmentTemplateFields returns the inline template fields the
// FeatureRecipe one boundary-declared attachment binds writes, in the order that
// FeatureRecipe's fields are enumerated in.
//
// The enumeration is BoundaryScopeFRTemplateFields — the same one the render's
// own refusal walks — so a field presented here is a field that refusal reaches,
// and a field added to the set arrives on both surfaces at once. Reproducing the
// set here would let the axis and the refusal cover different fields, and the
// field whichever of them omitted would be unchecked with nothing reporting it.
//
// Every attachment the boundary declares is enumerated, whatever scope its
// FeatureRecipe states. The axis presents what the document writes; which of
// those attachments a given rule is about is the rule's to select, from the
// execution-scope fact keyed at this same coordinate. Narrowing here instead
// would make the axis mean one rule rather than the document.
//
// Each coordinate is positioned at the FeatureRecipe rather than at the Release
// the attachment was declared in, because the field is written in the
// FeatureRecipe and that is the document an author edits. The FeatureRecipe's
// name stands in for its path, for the reason the provides axis states.
func (c *describedStageCoordinates) BoundaryAttachmentTemplateFields(
	boundary string, attachment string,
) []constraint.TemplateFieldCoordinate {
	fr, held := c.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !held {
		return nil
	}
	fields := BoundaryScopeFRTemplateFields(fr)
	if len(fields) == 0 {
		return nil
	}
	out := make([]constraint.TemplateFieldCoordinate, 0, len(fields))
	for _, field := range fields {
		out = append(out, constraint.TemplateFieldCoordinate{
			Value:        field.Label,
			SourceFile:   fr.Metadata.Name,
			AuthoredKeys: field.AuthoredKeys,
		})
	}
	return out
}

// BoundaryPathTemplateFields returns the authored fields one boundary writes a
// path template under, in the fixed order the enumeration declares.
//
// The enumeration is [BoundaryPathTemplateFields] — the same one the render's own
// refusal is driven from — so a field presented here is a field that refusal
// reaches, and a field added to the set arrives on both surfaces at once.
// Reproducing the set here would let the axis and the refusal cover different
// fields, and the field whichever of them omitted would be unchecked with nothing
// reporting it.
//
// A field the operator left empty contributes nothing, which is what the shared
// enumeration decides rather than a filter applied here: the render evaluates a
// default this package supplies in its place, and a rule invoked over that
// default would report a fault against text nobody wrote.
//
// The coordinates carry no file of their own, because the field is written in the
// same document as the scope that holds it — so the traversal positions a finding
// from the scope's own file and a second reading of it here could only disagree.
func (c *describedStageCoordinates) BoundaryPathTemplateFields(boundary string) []string {
	b, held := boundaryAtPath(c.release, boundary)
	if !held {
		return nil
	}
	fields := BoundaryPathTemplateFields(b)
	if len(fields) == 0 {
		return nil
	}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.Field)
	}
	return out
}

// walkBoundaries visits every declared boundary with the path it is named by,
// descending into nested scopes, in the Release's own order, through the one walk
// every stage enumerates a Release's boundaries by.
func (c *describedStageCoordinates) walkBoundaries(visit func(path string, b types.Boundary)) {
	resolver.WalkReleaseBoundaries(c.release, visit)
}

// boundaryAtPath returns the boundary a Release declares at one path.
//
// It walks rather than indexing, through the one walk the axes are enumerated by,
// so the path it matches is spelled exactly as the axis that produced the
// coordinate spelled it.
func boundaryAtPath(release *types.Release, path string) (types.Boundary, bool) {
	var found types.Boundary
	var held bool
	resolver.WalkReleaseBoundaries(release, func(p string, b types.Boundary) {
		if p == path {
			found, held = b, true
		}
	})
	return found, held
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

// AttachmentSite reports where one attachment a node carries was written, by
// asking the graph's own enumeration, and reports nothing where that enumeration
// records no site.
//
// It is spelled here rather than inherited because the graph enumeration is
// embedded as an interface naming the node, attachment and connection axes, and
// an embedded interface promotes only the methods it names — so without this the
// traversals over this stage's attachments would find no site and position every
// attachment at the node's own block whatever wrote it, while the same rule's
// finding at the graph stage named the block the author wrote.
func (c *describedStageCoordinates) AttachmentSite(node, attachment string) (constraint.AttachmentSite, bool) {
	sites, ok := c.GraphCoordinates.(constraint.AttachmentSiteEnumerator)
	if !ok {
		return constraint.AttachmentSite{}, false
	}
	return sites.AttachmentSite(node, attachment)
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
	_ boundaryScopeAttachmentAxes                         = (*describedStageCoordinates)(nil)
	_ authoredProvidesAxes                                = (*describedStageCoordinates)(nil)
	_ renderedNameAxes                                    = (*describedStageCoordinates)(nil)
	_ authoredLocalsAxes                                  = (*describedStageCoordinates)(nil)
	_ crossBoundaryLinkAxis                               = (*describedStageCoordinates)(nil)
	_ constraint.NodeEnumerator                           = (*describedStageCoordinates)(nil)
	_ constraint.EdgeEnumerator                           = (*describedStageCoordinates)(nil)
	_ constraint.ProvidesKeyEnumerator                    = (*describedStageCoordinates)(nil)
	_ constraint.LocalsKeyEnumerator                      = (*describedStageCoordinates)(nil)
	_ constraint.AttachmentEnumerator                     = (*describedStageCoordinates)(nil)
	_ constraint.AttachmentSiteEnumerator                 = (*describedStageCoordinates)(nil)
	_ constraint.PipelineStepEnumerator                   = (*describedStageCoordinates)(nil)
	_ constraint.AttachmentPipelineStepEnumerator         = (*describedStageCoordinates)(nil)
	_ constraint.BoundaryEnumerator                       = (*describedStageCoordinates)(nil)
	_ constraint.BoundaryAttachmentEnumerator             = (*describedStageCoordinates)(nil)
	_ constraint.BoundaryAttachmentPipelineStepEnumerator = (*describedStageCoordinates)(nil)
	_ constraint.BoundaryPathTemplateFieldEnumerator      = (*describedStageCoordinates)(nil)
	_ constraint.NodeTemplateFieldEnumerator              = (*describedStageCoordinates)(nil)
	_ constraint.RecipeImportFieldEnumerator              = (*describedStageCoordinates)(nil)
)
