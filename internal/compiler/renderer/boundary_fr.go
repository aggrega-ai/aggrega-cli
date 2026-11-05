// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Boundary-scope FeatureRecipe dispatch: the executionScope: boundary
// branch of Phase B.6. Per-component FR dispatch (the default,
// executionScope: component or unset) lives in phase_b_subpasses.go and is
// untouched by this file.
//
// Lifecycle.
//
//   1. RenderAll instantiates one *BoundaryFRState at start. The state
//      threads through every per-node phaseB6 invocation so dedup tracking
//      and boundary-scope provides accumulate across nodes that share a
//      boundary attachment.
//   2. Per-node phaseB6 detects boundary-scope FRs by checking
//      fr.Spec.ExecutionScope == "boundary". For each such FR encountered:
//      - Compute the dedup key (boundary_path, fr_instance).
//      - If state.Dedup[key] is set, skip — the FR already ran for this
//        boundary.
//      - Else: build the boundary-scope TemplateContext (see
//        buildBoundaryContext), evaluate FR locals + values + provides
//        templates, dispatch to the FR's engine, classify emitted files by
//        Intermediate flag, and record state. What those templates may
//        reference is decided ahead of the render by the rules declared over
//        the FR's inline fields, so nothing in this file re-decides it.
//   3. After the per-node loop completes, RenderAll routes boundary-scope
//      FR Intermediate=true files into the per-(path, provider) dispatch
//      group at the FR's evaluated path (using fr.Spec.Capability for the
//      provider component of the dispatch key). Intermediate=false files
//      carry the instance name alone, and where they land follows from
//      whether the group's elected assembler gives each contributor a
//      position of its own. Where it does, the attachment occupies that
//      position — <boundary_directory>/<provider>/<nest-segment>/<instance>/
//      — and nothing further is composed inside it. Where it does not,
//      the instance is encoded into a FILENAME prefix and the files land
//      directly in the boundary's own directory, at
//      <boundary_directory>/<provider>/<instance>--<file>. The two are
//      alternatives the group selects between, never one shape nested
//      inside the other.
//      Whichever coordinate carries it is named for the attachment
//      instance, never for the synthetic node id below: that id is a
//      dispatch marker, and the characters it opens and joins with are
//      refused in a path.
//   4. Consumer resolution: a boundary-scope FR's locals and provides are
//      evaluated once for its boundary, and every consumer the boundary holds
//      reads that one evaluation — the inject-rehydration path for a uses edge
//      the attachment carries, and .features.<attachment>.provides and the
//      .locals its injectMapping reads — through one cascade-aware lookup
//      (see resolveBoundaryScopeFREvaluation), which walks from the
//      consumer's BoundaryPath up to the boundary holding the FR attachment.

package renderer

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// executionScopeBoundary is the value a FeatureRecipe's spec.executionScope
// carries when its resource generation runs once for the boundary that holds
// the attachment rather than once for each component carrying it.
//
// It is a named constant because the same value decides two things that must
// not be able to disagree: which surface the render dispatches the attachment
// to, and which surface the rules over that attachment's pipeline are asked
// about. A second spelling of the string would let the render evaluate an
// attachment once while a rule reported on it per component.
const executionScopeBoundary = "boundary"

// boundaryFRNodeID is the dispatch identity the compiler holds one boundary-scope
// FeatureRecipe contribution under: a marker that says what kind of contributor it
// is, followed by both coordinates an author wrote for it.
//
// It is the single home of that identity, and both the render that produces the
// contribution and the routing that places its output reach it rather than
// spelling a marker of their own. Two spellings is not a stylistic matter here: the
// render declares the names its output carries under the identity it was given, and
// the assembled artifact addresses that content by the identity the routing held, so
// two spellings mean the name declared and the name emitted are derived from two
// different identities — which is a mismatch nothing fails on, because each side is
// self-consistent.
//
// Both coordinates are present because either alone identifies the wrong thing. The
// instance name alone is shared by every boundary that attaches the same feature
// under it, and two boundaries' declared directories can evaluate alike and feed one
// dispatch group — so an identity without the path arrives at that group as one
// contributor where two were declared, and one of them is lost. The path alone is
// shared by every attachment at that boundary.
//
// 🔴 It is a marker for the compiler's own dispatch and is NEVER a name a target
// accepts: it opens with an angle bracket and joins with a colon and an at sign,
// which a path refuses on some platforms and a name alphabet refuses outright.
// Nothing reads it back apart, either — it joins its two parts with characters
// either part may itself contain, so cutting it cannot return what went in. What
// a reader meets instead is the composed identity settled for the same tuple
// ([BoundaryFRState.ComponentIDs]) and the coordinates recorded beside the
// contribution.
func boundaryFRNodeID(key boundaryFRKey) string {
	return boundaryFRNodePrefix + key.FRInstance + "@" + key.BoundaryPath
}

// boundaryFRKey identifies a (boundary_path, FR-instance) tuple for
// boundary-scope FR deduplication and provides storage. The boundary path
// is the slash-separated traversal path to the boundary that holds the FR
// attachment (matching IRNode.BoundaryPath's shape); the FR instance name
// is the map key in the boundary's Features block.
type boundaryFRKey struct {
	BoundaryPath string
	FRInstance   string
}

// BoundaryFRState holds cross-node accumulator state for boundary-scope
// FeatureRecipe dispatch. One instance lives per RenderAll invocation;
// the orchestrator threads it through every per-node phaseB6 call.
//
// Dedup tracks (boundary_path, FR-instance) tuples already processed, so
// subsequent encounters of the same key skip the FR — the FR has already
// run once for that boundary.
//
// Evaluations stores the one evaluation of each (boundary, FR-instance)
// tuple's locals and provides; every consumer the boundary holds reads it
// through the cascade walk ([resolveBoundaryScopeFREvaluation]).
//
// Files captures the boundary-scope FR's emitted output for routing at
// finalization (Intermediate=true → per-(path, provider) BoundaryAssembler;
// Intermediate=false → per-boundary features/<instance>/ disk write).
type BoundaryFRState struct {
	Dedup       map[boundaryFRKey]bool
	Evaluations map[boundaryFRKey]boundaryFREvaluation
	Files       map[boundaryFRKey][]EmittedFile

	// evaluationsMade counts, per tuple, the evaluations of its locals and
	// provides that were made rather than read back. Nothing in the render reads
	// it: it is the instrument the proof that a tuple is evaluated once for the
	// whole build reads, kept on the state so a parallel test reads its own count.
	evaluationsMade map[boundaryFRKey]int

	// DerivedKeys carries every derived name each boundary-scope FeatureRecipe is
	// answerable for — the names its guest declares, plus the component identity
	// the compiler derives for it from its instance name and its boundary path —
	// keyed exactly as Files is so a declaration routes to the same dispatch group
	// the declaring contributor's files route to. A boundary-scope FeatureRecipe is
	// a contributor to an assembled output like any component, so its declarations
	// join the same per-group injectivity check; keying it any other way would
	// leave the check covering components and silently skipping features.
	DerivedKeys map[boundaryFRKey]types.OrderedMap[[]string]

	// ComponentIDs carries the composed component identity settled for each
	// (boundary_path, FR-instance) tuple — the identity every resource that
	// FeatureRecipe's output carries, composed from the instance name and the
	// segments of the boundary path and shortened to whatever bound its own target
	// accepts. It is also the value the boundary's subDirectory: template reads as
	// .component_id for that attachment, so the directory an author names after it
	// is the label the author reads on the resources inside.
	//
	// It is recorded here rather than recomputed where it is read, and the reason is
	// that recomputing it is not a cheaper spelling of the same thing: the
	// composition is settled against the FeatureRecipe's own rendering plugin, so a
	// second composition would have to ask that plugin again to reach the same
	// answer, and one that skipped the asking would spell the identity differently
	// from the one the resources already carry. Keyed exactly as Files is, so a
	// reader iterating the emitted output reaches the identity for the contributor
	// that emitted it. Written only through [BoundaryFRState.settledComponentID].
	//
	// A tuple whose identity was never settled carries none, which is the same
	// state as absent: a reader stamping or assembling the contributor's output
	// then uses its own dispatch identity as-is. The path evaluation never reads
	// such a tuple, because the routing visits only tuples whose render settled one.
	ComponentIDs map[boundaryFRKey]string

	// DerivedKeyRepeats carries, per the same key, the names one of that
	// FeatureRecipe's declarations held more than once inside a single one of its
	// key spaces. Kept beside DerivedKeys for the reason the per-node accumulator
	// keeps its own: merging a declaration into the accumulated names is what
	// makes a duplicate inside one declaration indistinguishable from a name
	// restated at two crossings, so it is recorded as the declaration arrives.
	DerivedKeyRepeats map[boundaryFRKey][]derivedKeyRepeat

	// Values carries what each (boundary_path, FR-instance) tuple's own
	// valuesMapping resolved to, keyed exactly as Files is so a reader routing the
	// emitted output to a dispatch group reaches the values for the contributor
	// that emitted it.
	//
	// It is recorded here rather than re-evaluated where it is read for the reason
	// ComponentIDs is: the evaluation runs against this FeatureRecipe's own
	// boundary-scoped context, which the surfaces that route its output no longer
	// hold, so a second evaluation there would either rebuild that context or
	// resolve against a different one and supply values the render never used.
	//
	// A tuple whose FeatureRecipe resolved no values carries none, which is the
	// same state as absent.
	Values map[boundaryFRKey]map[string]any

	// ValuesReachAssembly records, per the same key, that the tuple's Values also
	// travel to the assembler of the dispatch group its output joins, as that
	// contributor's own values marked as a mapping's output: the tuple rendered
	// from a source and its valuesMapping produced them
	// ([FeatureValues.reachAssembly]). Recorded beside Values, at the render that
	// settled both conditions, for the reason Values is recorded rather than
	// re-derived. An absent entry reads as false.
	ValuesReachAssembly map[boundaryFRKey]bool

	// links is the render's one classification of the uses edges the
	// cross-boundary link pass wires, and its record of what templates read off
	// them. It rides on this state because the state is what both boundary-scope
	// renders — the provides pre-pass and the per-boundary dispatch — already
	// share, and each stamps the edge views it builds through it. Nil in a render
	// that wires no links, which stamps nothing.
	links *crossBoundaryLinks

	// assemblyPaths is the render's one answer to where each node's output is
	// assembled, keyed by node ID ([assemblyPathsByNode]). It rides on this state
	// for the reason links does: both boundary-scope renders build the boundary's
	// edge views, and each view carries its endpoints' assembly paths from here.
	// Nil in a render that computed none, which leaves every assembly path empty.
	assemblyPaths map[string]string

	// ownAssemblyPaths is the render's one answer to where each boundary-scope
	// attachment's own output is assembled, keyed exactly as Files is
	// ([assemblyPathsByBoundaryFR]). It rides beside assemblyPaths for the same
	// reason: both boundary-scope renders build the attachment's boundary view, and
	// the view carries its own assembly path from here. Nil in a render that
	// computed none, which leaves the view's assembly path empty.
	ownAssemblyPaths map[boundaryFRKey]string
}

// boundaryFREvaluation is the one evaluation of a boundary-scope FeatureRecipe's
// locals and provides for one (boundary, FR-instance) tuple, both in the order the
// FeatureRecipe declares them.
//
// It is made once, against the boundary-scope context, and read wherever the
// attachment's locals or provides are read: by the boundary's own render, by
// every consumer the boundary holds at .features.<attachment>.provides and at the
// .locals its injectMapping reads, by an authored inject block on the attachment,
// and by a uses edge the attachment carries. A second evaluation per consumer
// would be a second value — and one that could not read .boundary at all, since a
// consumer's context does not carry it.
type boundaryFREvaluation struct {
	Locals   types.OrderedMap[string]
	Provides types.OrderedMap[string]
}

// NewBoundaryFRState returns a fresh state instance ready for population.
func NewBoundaryFRState() *BoundaryFRState {
	return &BoundaryFRState{
		Dedup:               map[boundaryFRKey]bool{},
		Evaluations:         map[boundaryFRKey]boundaryFREvaluation{},
		evaluationsMade:     map[boundaryFRKey]int{},
		Files:               map[boundaryFRKey][]EmittedFile{},
		ComponentIDs:        map[boundaryFRKey]string{},
		Values:              map[boundaryFRKey]map[string]any{},
		ValuesReachAssembly: map[boundaryFRKey]bool{},
		DerivedKeys:         map[boundaryFRKey]types.OrderedMap[[]string]{},
		DerivedKeyRepeats:   map[boundaryFRKey][]derivedKeyRepeat{},
	}
}

// settledComponentID returns the composed component identity of one
// boundary-scope attachment, settling it against engine and recording it in
// ComponentIDs the first time it is asked for, and reading the record back every
// time after.
//
// The identity is COMPOSED from the two things an author wrote — the instance
// name the feature is attached under, and the segments of the boundary path it is
// attached to — and deliberately not read out of the attachment's dispatch
// identity ([boundaryFRNodeID]). That identity is a marker the compiler built for
// its own dispatch, and every character it joins those two parts with is one a
// label value refuses; substituting for them destroys the boundary between the
// parts, so two attachments an author can tell apart would reach one identity.
//
// How long the identity may be is the FeatureRecipe's own target's answer rather
// than a number the compiler holds, so the composition is settled against the
// engine the feature renders through: composed, put to the guest, shortened to
// whatever bound the guest returned, and put again until the guest accepts it. A
// guest that answers no such question accepts the composition as composed.
//
// It is the one place a tuple's identity is settled, and both readers reach it:
// the answer to where every attachment's output is assembled, which runs before
// any template is evaluated because a subDirectory: may read the identity as
// .component_id, and the attachment's own render, which stamps it on every
// resource. A failed settlement records nothing, so the next reader settles again
// and reports the failure where it reads the identity.
//
// Because the first reader runs before any template is evaluated, no resolved
// workload is in hand, and the question carries none: a name a target derives
// only for the attachment's workload — a Service named after one of its keys, the
// job a scheduled workload emits — is not judged for a boundary-scope attachment.
func (s *BoundaryFRState) settledComponentID(ctx context.Context, engine any, fr *types.FeatureRecipe, key boundaryFRKey) (string, error) {
	if id, ok := s.ComponentIDs[key]; ok {
		return id, nil
	}
	id, err := SettleComposedComponentIdentity(ctx, engine, labels.BoundaryFeatureScope,
		fr.Spec.Capability, nil, append(splitBoundaryPath(key.BoundaryPath), key.FRInstance)...)
	if err != nil {
		return "", err
	}
	s.ComponentIDs[key] = id
	return id, nil
}

// boundaryScopeFRScope is the reference set a boundary-scope FR template is
// checked against — every inline field the recipe evaluates once for its boundary.
// The universe is types.TemplateContext — the same type a boundary-scope FR
// evaluates against, with .boundary populated and the per-component pillars left
// zero — so the roots the check admits are derived from that type rather than
// enumerated here.
//
// The recipe's injectMapping is not such a field. It runs once per consuming
// component, on that component's context, where these pillars are set and
// .boundary is not, so it is checked against [injectMappingScope], the scope its
// evaluation reads; [boundaryScopeFRFieldScope] says which of the two a field
// takes.
//
// The surface declares one narrowing: the pillars that are zero at boundary
// scope. Each is a deliberately kept exclusion, because the type carries the
// field for the per-component path and only this surface cannot use it:
//
//   - context  — replaced by .boundary.{path,boundary_name,components,...}.
//     .context.instance_id, .context.inject, .context.uses are
//     meaningless when the FR renders once for a boundary rather than once
//     per component.
//   - workload — the per-component Workload; there is no single component.
//   - attachment_name, inject, inject_raw — per-attachment; a boundary
//     attachment has no per-component delivery pipe.
//
// boundary-scope FRs reference .boundary.* pillars instead. The rules declared
// over these fields are asked before anything is evaluated and before any FR
// reaches its provider engine, so a shape mismatch is named as an authoring error
// rather than surfacing as an engine failure.
var boundaryScopeFRScope = templateScope{
	data:    &types.TemplateContext{},
	exclude: []string{"context", "workload", "attachment_name", "inject", "inject_raw"},
}

// BoundaryScopeFRTemplateField is one inline template field a FeatureRecipe
// declares: the label an author reads it back by, the text as written, and the
// authored keys beneath the document's spec that name it.
//
// The three travel together because they are three readings of one authored
// entry, and a surface deriving any of them from another would be free to differ
// from this one. The label is what a diagnostic quotes, the text is what the
// admission check parses, and the keys are what position a finding inside the
// FeatureRecipe.
type BoundaryScopeFRTemplateField struct {
	// Label names the field the way an author reads it back: the authored key,
	// carrying the entry's own key or the step's id where the block is keyed.
	Label string

	// Text is the template as written, before anything is evaluated.
	Text string

	// AuthoredKeys are the keys beneath the FeatureRecipe's spec that name this
	// field, one segment per key.
	//
	// They are carried rather than derived from the label, and the difference is
	// not tidiness. Deriving them would mean parsing the label's grammar a second
	// time, in a second place, and the grammar joins an authored key to its block
	// with characters an authored key is not constrained to exclude — so a
	// provides or locals key containing one of them would not survive the round
	// trip, and the locator would name a path the file does not contain.
	AuthoredKeys []string
}

// BoundaryScopeFRTemplateFields presents every inline template field a
// FeatureRecipe declares, as the one enumeration of that set.
//
// It is one home because three surfaces need the same answer: the pre-evaluation
// refusal below, the axis a declared rule over these fields is invoked along, and
// the presentation that hands one field's authored text to such a rule. Two
// enumerations would be free to differ on which fields they carry, and a field
// either of them omitted would be silently unchecked — which is the failure an
// enumerated set exists to prevent.
//
// A field whose content is imported from a file or a library carries no text
// here and is absent from the set: that content is disk-resident or resolved when
// the render reaches it, so there is nothing to read before then. The inline
// forms are the whole of what this set covers, and a caller stating its coverage
// states that bound with it.
//
// The order is the document's own within each keyed block and fixed between the
// blocks, so a caller reporting the first offending field reports the same field
// on every run.
func BoundaryScopeFRTemplateFields(fr *types.FeatureRecipe) []BoundaryScopeFRTemplateField {
	if fr == nil {
		return nil
	}
	var out []BoundaryScopeFRTemplateField
	add := func(label, text string, keys ...string) {
		if text == "" {
			return
		}
		out = append(out, BoundaryScopeFRTemplateField{Label: label, Text: text, AuthoredKeys: keys})
	}
	add("valuesMapping", fr.Spec.ValuesMapping.Inline, "valuesMapping")
	add(featureRecipeFieldInjectMapping, fr.Spec.InjectMapping, featureRecipeFieldInjectMapping)
	add("workload", fr.Spec.Workload.Inline, "workload")
	for k, v := range fr.Spec.Artifacts.All() {
		add("artifacts["+k+"]", v.Inline, "artifacts", k)
	}
	omitTemplateFields(fr.Spec.Omits, add)
	for k, v := range fr.Spec.Provides.All() {
		add("provides["+k+"]", v, "provides", k)
	}
	for k, v := range fr.Spec.Locals.All() {
		add("locals["+k+"]", v, "locals", k)
	}
	for _, step := range fr.Spec.Pipeline {
		add("pipeline["+step.ID+"].with", step.With, "pipeline", step.ID, "with")
	}
	return out
}

// featureRecipeFieldInjectMapping is the authored key a FeatureRecipe writes its
// injectMapping under, and the label [BoundaryScopeFRTemplateFields] presents that
// field by. It is a constant because two sites spell it: the enumeration that
// builds the label, and [boundaryScopeFRFieldScope], which recognises the label to
// pick the field's scope. A spelling written out at each would agree on the day it
// was written and drift afterwards, and a mapping the reading stopped recognising
// would be judged against the boundary scope it is never evaluated in.
const featureRecipeFieldInjectMapping = "injectMapping"

// boundaryScopeFRFieldScope returns the scope one inline template field of a
// boundary-scope FeatureRecipe is evaluated against, given the label the field is
// enumerated under.
//
// A boundary-scope FeatureRecipe's templates run in two scopes. The injectMapping
// runs once per consuming component and reads [injectMappingScope], the scope its
// evaluation reads; every other field runs once for the boundary and reads
// [boundaryScopeFRScope]. Answering from the declaration each field is evaluated
// against, rather than from a set kept here, is what keeps the admission and the
// evaluation of a field from disagreeing about what it may read.
func boundaryScopeFRFieldScope(field string) templateScope {
	if field == featureRecipeFieldInjectMapping {
		return injectMappingScope
	}
	return boundaryScopeFRScope
}

// boundaryScopeFRTemplateFault names what one boundary-scope FeatureRecipe
// template field is refused for, or records that every reference it makes is
// admitted.
type boundaryScopeFRTemplateFault int

const (
	// boundaryScopeFRTemplateAdmitted is a field the surface admits every
	// reference of.
	boundaryScopeFRTemplateAdmitted boundaryScopeFRTemplateFault = iota

	// boundaryScopeFRTemplateUnparsed is a field that is not a template at all.
	// It is a failure to READ the field rather than a verdict on what the field
	// says, which is why it is a class of its own: naming a refused reference for
	// one would describe the author's mistake wrongly.
	boundaryScopeFRTemplateUnparsed

	// boundaryScopeFRTemplatePerComponentPillar is a field evaluated once for the
	// boundary naming a root that surface excludes — a pillar the context type
	// carries for the per-component path, which holds nothing when the recipe
	// renders once for a boundary.
	boundaryScopeFRTemplatePerComponentPillar

	// boundaryScopeFRTemplateUnknownRoot is a field naming a reference its scope
	// does not define: a stale root, a mistyped key beneath a closed pillar such as
	// .boundary, or — in the injectMapping, which runs per consuming component — a
	// read of .boundary, which no consuming component's context carries.
	boundaryScopeFRTemplateUnknownRoot
)

// boundaryScopeFRTemplateRef is one field's classification: the fault it carries,
// and the refused reference where there is one.
//
// A field that could not be read at all carries its fault and no reference, and
// the parse failure itself is not carried: a template that does not parse is
// refused by the surface that evaluates the field, which reports the parse error
// against that field. Carrying it here as well would give one authoring fault two
// reporting sites that could disagree about which of them is the finding.
type boundaryScopeFRTemplateRef struct {
	Fault     boundaryScopeFRTemplateFault
	Violation *templateScopeViolation
}

// classifyBoundaryScopeFRTemplate decides what one authored template field on a
// boundary-scope FeatureRecipe is refused for.
//
// It is the ONE home of that decision, and both rules declared over these fields
// route through here. Two readings of one admission would be free to disagree
// about which reference offends and about which identity describes it, and the
// disagreement would surface as one author being told two different things about
// one template.
//
// field is the label the field is enumerated under, and it selects the scope the
// text is checked against through [boundaryScopeFRFieldScope]: the injectMapping is
// judged against the scope it is evaluated in, per consuming component, and every
// other field against the boundary scope.
//
// The reference set is collected by the UNDER-collecting walk, through the scope
// itself. That direction is the one an allow-list may use: it attributes a
// reference only where the spelling names a pillar unambiguously, so a field read
// inside a rebound range body — which names a field of the element rather than a
// pillar — is not counted, and legal authoring is not refused.
//
// A refused root is a per-component pillar only where the boundary scope's own
// narrowing names it. The injectMapping's scope refuses .boundary instead, and that
// read is classed as a reference its scope does not define: the remedy for it is
// not to rewrite against the boundary pillars, which is what the per-component
// pillar finding tells an author.
func classifyBoundaryScopeFRTemplate(field, text string) boundaryScopeFRTemplateRef {
	tree, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		return boundaryScopeFRTemplateRef{Fault: boundaryScopeFRTemplateUnparsed}
	}
	v := boundaryScopeFRFieldScope(field).check(tree)
	switch {
	case v == nil:
		return boundaryScopeFRTemplateRef{Fault: boundaryScopeFRTemplateAdmitted}
	case v.Level == scopeLevelRoot && slices.Contains(boundaryScopeFRScope.exclude, v.Ref.Segs[0]):
		return boundaryScopeFRTemplateRef{Fault: boundaryScopeFRTemplatePerComponentPillar, Violation: v}
	default:
		return boundaryScopeFRTemplateRef{Fault: boundaryScopeFRTemplateUnknownRoot, Violation: v}
	}
}

// dispatchBoundaryScopeFR runs a boundary-scope FeatureRecipe once for a
// given (boundary_path, fr_instance) tuple. The caller (phaseB6) gates on
// state.Dedup[key]; this function assumes the dedup check passed.
//
// Steps:
//
//  1. Build the boundary-scope TemplateContext via buildBoundaryContext.
//  2. Read the boundary's BoundaryFeature attachment for the FR instance
//     to populate .infra and .values.
//  3. Read the FR's one locals and provides evaluation for the tuple — the
//     provides pre-pass made it before any consumer read it, so it is read
//     back here rather than made again — and evaluate valuesMapping against
//     the boundary-scope context carrying those locals.
//  4. Dispatch to the FR's engine.Render with the resolved values.
//  5. Classify emitted files by Intermediate flag; record into state.Files
//     for routing at orchestrator finalization.
//
// What the recipe's own inline templates may reference is decided before any of
// this, by the rules declared over those fields — so nothing here re-decides it.
//
// recipeLookup supplies the file this FeatureRecipe was declared in, which is what
// a relative source its template produced is resolved against and what a diagnostic
// raised inside this render names as its location. It is the same owner the
// component-scope attachment loop reads that fact from; a resolved path passed in
// its place would be a second shape for one fact.
func dispatchBoundaryScopeFR(
	ctx context.Context,
	fr *types.FeatureRecipe,
	frInstance string,
	key boundaryFRKey,
	state *PhaseBState,
	boundaryFRState *BoundaryFRState,
	nodeCtx NodeRenderContext,
	engineFactory EngineFactory,
	recipeLookup RecipeLookup,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	allProvides NodeProvides,
	input RendererInput,
) error {
	// 1-3. Build the FR-scoped TemplateContext (.boundary + .infra, .values
	//       empty per the Release pillar-split). Shared with the pre-loop
	//       provides pre-pass, so the templates evaluated here read the locals
	//       the pre-pass resolved on a context built from the same inputs.
	feat, _ := lookupBoundaryFeatureAttachment(graph.Spec.BoundaryFeatures, key.BoundaryPath, frInstance)
	frCtx, err := buildBoundaryFRTemplateContext(allProvides, fr, frInstance, key, graph, release, boundaryAnnotations, boundaryFRState.assemblyPaths, boundaryFRState.ownAssemblyPaths[key])
	if err != nil {
		return err
	}
	boundaryFRState.links.stampBoundaryLinkReads(frCtx)
	// What the Feature this attachment names declares, governing the infra this
	// context carries, what each entry of the boundary's lists carries that its own
	// entity declares, and the locals computed from either — the set every template
	// this render evaluates is withheld by.
	withheld := boundaryFRKeys(fr, declaredLeavesOf(input.declaredFeatureLeaves, feat.Type), frCtx.Boundary, listDeclarationsOf(input, graph))

	// The FR's locals and provides, read back from the one evaluation the
	// pre-pass made against a context built from the same inputs; the locals land
	// on frCtx for the templates below.
	if err := evaluateBoundaryFRTemplates(fr, frInstance, frCtx, boundaryFRState, key, withheld,
		featureProvidesContractOf(input.CatalogLookup, feat.Type)); err != nil {
		return err
	}

	// 6. Dispatch to FR's engine.
	frEngine, err := engineFactory(fr.Spec.Capability)
	if err != nil {
		return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: key.BoundaryPath}, fmt.Sprintf("boundary-scope FR %q: selecting its rendering plugin", frInstance))
	}

	// Build the per-FR NodeRenderContext. Use the boundary's path as the
	// pseudo-NodeID for the engine — the engine's per-node identity is
	// the boundary-FR tuple, not a per-component instance. The synthetic
	// NodeID marker syntax stays for dispatch + trace + error-message
	// uniqueness. The FeatureRecipe's own Recipe identity is deliberately NOT set
	// here: renderFeatureRecipeFiles is the one funnel every FeatureRecipe render
	// passes through and establishes it there, so both attachment scopes name the
	// producing FeatureRecipe from a single site rather than from two that agree.
	frNodeCtx := nodeCtx
	frNodeCtx.NodeID = boundaryFRNodeID(key)
	frNodeCtx.BoundaryPath = key.BoundaryPath
	// The guest context is the boundary's — one value, handed to this render's
	// provider and to its pipeline step below, so the two sockets receive the same
	// bytes — and not the copied one: that described whichever component's turn
	// reached this dispatch, so its annotations and placement were an artifact of
	// the walk order rather than anything authored about this boundary-wide render.
	var parameters map[string]any
	if input.ResolveResult != nil {
		parameters = input.ResolveResult.Parameters
	}
	frNodeCtx.GuestContext = buildBoundaryFRRenderCtx(key.BoundaryPath, parameters, boundaryAnnotations)
	// The component identity is the one settled for this tuple
	// ([BoundaryFRState.settledComponentID]): composed from what the author wrote
	// rather than read out of the node id above, and settled against the engine
	// this feature renders through. The render that answers where every
	// attachment's output is assembled has normally settled it already, because the
	// boundary's subDirectory: reads it as .component_id; it is read back here, and
	// settled here only where that earlier settlement did not reach an answer.
	frComponentID, err := boundaryFRState.settledComponentID(ctx, frEngine, fr, key)
	if err != nil {
		return err
	}
	frNodeCtx.LabelComponentID = frComponentID
	// The same composed value is this render's target-name basis, and setting it
	// here is what keeps a boundary-scope feature's release name and output-file
	// prefix the names they already were. The basis defaults to the node id, and
	// this node's id is the dispatch marker above: its first character is one a
	// release-name alphabet refuses, so every escape would open the name with a
	// hyphen and the target would return a refusal no shortening repairs. A
	// per-component attachment is the opposite case and leaves the basis empty, so
	// its author's own chart keeps rendering against the host component's name.
	frNodeCtx.DerivedNameBasis = frComponentID
	// That label value is a name the compiler derived, and it lands on every
	// resource this feature's output carries — so it joins the injectivity check
	// the names a guest declares go through. Two features whose identities reach
	// one label value would otherwise hand the same provenance to two different
	// sets of resources with nothing in the emitted output recording that two
	// were meant.
	//
	// Declared into this feature's own accumulator by name, not through the
	// context's sink: the sink at this point is still the one copied from the
	// consumer node, and rewiring it is three lines below. Naming the accumulator
	// is what makes the ordering irrelevant rather than load-bearing — the
	// declaration cannot land in the consumer node's set whichever line it sits
	// on. One declaration, once: the boundary-scope render carries this value
	// across its every crossing, and it is not derived again inside them. An
	// empty value declares nothing, because empty means the node id is used
	// as-is and no name was derived.
	if frNodeCtx.LabelComponentID != "" {
		frLabelKeys := boundaryFRState.DerivedKeys[key]
		boundaryFRState.DerivedKeyRepeats[key] = append(boundaryFRState.DerivedKeyRepeats[key],
			declareDerivedKey(&frLabelKeys, componentIDKeySpace, frNodeCtx.LabelComponentID)...)
		boundaryFRState.DerivedKeys[key] = frLabelKeys
	}
	// The document this FeatureRecipe was declared in, read through the same lookup
	// the component scope reads it through: what a relative source its template
	// produces is resolved against, and what a diagnostic raised inside this render
	// names as its location.
	frDocument := recipeLookup.featureRecipeDocument(fr)
	// The source template is evaluated before it is resolved, on the same terms and
	// in the same order as a component Recipe's: what the resolution below receives
	// is the value the template produced, anchored to the document that declared it.
	// The locals were resolved onto frCtx by evaluateBoundaryFRTemplates above, so
	// the template reads the same values this FeatureRecipe's other templates read.
	//
	// It is evaluated and published on frCtx by the one sequence both FeatureRecipe
	// scopes share — see publishFeatureRecipeSource for what it replaces, evaluates,
	// anchors and sets, and in which order. It runs here rather than immediately
	// before the values mapping because the resolution below reads what it returns;
	// nothing between the two evaluates a template on frCtx.
	evaluatedSource, withheld, err := publishFeatureRecipeSource(frCtx, withheld, fr.Spec.Source, frDocument,
		func(withheld redact.Keys) (any, error) {
			return EvaluateBoundaryScopeFeatureRecipeSource(fr.Spec.Source, frCtx, withheld)
		})
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: frDocument}, fmt.Sprintf("boundary-scope FR %q source", frInstance))
	}
	frNodeCtx.OriginalSource = evaluatedSource
	// Route this boundary-scope FR's guest advisories into the render state's
	// warnings, which reach result.Warnings. The context was copied from a
	// consumer node whose sink (if any) targets a different accumulator, so wire
	// it explicitly here to the boundary-FR render state.
	frNodeCtx.WarningSink = func(d renderctx.Diagnostic) { state.Warnings = append(state.Warnings, d) }
	// The derived-name declarations this FR's guest makes are rewired for the same
	// reason: the copied context's sink targets the consumer node's accumulator,
	// and a declaration landing there would be attributed to that component
	// instead of to this feature — and would be checked against the wrong dispatch
	// group, since the FR's output routes to the group at its own evaluated path.
	frNodeCtx.DerivedKeySink = func(declared types.OrderedMap[[]string]) {
		merged := boundaryFRState.DerivedKeys[key]
		repeated := mergeDerivedKeys(&merged, declared)
		boundaryFRState.DerivedKeys[key] = merged
		boundaryFRState.DerivedKeyRepeats[key] = append(boundaryFRState.DerivedKeyRepeats[key], repeated...)
	}
	// The facts this FR's guest reports are not recorded. The copied context's
	// sink records the consumer node's own render, and this render is not that
	// node's — it belongs to the boundary, and no node's record of what it rendered
	// includes it.
	frNodeCtx.RenderFactSink = nil
	// The boundary-scope FR's source resolves through the shared
	// resolve+extract helper against the boundary's own infra — the SAME
	// infra (graph.Spec.BoundaryFeatures) the lock-time descent pins from —
	// so a remote source is cache-extracted at build and the build-time
	// identity matches the lockfile pin. A local source forwards verbatim.
	resolvedSrc, err := resolveEntitySource(input, evaluatedSource, fr.Spec.Locals, frSourceURI(fr), nil, feat.Infra.TemplateAlphabeticalMapDeep(), frDocument,
		sourceOrigin{authored: fr.Spec.Source, withheld: withheld})
	if err != nil {
		return wrapSourceResolutionError(err, frNodeCtx.NodeID, fmt.Sprintf("boundary-scope FR %q source resolution", frInstance))
	}
	frNodeCtx.Source = resolvedSrc
	frNodeCtx.SourceIgnore = localSourceIgnore(input, evaluatedSource, frDocument)

	// Evaluate the FR's valuesMapping against the boundary-scope context — the same
	// evaluator the per-component FR path uses (phase_b_subpasses.go), not a parallel
	// path. A valuesMapping-less FR yields the flattened .values pillar unchanged, so a
	// boundary FR without a valuesMapping renders byte-identically.
	frValues, err := ExecutePhaseBForFeature(frCtx, fr, input.MappingLibraries, withheld)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("boundary-scope FR %q valuesMapping", frInstance))
	}
	// Recorded against this tuple as the render is handed them, because the
	// surfaces that route this contribution's output run after this render has
	// returned and hold the tuple rather than this context — and the context is
	// what the evaluation above resolves against, so a second evaluation there
	// would supply a value this render never used. Recorded at the evaluation
	// rather than beside the emitted files below for the same reason: nothing
	// between the two touches the values, and reading them from the one site that
	// produced them is what keeps the assembled artifact supplying exactly what
	// the render was given.
	boundaryFRState.Values[key] = frValues.Resolved
	if frValues.reachAssembly(resolvedSrc) {
		boundaryFRState.ValuesReachAssembly[key] = true
	}

	// The context copied above carries the sensitivity declarations of whichever
	// component node's render reached this dispatch, and a boundary-scope feature
	// has no host component at all — so those declarations govern rendered keys
	// that are not this feature's, and which node supplied them is an artifact of
	// the walk order. Carrying them would mark a rendered key from a declaration no
	// author wrote about it, and a consumer that begins withholding a value has no
	// way to discover that it should not have. Overwritten rather than left
	// inherited because a whole-struct copy names no field: nothing at the copy
	// says which declarations travelled with it.
	//
	// What replaces them is what the Feature this attachment names declares, through
	// the same assembly the per-component scope uses. This render's values pillar is
	// empty by construction, so its rendered inputs can only come from a values
	// mapping, whose output keys are a template's own invention — and the gate
	// inside that assembly answers exactly that: none of this render's values keys
	// is recoverable from a declaration. That is the correct answer here rather
	// than an omission, and it is what the assembly's own gate states.
	//
	// It sits here rather than at the copy because the gate it carries is settled
	// by the evaluation immediately above, and nothing between the copy and this
	// line hands this context to a crossing.
	frNodeCtx.DeclaredSensitivity = featureRecipeDeclaredSensitivity(fr, feat.Type, input, frValues.KeysAreDeclaredLeaves)
	// The copied marking over the values as a whole is replaced on the same reading:
	// it described whichever component dispatched this FR, and this render carries
	// the values this FR's own mapping produced.
	frNodeCtx.SensitiveValues = frValues.Sensitive
	// So is whether those values are a mapping's output, on the same reading.
	frNodeCtx.ValuesMapped = frValues.Mapped
	// The copied omit entries are replaced on that reading too: they belonged to the
	// component that dispatched this FR, and this render's source is handed the
	// entries this FR declares, or none.
	frNodeCtx.Omits = frValues.Omits

	// 5-6. Render via the SAME input-kind machinery the per-component FR sites
	// use — the central gate, then sourced engine.Render / synthesized workload
	// synthesis / artifacts emission — against the boundary-scope context (frCtx
	// carries .boundary.* pillars, never .context.*; what the workload and
	// artifacts templates may reference was decided by the rules over those
	// fields, ahead of this render). The
	// returned EmittedFiles already carry their Intermediate flag for boundary-
	// assembler routing. The host and instance parts are both empty, so the
	// identity composed above from the boundary path stays the resource-naming
	// basis: a boundary-attached feature has no single host component to compose
	// against, and an identity built from the instance alone would meet itself
	// across two boundaries sharing one attachment name.
	//
	// The owner is (this boundary, this attachment), passed as the two identities
	// rather than derived from the synthetic node ID above: that string joins the
	// same two values with separators neither of them is constrained to exclude, so
	// splitting it back is not a recoverable operation. The retention it records
	// into and the presentation its declared rule reads are both the render's own,
	// reached through the state of whichever component node dispatched this FR — one
	// of each for the whole render, so which node that was does not select either.
	//
	// No per-component attachment identity is named: this render has no host
	// component, and the identity its resources carry is composed from the boundary
	// path and set on the context above. The reported derived label value is
	// therefore discarded — this render derives none, and the boundary-disambiguated
	// value it carries was declared at the point it was minted above. Reading it
	// here as well would declare one name twice for one derivation.
	//
	// The annotation set handed over is this BOUNDARY's own, not any component's:
	// a boundary-scope render has no host component, and its output lands in the
	// boundary's directory, so the boundary's authored annotations are the set that
	// reaches it. Read from the ordered per-boundary map for the same reason the
	// per-component caller reads the ordered IR node — the advisory stream stays
	// byte-stable across runs of identical input.
	emitted, _, err := renderFeatureRecipeFiles(ctx, frEngine, fr, frCtx, frNodeCtx, frValues.Resolved, resolvedSrc, frDocument,
		input.RecordedPins.For(frDocument), attachmentIdentity{},
		ownerProductsOf(state).attachmentOnBoundary(key.BoundaryPath, frInstance),
		featureAttachmentSlots(fr, input), input.WorkloadLibraries, input.ArtifactLibraries,
		boundaryAnnotations[key.BoundaryPath], input.CatalogLookup.ProviderPlugin, input.ProviderVersion, withheld)
	if err != nil {
		return err
	}

	// Boundary-scope FeatureRecipe pipeline (own only — a boundary-scope FR has
	// no single host component, so host is nil and any /host operation halts
	// with ErrPipelineHostScopeUnavailable).
	if len(fr.Spec.Pipeline) > 0 {
		// own provider is the boundary-scope FR's own engine kind (version pin
		// stripped — the codec keys on the kind); a boundary-scope FR has no
		// single host component (host nil), so hostProvider is empty.
		pres, perr := runNodePipeline(ctx, fr.Spec.Pipeline, emitted, nil, false, ProviderEngineKind(fr.Spec.Capability), "", frNodeCtx.GuestContext, frCtx, withheld, input.YAMLIndent, input.PluginPathLookup, engineFactory, frNodeCtx.NodeID)
		if perr != nil {
			return perr
		}
		emitted = pres.Own
		state.Warnings = append(state.Warnings, pres.Warnings...)
	}
	boundaryFRState.Files[key] = emitted

	return nil
}

// buildBoundaryFRRenderCtx assembles the render-time context a boundary-scope
// FeatureRecipe is handed on both sockets — its provider render and its
// pipeline step receive these same bytes. A boundary-scope FR has no host
// component, so the context carries the fulfilled parameters and the boundary's
// metadata, and the boundary's own cascaded annotation set is both the
// boundary's annotations and the release bucket: the release bucket holds the
// annotations the Release wrote for what is being rendered, which here is the
// boundary itself. That is what lets a guest reading the well-known keys from
// the release bucket place a boundary-wide render by the boundary's values, and
// by no component's. The aggregate and catalog buckets stay empty, since no
// Aggregate component and no catalog entity stands behind this render.
func buildBoundaryFRRenderCtx(boundaryPath string, parameters map[string]any, boundaryAnnotations map[string]types.OrderedMap[string]) renderctx.RenderContext {
	var placement *resolver.BoundaryInfra
	var buckets renderctx.RenderAnnotations
	if ann, ok := boundaryAnnotations[boundaryPath]; ok {
		placement = &resolver.BoundaryInfra{Annotations: ann}
		buckets.Release = ann
	}
	return buildGuestContext(boundaryPath, buckets, 0, placement, parameters)
}

// buildBoundaryFRTemplateContext assembles the FR-scoped TemplateContext for a
// boundary-scope FeatureRecipe instance: the boundary pillars (.boundary) plus
// the boundary attachment's .infra, with .values empty per the Release
// pillar-split (Aggregate authors values; Release authors infra). Shared by the
// pre-loop provides pre-pass and the Phase B.6 dispatch so both evaluate the
// FR's templates against an identical context.
//
// ownAssemblyPath is where this attachment's own output is assembled
// ([assemblyPathsByBoundaryFR]); the boundary view carries it as its assembly path.
func buildBoundaryFRTemplateContext(
	allProvides NodeProvides,
	fr *types.FeatureRecipe,
	frInstance string,
	key boundaryFRKey,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	assemblyPaths map[string]string,
	ownAssemblyPath string,
) (*types.TemplateContext, error) {
	boundaryCtx, err := buildBoundaryContext(allProvides, key.BoundaryPath, graph, release, boundaryAnnotations, assemblyPaths, ownAssemblyPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: key.BoundaryPath}, fmt.Sprintf("boundary-scope FR %q: build context for boundary %q", frInstance, key.BoundaryPath))
	}
	feat, found := lookupBoundaryFeatureAttachment(graph.Spec.BoundaryFeatures, key.BoundaryPath, frInstance)
	if !found {
		// The boundary doesn't carry the attachment directly. Defensive: the
		// cascade resolution that reaches this FR-instance is upstream.
		return nil, cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{File: key.BoundaryPath},
			fmt.Sprintf("boundary-scope FR %q: no BoundaryFeature attachment found at boundary %q", frInstance, key.BoundaryPath),
			nil, nil)
	}
	frCtx := &types.TemplateContext{
		Values:   types.OrderedMap[any]{},
		Infra:    feat.Infra,
		Locals:   types.OrderedMap[string]{},
		Boundary: boundaryCtx,
		Metadata: nil,
	}
	if release != nil {
		frCtx.Metadata = &types.TemplateMetadata{
			ReleaseName:    release.Metadata.Name,
			ReleaseVersion: release.Metadata.Version,
			Environment:    release.Metadata.Environment,
		}
		if graph != nil {
			frCtx.Metadata.AggregateName = graph.Metadata.AggregateName
		}
	}
	return frCtx, nil
}

// PrepopulateBoundaryScopeProvides makes the one evaluation of every
// boundary-scope FeatureRecipe's locals and provides ahead of the per-node render
// loop, storing it in state.Evaluations keyed by (owning-boundary-path,
// instance). It runs before any consumer reads it: a consumer's uses edge carrying
// the attachment during inject rehydration, the consumer's
// .features.<attachment>.provides and an authored inject block on the attachment
// when the node's attachments are resolved, and the .locals the attachment's
// injectMapping reads in Phase A. The Phase B.6 dispatch reads the same evaluation
// back rather than making a second one: both build their context through
// buildBoundaryFRTemplateContext from the same inputs, so the dispatch's
// valuesMapping and source read the locals this evaluation resolved. Walks
// graph.Spec.BoundaryFeatures (the un-folded projection), so no Release re-walk.
//
// The Feature's provides contract is enforced on that one evaluation, positioned
// at the boundary attachment, so a FeatureRecipe that omits a declared key halts
// once rather than once per consumer. catalogLookup is what the contract is read
// from; a lookup with no Feature reader declares no contract.
//
// The evaluation is host-side template work and crosses into no plugin.
//
// declared answers what each boundary attachment's Feature declares sensitive, and
// declaredComponent what a Component type declares — the target of each
// connection the boundary's view lists — so a failing locals or provides template
// reports without such a value; nil declares nothing.
func PrepopulateBoundaryScopeProvides(
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	recipeLookup RecipeLookup,
	catalogLookup resolver.CatalogLookup,
	allProvides NodeProvides,
	state *BoundaryFRState,
	declared func(featureType string) resolver.SensitiveLeaves,
	declaredComponent func(componentType string) resolver.SensitiveLeaves,
) error {
	if graph == nil || state == nil {
		return nil
	}
	lists := listDeclarations{graph: graph, component: declaredComponent, feature: declared}
	for boundaryPath, feats := range graph.Spec.BoundaryFeatures.All() {
		for instName, bf := range feats.All() {
			fr, ok := recipeLookup.FeatureRecipe(instName, bf.Type)
			if !ok || fr.Spec.ExecutionScope != executionScopeBoundary {
				continue
			}
			key := boundaryFRKey{BoundaryPath: boundaryPath, FRInstance: instName}
			frCtx, err := buildBoundaryFRTemplateContext(allProvides, fr, instName, key, graph, release, boundaryAnnotations, state.assemblyPaths, state.ownAssemblyPaths[key])
			if err != nil {
				return err
			}
			state.links.stampBoundaryLinkReads(frCtx)
			if err := evaluateBoundaryFRTemplates(fr, instName, frCtx, state, key,
				boundaryFRKeys(fr, declaredLeavesOf(declared, bf.Type), frCtx.Boundary, lists),
				featureProvidesContractOf(catalogLookup, bf.Type)); err != nil {
				return err
			}
		}
	}
	return nil
}

// evaluateBoundaryFRTemplates makes the one evaluation of a boundary-scope FR's
// locals and provides templates for key, against the FR-scoped context, and
// stores it in state.Evaluations; the resolved locals are set on frCtx either
// way.
//
// The evaluation is made once for the whole build. A call for a key already
// evaluated reads the stored evaluation back — its locals onto frCtx — and
// evaluates nothing, so the provides pre-pass and the feature-rendering dispatch,
// which both reach here for one key against contexts built from the same inputs,
// hold one value between them rather than two that agree. Every consumer the
// boundary holds reads that same stored value through
// [resolveBoundaryScopeFREvaluation].
//
// contract is the provides contract of the Feature the attachment names. It is
// enforced on the evaluation as it is made, positioned at the boundary attachment,
// so a missing key halts once rather than once per consumer.
//
// The locals resolve through evaluateLocalsInOrder — the one home of the locals
// contract — so an entry may read an earlier sibling and a forward or unknown
// sibling reference halts, exactly as on the Recipe, per-component FeatureRecipe
// and attachment FeatureRecipe surfaces. The provides then read the fully resolved
// block, and a provides template naming a local the block does not declare halts
// through the same shared refusal every provides surface applies.
//
// This surface evaluates against *types.TemplateContext rather than the
// locals-specific LocalsContext, and that is a context-SHAPE difference, not a
// contract difference: a boundary-scope FR reads .boundary.* pillars that the
// node-local type does not carry, so the two cannot be one type. The evaluation
// contract — author order, the resolved-so-far prefix published per entry, the
// same reference check, the same two halts — is shared through the loop above and
// is not restated here. Do not "unify" the two context types (it would strip the
// boundary pillars this surface exists to read), and do not read two context types
// as two contracts (there is one, and it lives in evaluateLocalsInOrder).
//
// The FR's valuesMapping is evaluated separately at the dispatch site
// (dispatchBoundaryScopeFR) through the shared ExecutePhaseBForFeature — the
// same evaluator the per-component FR path uses — and its output drives
// engine.Render. A valuesMapping-less FR resolves to the flattened .values
// pillar unchanged, so it renders identically either way.
//
// withheld names what of frCtx an author declared sensitive, so a failing locals
// or provides template reports without such a value.
func evaluateBoundaryFRTemplates(
	fr *types.FeatureRecipe,
	frInstance string,
	frCtx *types.TemplateContext,
	state *BoundaryFRState,
	key boundaryFRKey,
	withheld redact.Keys,
	contract featureProvidesContract,
) error {
	if made, ok := state.Evaluations[key]; ok {
		frCtx.Locals = made.Locals
		return nil
	}
	resolvedLocals, err := evaluateLocalsInOrder(
		fr.Spec.Locals,
		boundaryScopeFRScope,
		func(k string) localsRefSite {
			return localsRefSite{
				Where: fmt.Sprintf("boundary-scope FR %q locals[%q]", frInstance, k),
				File:  fr.Metadata.Name,
				Key:   k,
			}
		},
		func(k string) string { return "fr-local[" + frInstance + "." + k + "]" },
		func(resolved types.OrderedMap[string]) any {
			frCtx.Locals = resolved
			return frCtx
		},
		withheld,
	)
	if err != nil {
		return err
	}
	frCtx.Locals = resolvedLocals

	if err := checkProvidesLocalsReferences(fr.Spec.Provides, resolvedLocals, func(k string) localsRefSite {
		return localsRefSite{
			Where: fmt.Sprintf("boundary-scope FR %q provides[%q]", frInstance, k),
			File:  fr.Metadata.Name,
		}
	}); err != nil {
		return err
	}

	// Evaluate provides in the order the FeatureRecipe declares them, which is the
	// order every consumer reads them in. Uses the same EvalTemplate (Sprig-enabled,
	// missingkey=zero) as per-component FR provides.
	provided := types.OrderedMap[string]{}
	for k, tmplText := range fr.Spec.Provides.All() {
		out, err := EvalTemplate("fr-provides["+frInstance+"."+k+"]", tmplText, frCtx, withheld)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("boundary-scope FR %q provides[%q]", frInstance, k))
		}
		provided.Set(k, out)
	}
	if err := contract.enforce(provided, key.BoundaryPath,
		fmt.Sprintf("boundary %q attachment %q", key.BoundaryPath, frInstance),
		fmt.Sprintf("FeatureRecipe %q", fr.Metadata.Name)); err != nil {
		return err
	}
	state.Evaluations[key] = boundaryFREvaluation{Locals: resolvedLocals, Provides: provided}
	if state.evaluationsMade != nil {
		state.evaluationsMade[key]++
	}
	return nil
}

// buildBoundaryContext walks the Release's boundary tree to find the
// boundary at boundaryPath, then assembles the BoundaryContext that the
// boundary-scope FR template evaluation reads.
//
// The function populates the documented BoundaryContext pillars:
//
//   - Path: boundaryPath as given — the Release boundary path, never the
//     evaluated directory its output is assembled under.
//   - AssemblyPath: assemblyPath as given — the directory the attachment's own
//     output is assembled into, the path of the dispatch key the routing places
//     that output under ([boundaryFRDispatch]).
//   - BoundaryName: the last segment of the boundary path (the map key
//     in release.spec.boundaries).
//   - Components: every node in the boundary subtree (recursive),
//     captured as instance_id → opaque ComponentInfo (resolved type URI,
//     instance ID, provided values).
//   - InternalUsesEdges, OutgoingCrossBoundaryUsesEdges,
//     IncomingCrossBoundaryUsesEdges, CrossBoundaryUsesEdges — derived by
//     intersecting graph.Spec.Edges with subtree node membership, each
//     endpoint's namespace and boundary path read by
//     edgeEndpointNetworkInputs from that endpoint's own placement, and its
//     assembly path read from assemblyPaths.
//   - Labels: union of subtree nodes' Labels with author-order preservation
//     (alphabetical-by-key key order; OrderedMap iteration preserves
//     insertion order).
//   - Annotations: read from boundaryAnnotations[boundaryPath] (the
//     resolver-side cascade-resolved set).
//   - FeatureAttachments: features directly attached to the boundary,
//     parsed from boundary.Features via parseBoundaryFeature equivalent.
//
// assemblyPaths is the render's one answer to where each node's output is
// assembled ([assemblyPathsByNode]); an endpoint it holds nothing for carries an
// empty assembly path. assemblyPath is the attachment's own answer
// ([assemblyPathsByBoundaryFR]); empty where there is none.
func buildBoundaryContext(
	allProvides NodeProvides,
	boundaryPath string,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	assemblyPaths map[string]string,
	assemblyPath string,
) (*types.BoundaryContext, error) {
	if release == nil {
		return nil, cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{}, "internal error while building a boundary render context: no Release was supplied", nil, nil)
	}
	parts := strings.Split(boundaryPath, "/")
	if _, ok := findBoundaryAtPath(release.Spec.Boundaries, parts); !ok {
		return nil, cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{File: boundaryPath}, fmt.Sprintf("boundary %q is not declared in the Release", boundaryPath), nil, nil)
	}

	bc := &types.BoundaryContext{
		Path:         boundaryPath,
		AssemblyPath: assemblyPath,
		BoundaryName: parts[len(parts)-1],
	}
	if bc.BoundaryName == "" && len(parts) > 1 {
		bc.BoundaryName = parts[len(parts)-2]
	}

	// Subtree node membership: every node whose BoundaryPath is the
	// boundary's path or descends from it.
	subtreeMembers := map[string]bool{}
	componentsMap := types.OrderedMap[types.ComponentInfo]{}
	if graph != nil {
		// Populate in alphabetical-by-nodeID order for output determinism;
		// graph.Spec.Nodes.All() preserves author order, but the subtree
		// filter collapses across cross-boundary nodes, so a deterministic
		// alphabetical order at the boundary view is the safer choice.
		nodeIDs := graph.Spec.Nodes.Keys()
		sort.Strings(nodeIDs)
		for _, nodeID := range nodeIDs {
			node, _ := graph.Spec.Nodes.Get(nodeID)
			if isInSubtree(node.BoundaryPath, boundaryPath) {
				subtreeMembers[nodeID] = true
				// The KEY stays the identity the graph is keyed by and the entry's
				// instance_id is encoded, and the two answer different questions: the
				// key is how a template finds a component, written the way its author
				// can read it, while instance_id is what a template names a resource
				// after and so has to be a name every target admits. A key a template
				// could not spell would be no key at all, and an unencoded name would
				// carry the identity join into a resource name.
				componentsMap.Set(nodeID, types.ComponentInfo{
					InstanceID:      encodeNodeIdentity(nodeID),
					Component:       node.Component,
					ResolvedVersion: node.ResolvedVersion,
					BoundaryPath:    node.BoundaryPath,
				})
			}
		}
	}
	bc.Components = componentsMap

	// Edge enumeration.
	if graph != nil {
		for _, edge := range graph.Spec.Edges {
			srcIn := subtreeMembers[edge.Source]
			tgtIn := subtreeMembers[edge.Target]
			edgeView := EnrichedEdgeViewFromIREdge(edge, allProvides)
			// Per-endpoint network inputs (pure data): the endpoints' boundary
			// paths, assembly paths and raw aggrega.ai/namespace annotation
			// values, so a provider engine can resolve each endpoint's network and
			// decide whether the edge crosses a seam. No interpretation here
			// (Invariant 1/6).
			edgeView.SourceBoundaryPath, edgeView.SourceNamespace = edgeEndpointNetworkInputs(edge.Source, graph)
			edgeView.TargetBoundaryPath, edgeView.TargetNamespace = edgeEndpointNetworkInputs(edge.Target, graph)
			edgeView.SourceAssemblyPath = assemblyPaths[edge.Source]
			edgeView.TargetAssemblyPath = assemblyPaths[edge.Target]
			switch {
			case srcIn && tgtIn:
				bc.InternalUsesEdges = append(bc.InternalUsesEdges, edgeView)
			case srcIn && !tgtIn:
				bc.OutgoingCrossBoundaryUsesEdges = append(bc.OutgoingCrossBoundaryUsesEdges, edgeView)
				bc.CrossBoundaryUsesEdges = append(bc.CrossBoundaryUsesEdges, edgeView)
			case !srcIn && tgtIn:
				bc.IncomingCrossBoundaryUsesEdges = append(bc.IncomingCrossBoundaryUsesEdges, edgeView)
				bc.CrossBoundaryUsesEdges = append(bc.CrossBoundaryUsesEdges, edgeView)
			}
		}
	}

	// Labels: union across subtree nodes (alphabetical-by-key for
	// determinism since multiple nodes contribute).
	labels := types.OrderedMap[string]{}
	if graph != nil {
		labelKeys := map[string]string{}
		for _, node := range graph.Spec.Nodes.All() {
			if !subtreeMembers[graph.Metadata.AggregateName+"-"+node.Component] {
				// Subtree filter applies to nodeID, not component name;
				// re-iterate via Keys() and Get().
				_ = node
			}
		}
		// Re-iterate alphabetically over subtree node IDs to populate
		// labels in a deterministic order.
		nodeIDs := graph.Spec.Nodes.Keys()
		sort.Strings(nodeIDs)
		for _, nid := range nodeIDs {
			if !subtreeMembers[nid] {
				continue
			}
			node, _ := graph.Spec.Nodes.Get(nid)
			for k, v := range node.Labels.All() {
				if _, exists := labelKeys[k]; !exists {
					labelKeys[k] = v
					labels.Set(k, v)
				}
			}
		}
	}
	bc.Labels = labels

	// Annotations from the resolver-side cascade.
	if ann, ok := boundaryAnnotations[boundaryPath]; ok {
		bc.Annotations = ann
	}

	// Feature attachments: the boundary's Feature attachments read from the
	// IR projection (the IR builder parsed them once at build; no Release
	// re-walk here).
	//
	// A boundary attachment carries type and infra and nothing else, because
	// IRBoundaryFeature declares nothing else: values are absent by the same
	// deliberate design its own documentation states, and resolved_version,
	// labels and provides never reach this site at all. So they are left unset
	// here rather than defaulted — a boundary template sees them absent, which
	// is the honest reading of a fact the boundary projection does not carry.
	// The per-node projection is where an attachment is read with its version,
	// labels and published provides.
	if graph != nil {
		if inner, ok := graph.Spec.BoundaryFeatures.Get(boundaryPath); ok {
			for fname, bf := range inner.All() {
				bc.FeatureAttachments = append(bc.FeatureAttachments, types.TemplateAttachment{
					Type:   bf.Type,
					Values: types.OrderedMap[any]{},
					Infra:  bf.Infra,
				})
				_ = fname
			}
		}
	}

	return bc, nil
}

// EnrichedEdgeViewFromIREdge converts an IREdge to a renderer-side
// EnrichedEdge view. The boundary-scope FR templates consume edges via
// the same EnrichedEdge shape used elsewhere in the renderer; this
// converter projects the IR edge's Source/Target/Socket/Alias onto the
// renderer view so templates can reference `.boundary.*_uses_edges[].
// target` etc.
func EnrichedEdgeViewFromIREdge(edge types.IREdge, allProvides NodeProvides) types.EnrichedEdge {
	view := types.EnrichedEdge{
		Source:        edge.Source,
		Target:        edge.Target,
		Socket:        edge.Socket,
		OriginFeature: edge.OriginFeature,
		Inject:        edge.Inject,
	}
	if p, ok := allProvides[edge.Target]; ok {
		view.Provides = p
	}
	return view
}

// edgeEndpointNetworkInputs returns the boundary path and raw
// aggrega.ai/namespace annotation value for an edge endpoint node, as pure
// data for a provider engine's network-identity resolution. Empty strings when
// the node or annotation is absent. The core does not resolve these into a
// network name — that (sanitize + fallback + compare) is provider-domain.
//
// It is the one place a connection endpoint's placement is read, and both views
// that carry one call it: the boundary-scope edge views, for each end of an edge,
// and the connections made to a node, for the connecting end (AssembleInboundEdges).
//
// The namespace is the endpoint's OWN annotation — the set its own render is handed
// (nodePlatformAnnotations: what the Aggregate wrote for it, with its boundaries'
// annotations cascaded down and its own Release entry's layered over) — and never
// its boundary's set alone. A component whose Aggregate or Release entry sets or
// clears aggrega.ai/namespace renders into that namespace, so a value read from the
// boundary would name a namespace the endpoint does not run in, and the provider's
// network placement, which reads the node's own set, would disagree with whatever
// this value was used to decide.
func edgeEndpointNetworkInputs(nodeID string, graph *types.IRGraph) (boundaryPath, namespace string) {
	if graph == nil {
		return "", ""
	}
	n, ok := graph.Spec.Nodes.Get(nodeID)
	if !ok {
		return "", ""
	}
	platform := nodePlatformAnnotations(n)
	namespace, _ = platform.Get(AnnotationNamespace)
	return n.BoundaryPath, namespace
}

// findBoundaryAtPath traverses release.Spec.Boundaries by the path
// segments. Returns (boundary, true) on hit; (zero, false) on miss.
func findBoundaryAtPath(boundaries types.OrderedMap[types.Boundary], parts []string) (types.Boundary, bool) {
	if len(parts) == 0 {
		return types.Boundary{}, false
	}
	current := boundaries
	var found types.Boundary
	for _, part := range parts {
		b, ok := current.Get(part)
		if !ok {
			return types.Boundary{}, false
		}
		found = b
		current = b.Boundaries
	}
	return found, true
}

// isInSubtree reports whether candidate is the boundaryPath itself or
// descends from it. Subtree membership is exact-match-or-prefix-with-slash.
func isInSubtree(candidate, boundaryPath string) bool {
	if candidate == boundaryPath {
		return true
	}
	return strings.HasPrefix(candidate, boundaryPath+"/")
}

// findFRAttachmentBoundary walks from componentBoundaryPath up the boundary
// tree (toward the root) and returns the path of the FIRST ancestor (or
// componentBoundaryPath itself) whose `Features` map includes frInstance.
// Used by Phase B.6 to compute the dedup key for boundary-scope FRs whose
// attachment lives on an ancestor of the component's containing boundary
// (Tier-4 propagation in the IR builder cascades the feature down to every
// component in the subtree, but the orchestrator-side dedup key is the
// owning boundary, not the component's containing boundary).
//
// Falls back to componentBoundaryPath when no ancestor carries the
// attachment — the boundary-scope dispatch then surfaces a precise
// "no BoundaryFeature attachment" error rather than silently misattributing.
func findFRAttachmentBoundary(boundaryFeatures types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]], componentBoundaryPath, frInstance string) string {
	owning, held := declaringAncestorBoundary(componentBoundaryPath, func(candidate string) bool {
		_, hasFeat := lookupBoundaryFeatureAttachment(boundaryFeatures, candidate, frInstance)
		return hasFeat
	})
	if !held {
		return componentBoundaryPath
	}
	return owning
}

// declaringAncestorBoundary walks from the boundary a component sits in toward
// the root and returns the first scope — that one, or one enclosing it — the
// caller's predicate reports a declaration at, and whether any did.
//
// It is the ONE home of that walk, and both surfaces asking it route through
// here: the dispatch above, which needs the scope a boundary-scope recipe
// generates for, and the rule that refuses an attachment no scope declares. Two
// walks would be free to differ on which ancestors they visit and on how a path
// is split into them — and the disagreement would surface as a rule admitting a
// coordinate the dispatch then cannot resolve, which reports approval for a
// build that halts.
//
// The predicate is the caller's because the two read a declaration from
// different products: the dispatch reads the finalised graph's projection, and a
// rule reads the facts it is handed. What the two share is the walk, not the
// lookup, so only the walk lives here.
//
// A component the Release placed in no scope has no ancestor to walk, and the
// empty path is reported as no scope rather than as a scope named by the empty
// string — a boundary whose path is empty is one the compiler synthesized, which
// the operator declared nothing at.
func declaringAncestorBoundary(componentBoundaryPath string, declares func(boundary string) bool) (string, bool) {
	segments := splitBoundaryPath(componentBoundaryPath)
	for i := len(segments); i >= 1; i-- {
		candidate := strings.Join(segments[:i], "/")
		if declares(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// lookupBoundaryFeatureAttachment finds the BoundaryFeature with the given
// instance name on the boundary at boundaryPath. Returns (zero, false)
// when the boundary or feature instance is absent. The boundary's
// Features map is OrderedMap[any]; this function parses the raw
// payload into a typed BoundaryFeature.
func lookupBoundaryFeatureAttachment(boundaryFeatures types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]], boundaryPath, frInstance string) (types.IRBoundaryFeature, bool) {
	inner, ok := boundaryFeatures.Get(boundaryPath)
	if !ok {
		return types.IRBoundaryFeature{}, false
	}
	return inner.Get(frInstance)
}

// resolveBoundaryScopeFREvaluation walks the consumer's BoundaryPath up the
// boundary tree to find the boundary holding the FR-instance attachment, and
// returns that boundary's one evaluation of the attachment's locals and provides
// + true on first match.
//
// Cascade walk: split consumer's boundary path on "/"; iterate from full
// path back to root; at each level, check state.Evaluations for
// (level, frInstance). Return on first hit.
//
// It is the one lookup every consumer-side reader of a boundary-scope
// attachment goes through, so they all read the same value:
//
//   - RehydrateInjectFromProvides, for a per-component uses edge whose
//     OriginFeature names a boundary-scope FeatureRecipe: the edge carries
//     OriginFeatureInstance (the per-attachment instance name that
//     disambiguates two attachments of the same FR type on one boundary), the
//     FR's boundary scope is confirmed via RecipeLookup, and the resolved
//     provides are merged over the per-component targetProvides feeding
//     resolveInjectKeys;
//   - ResolveAttachmentProvides, for the consumer's
//     .features.<attachment>.provides, and ResolveAttachmentInject, for an
//     inject block authored on the attachment;
//   - resolveFeatureRecipes, for the .locals the attachment's injectMapping
//     reads in Phase A.
//
// A consumer boundary path with no matching attachment resolves nothing.
func resolveBoundaryScopeFREvaluation(
	state *BoundaryFRState,
	consumerBoundaryPath, frInstance string,
) (boundaryFREvaluation, bool) {
	if state == nil {
		return boundaryFREvaluation{}, false
	}
	parts := strings.Split(consumerBoundaryPath, "/")
	for i := len(parts); i > 0; i-- {
		candidate := strings.Join(parts[:i], "/")
		key := boundaryFRKey{BoundaryPath: candidate, FRInstance: frInstance}
		if made, ok := state.Evaluations[key]; ok {
			return made, true
		}
	}
	return boundaryFREvaluation{}, false
}

// boundaryScopeAttachmentEvaluation answers, for one attachment a consumer
// carries, whether its FeatureRecipe renders once for its boundary and, if so,
// the one evaluation the boundary holding it made. A consumer's attachment
// surfaces ask it before evaluating anything, so none of them evaluates a
// boundary-scope FeatureRecipe's locals or provides itself.
//
// boundary is true for every boundary-scope FeatureRecipe; found is false where
// no boundary holding the consumer has an evaluation for the instance, which a
// render reaches only where no boundary declares the attachment — a build the
// rule over a boundary-scope attachment's declaration refuses before the node
// walk — or where no boundary-scope pre-pass ran. Such an attachment reads no
// locals and no provides rather than being evaluated per consumer.
func boundaryScopeAttachmentEvaluation(
	fr *types.FeatureRecipe,
	state *BoundaryFRState,
	consumerBoundaryPath, instanceName string,
) (made boundaryFREvaluation, boundary, found bool) {
	if fr == nil || fr.Spec.ExecutionScope != executionScopeBoundary {
		return boundaryFREvaluation{}, false, false
	}
	made, found = resolveBoundaryScopeFREvaluation(state, consumerBoundaryPath, instanceName)
	return made, true, found
}
