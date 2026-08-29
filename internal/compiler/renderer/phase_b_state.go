// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PhaseBState is the accumulator threaded through Phase B sub-passes B.0
// through B.6. It carries the parsed Recipe (read by every sub-pass), the
// rendered values dictionary (B.1 produces; engines consume), the parsed
// Workload (B.2 produces; B.4 hands to the synthesizer), the accumulated
// emitted files (B.1, B.3, B.4 contribute), and the high-water-mark phase
// indicator that the context-availability matrix tests assert against.
//
// The state is owned by the orchestrator's per-node loop; sub-pass functions
// receive it by pointer and mutate it in place.
type PhaseBState struct {
	// Recipe is the per-node Recipe. Read at every sub-pass entry.
	Recipe *types.Recipe

	// NodeID is the flattened instance ID (e.g., "billing.api"). Threaded
	// into error messages and used by the eventual collision-detector.
	NodeID string

	// BoundaryPath is the boundary the node belongs to. Used by the
	// orchestrator's per-boundary file collection and not consumed by the
	// render's own passes.
	BoundaryPath string

	// RecipePath / RecipeName / RecipeVersion are operator-facing
	// identifiers threaded into validator errors (Workload validation,
	// featuresSchema check, filename-collision detection).
	RecipePath    string
	RecipeName    string
	RecipeVersion string

	// ResolvedValues is the values dictionary B.1 produces (or, for
	// sourceless Recipes today, the values context flattened into a plain
	// map). Engines consume this via engine.Render. Populated at B.1; nil
	// before then.
	ResolvedValues map[string]any

	// ValuesAreDeclaredLeaves reports that ResolvedValues' keys ARE the authored
	// values-pillar leaf paths, which holds exactly when the Recipe declares no
	// values mapping. A mapping is one whole-document template, so with one
	// declared the keys are the template's own invention and no declaration names
	// any of them. Recorded by the sub-pass that resolves the values so no reader
	// has to re-answer the question from the Recipe. False before B.1, which is
	// the safe reading: a consumer joining a declaration to one of these keys
	// declines the join rather than making it on an unproven spelling.
	ValuesAreDeclaredLeaves bool

	// ParsedWorkload is the typed Workload B.2 produces after yaml.Unmarshal
	// + validator.ValidateWorkload + validator.ValidateWorkloadAttachments all
	// succeed. nil before B.2 and for Recipes that don't declare workload:.
	ParsedWorkload *types.Workload

	// DisplayWorkload is the rendered workload a display SHOWS, which is not the
	// document the build emitted: it is the same workload template evaluated a
	// second time against a context whose declared-sensitive data reads as the
	// replacement text. It never reaches an emitted file — the only reader is the
	// display context the capture is handed.
	//
	// nil is the ordinary state, and covers three different situations that need
	// no re-render: nothing is declared sensitive, no capture is live, or the
	// workload template reads nothing withheld. It is also nil when the re-render
	// could not be produced, which Redact.Workload then records as unavailable so
	// the display says so instead of showing the emitted document.
	DisplayWorkload *types.Workload

	// EmittedFiles accumulates this render's per-input-kind file
	// contributions: the source render, the artifacts walk, workload
	// synthesis, and an own-scope pipeline's EMITs. Order is preserved
	// (byte-for-byte deterministic output). Once the accumulation is
	// complete it is reduced to the projection the filename-collision rule
	// reads, which is where two blocks meeting on one name is refused.
	EmittedFiles []EmittedFile

	// PhaseReached records the last completed sub-pass index (0..6).
	//
	// Test instrumentation only — production sub-pass logic does NOT branch
	// on this value. Sub-passes write it on completion; only the context-
	// availability matrix tests (phase_b_subpasses_test.go) read it.
	// Future contributors: do not start gating production behavior on
	// PhaseReached. If a real pipeline coordinator needs phase-aware
	// routing, model it as a separate field with explicit pipeline
	// semantics.
	PhaseReached int

	// Warnings is a per-node accumulator for non-fatal advisories any
	// Phase B sub-pass wants to surface to the build's RenderResult.Warnings
	// stream. The orchestrator drains state.Warnings into result.Warnings
	// at the END of the per-node Phase B sequence (after phaseB6 returns)
	// so any sub-pass appending here — present today: phaseB3's helm-chart
	// provenance-skip notice — and any future sub-pass appending later in
	// the pipeline both reach the result. Drain position is load-bearing.
	Warnings []string

	// Diagnostics is the per-node accumulator for the structured, coded
	// advisories the render raises — the unsupported-field notices a target
	// emits for an authored field it cannot honor, and the sensitive-by-value
	// notices attachment delivery raises — drained into result.Diagnostics
	// alongside Warnings at the end of the per-node sequence. Kept separate from
	// Warnings because each entry carries a stable code, which is what lets a
	// display surface filter one class without touching another and lets a
	// downstream consumer match them structurally.
	Diagnostics []Diagnostic

	// DerivedKeys is the per-node accumulator for every derived name this node is
	// answerable for, keyed by the space it was derived into: the names the node's
	// provider guest declares, under the guest's own space names, and the names the
	// compiler derived for the node itself — a component-identity label value for
	// an attachment — under a space the compiler names. A node can render across
	// more than one crossing — a source render, a workload synthesis, an artifacts
	// placement, an attachment's own render — and each returns its own declaration,
	// so the declarations merge here: a space already present gains the new
	// crossing's names rather than replacing them. The orchestrator moves the
	// merged set into the node's dispatch group, where duplicates across
	// contributors are checked once every contributor has rendered.
	//
	// 🔴 The move happens after the node's ATTACHMENTS have rendered, and it has to:
	// an attachment's render declares into this accumulator, so reading it any
	// earlier snapshots it before those declarations arrive and drops them without
	// a trace. Empty for a node that derived nothing and whose guest declared
	// nothing, which leaves the check inert for it.
	DerivedKeys types.OrderedMap[[]string]

	// DerivedKeyRepeats carries the names one of this node's declarations held
	// more than once inside a single one of its key spaces. It travels beside
	// DerivedKeys rather than inside it because the merge above is what makes it
	// unrecoverable: once two crossings' names sit in one list, a name declared
	// twice by one crossing and a name restated at two crossings are the same
	// pair of entries, and only the first is a failure. Recorded as each
	// declaration arrives, and moved into the node's dispatch group alongside the
	// declarations themselves.
	DerivedKeyRepeats []derivedKeyRepeat

	// Trace is the per-node trace.Writer threaded from the orchestrator.
	// nil-safe: a no-op writer when --trace is off, an async fs writer
	// when --trace is on. Sub-passes call state.Trace.Write directly to
	// emit per-pass snapshots; the trace package handles serialization
	// and async file I/O. The orchestrator owns Close().
	Trace trace.Writer

	// Redact names the node's declared-sensitive data, assembled once by the
	// orchestrator — where the catalog and the node's attachments are both in
	// scope — and read by every sub-pass that captures a trace snapshot.
	// Carrying the assembled set rather than its inputs is what keeps
	// sensitivity derived in one place: a sub-pass substitutes a placeholder,
	// it never decides what is sensitive. The zero value redacts nothing.
	Redact redact.Keys

	// MappingLibraries is the resolved MappingLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. B.1
	// resolves a Recipe valuesMapping's bare-name `$import: <Library>#/mappings/<entry>`
	// against it. The orchestrator populates it once per node from the
	// RendererInput; nil when no MappingLibraries are present (a non-library
	// valuesMapping never reads it).
	MappingLibraries map[string]types.MappingLibrary

	// WorkloadLibraries is the local-catalog WorkloadLibrary set, keyed by bare
	// name. B.2 resolves a Recipe workload's bare-name
	// `$import: <Library>#/workloads/<entry>` against it. The orchestrator
	// populates it once per node from the RendererInput; nil when no
	// WorkloadLibraries are present (a non-library workload never reads it).
	WorkloadLibraries map[string]types.WorkloadLibrary

	// ArtifactsLibraries is the local-catalog ArtifactsLibrary set, keyed by bare
	// name. B.3 resolves a Recipe / FeatureRecipe artifacts entry's bare-name
	// `artifacts.<name>: { $import: <Library>#/artifacts/<entry> }` against it. The
	// orchestrator populates it once per node from the RendererInput; nil when no
	// ArtifactsLibraries are present (a non-library artifacts entry never reads it).
	ArtifactsLibraries map[string]types.ArtifactsLibrary

	// hydrated is the render's retained set of per-node mid-render projections,
	// carried here so the artifacts walk can record the template text standing at
	// each entry — the one value whose recording site is inside that walk, because
	// that is where an imported or bundle-supplied entry resolves to its content.
	//
	// It is unexported and is set only for a node's own base-Recipe state. An
	// attachment's render builds its own state around a synthesized Recipe and
	// leaves this nil, which is what keeps an attachment's artifact templates out
	// of a value declared over a node's own Recipe — the two would otherwise
	// collide under one node identity. nil records nothing.
	hydrated *hydratedProjections

	// attachmentHydrated is the seam through which an attachment's render records
	// the state it reaches, under the carrier that attachment was declared
	// against.
	//
	// It is set INSTEAD of the retention above and never beside it. The two are the
	// same mechanism reached from carriers that cannot share a key space: an
	// attachment's render walks the same producers under the host component's node
	// identity, so a state carrying the node retention would file the attachment's
	// entries under the host — where nothing tells them from the host Recipe's own,
	// and where they would answer a fact declared over it.
	//
	// The carrier travels on the state rather than being derived at the recording
	// site because it is not derivable there: the node identity an attachment's
	// render carries is the host's on one carrier and a composite marker built for
	// dispatch uniqueness on the other, and neither names the attachment.
	//
	// It carries the presentation over that retention as well as the retention
	// itself, so the walk that records an attachment's facts is the walk that can
	// read them back. That is one value rather than two deliberately: a render
	// given the recording and not the presentation would record facts nothing is
	// then asked about, which is a rule invoked zero times and green under every
	// proof over the same build.
	//
	// nil records nothing, which is the state of a node's own render and of a
	// render whose caller assembled its own input.
	attachmentHydrated attachmentCarrier

	// hydratedFacts presents, as named facts, the state a node reaches before its
	// artifact bytes exist. It is carried here so the rule declared over those
	// facts can be asked about an artifact entry at the point the entry's text has
	// been resolved and nothing has evaluated it yet — the only point where both
	// halves of that question are answerable.
	//
	// It reads the retention above by pointer rather than holding a copy, so it
	// answers from whatever this walk has recorded by the time it is asked. That
	// is what lets the entry recorded on the line above be the entry the rule is
	// then invoked over.
	//
	// It is unexported and, like the retention, is set only for a node's own
	// base-Recipe state. An attachment's render builds its own state and receives
	// neither, so the value declared over node-keyed facts contributes no finding
	// there — the values declared over that render's own carrier do, reading the
	// presentation the carrier holds. The two absences are one arrangement and are
	// set together for that reason: a walk that records the facts must also be
	// able to read them.
	hydratedFacts constraint.HydratedView

	// emitted is the render's retained set of per-carrier emitted-file projections,
	// and emittedFacts presents it as named facts.
	//
	// They travel on a node's own state for one reason: the carrier an attachment's
	// render is given is built HERE, from the host node's state, and a carrier must
	// carry both stages' products or it would record an emission nothing is then
	// asked about. A node's own emission is recorded by the orchestrator from the
	// render input rather than through these, so what these two exist for is to
	// reach the carrier construction sites.
	//
	// They are set together, on the reading the hydrated pair above states: a walk
	// that records facts must also be able to read them, and a state given one
	// without the other would leave a rule invoked zero times — which reads exactly
	// like a rule that ran and approved.
	emitted      *emittedProjections
	emittedFacts constraint.EmittedView

	// callerPresentationSupplied reports whether the render's CALLER handed in the
	// presentation this render's view chain extends.
	//
	// It exists so a rule that HALTS can tell the two ways its presentation can be
	// absent apart. A caller supplying nothing is asking for a render rather than a
	// build, and a rule invoked over facts nothing produced would be worse than one
	// not invoked. A caller that did supply one, standing over an absent
	// presentation here, is a chain that broke inside this package — and passing
	// that over would turn a build that refuses today into one that renders, which
	// is the silent direction a halting rule must not have.
	callerPresentationSupplied bool

	// artifacts is the render's retained set of per-node artifact projections,
	// carried here so the artifacts walk can record each entry's evaluated bytes
	// at the point that entry becomes part of what the render projects — after the
	// walk has dropped an entry evaluating to nothing and refused a name already
	// emitted, which is what makes the recorded key set the render's rather than
	// the set of entries the Recipe declared.
	//
	// It is unexported and is set only for a node's own base-Recipe state, on the
	// same reading the mid-render retention above follows: an attachment's render
	// walks the same producer under the host node's identity and leaves this nil,
	// which keeps an attachment's artifacts out of a value declared over a node's
	// own Recipe. nil records nothing.
	artifacts *artifactProjections

	// AttachmentProvides is the per-node `slot → resolved fill provides` map the
	// orchestrator resolves once, through the single attachment-provides
	// resolution, before context assembly. The same bag is surfaced to the
	// consumer as `.features.<name>.provides` AND reused at Phase B.6 as the
	// credential FeatureRecipe's `.provides`, so the consumer's read and the
	// rendered Secret derive from one evaluation (no drift). nil when no
	// credential attachment resolves a provides bag.
	AttachmentProvides map[string]types.OrderedMap[string]
}

// capture writes one per-pass display snapshot for this node.
//
// It is the ONE projection call in the renderer, and the singularity is the
// point rather than tidiness: the projection is handed a context whose
// non-key-addressable data has ALREADY been substituted, never a live context
// plus a decision about it. A second call site would be free to pass the live
// context and show a reader the document the build emitted where the display
// promised a re-render — and a projection added later for another parsed
// document would acquire its own parallel path instead of routing through this
// one. TestPillarSnapshotHasOneCallSite holds it.
//
// A build with no live capture returns before the projection is built, so its
// cost is paid only where something reads it.
func (s *PhaseBState) capture(pass trace.Pass, fileSuffix string, ctx *types.TemplateContext) {
	if !trace.Captures(s.Trace) {
		return
	}
	_ = s.Trace.Write(pass, s.NodeID, s.BoundaryPath, fileSuffix,
		trace.PillarSnapshot(s.displayContext(ctx), pass, s.Redact))
}

// recordArtifactTemplateText retains the template text standing at one artifact
// this render's artifacts walk resolved, against whichever carrier the state
// holds.
//
// It is the one routing seam for that recording, so the walk records without
// knowing which carrier it is on. The two arms are exclusive by construction
// rather than by a test here: a node's own state is given the per-node retention
// and an attachment's render the attachment seam, each at its own construction
// site. A state carrying neither records nothing.
func (s *PhaseBState) recordArtifactTemplateText(outputName, text string) {
	if s == nil {
		return
	}
	s.hydrated.recordArtifactTemplate(s.NodeID, outputName, text)
	if s.attachmentHydrated != nil {
		s.attachmentHydrated.recordArtifactTemplate(outputName, text)
	}
}

// recordArtifactContentPin retains the content digest the document this render's
// artifacts belong to recorded for one artifact, against whichever carrier the
// state holds.
//
// It routes exactly as the text recording above does, and for the same reason: the
// walk records without knowing which carrier it is on, and the two arms are
// exclusive by construction rather than by a test here.
//
// It is called only for an entry whose resolved form carries a pin, so what the
// retention holds is the set of entries a comparison is owed for. The gate sits at
// the call site because the walk is the only place that knows which form it
// resolved — a pin recorded for every entry would leave each reader to decide
// whether the comparison applied, and a reader deciding wrongly either refuses a
// legal inline entry or admits an imported one unverified.
func (s *PhaseBState) recordArtifactContentPin(outputName, pin string) {
	if s == nil {
		return
	}
	s.hydrated.recordArtifactContentPin(s.NodeID, outputName, pin)
	if s.attachmentHydrated != nil {
		s.attachmentHydrated.recordArtifactContentPin(outputName, pin)
	}
}

// recordArtifactImportedPath retains the file one artifact of the document this
// render's artifacts belong to was supplied from, against whichever carrier the
// state holds.
//
// It routes exactly as the two recordings above do, and for the same reason: the
// walk records without knowing which carrier it is on, and the two arms are
// exclusive by construction rather than by a test here.
//
// It is called only for an entry the walk resolved from an imported file, which
// is the one authoring form naming a file to open. The gate sits at the call site
// for the reason the pin's does — the walk is the only place that knows which
// form it resolved — and it is the SAME branch, so an entry with a recorded pin
// always has a recorded path.
func (s *PhaseBState) recordArtifactImportedPath(outputName, path string) {
	if s == nil {
		return
	}
	s.hydrated.recordArtifactImportedPath(s.NodeID, outputName, path)
	if s.attachmentHydrated != nil {
		s.attachmentHydrated.recordArtifactImportedPath(outputName, path)
	}
}

// recordAttachmentPillars reduces the mid-render state one attachment's render
// reached to the projection that outlives it.
//
// It is called at the position the node walk records its own: source resolution
// has produced the value the render's Recipe carries, the workload has been
// parsed, and no artifact template has been evaluated. A state that is not an
// attachment's render records nothing.
func (s *PhaseBState) recordAttachmentPillars() {
	if s == nil || s.attachmentHydrated == nil {
		return
	}
	s.attachmentHydrated.recordPillars(s)
}

// recordAttachmentEmission reduces the completed emitted file set of one
// attachment's render to the projection that outlives it, under that render's own
// carrier.
//
// It is the routing seam for that recording, so the render records without knowing
// which carrier it is on — the same shape the artifact-template recording above
// uses. A state that is not an attachment's render records nothing, which is the
// case for a node's own render: its emission is recorded by the orchestrator, at the
// point the node's own set is complete and before the node joins its dispatch group.
func (s *PhaseBState) recordAttachmentEmission() {
	if s == nil || s.attachmentHydrated == nil {
		return
	}
	s.attachmentHydrated.recordEmission(s)
}

// displayContext returns the context the projection is handed: the live one when
// no projection inside it was replaced, and a shallow copy carrying the display
// re-render otherwise.
//
// The copy is what keeps the substitution on the display side of the boundary.
// The context this reads is the one the artifacts render from, so swapping the
// field in place would put a re-rendered document where the emitted one belongs.
// Deriving a context by shallow copy is the established shape in this package.
func (s *PhaseBState) displayContext(ctx *types.TemplateContext) *types.TemplateContext {
	if ctx == nil || s.DisplayWorkload == nil {
		return ctx
	}
	shown := *ctx
	shown.Workload = s.DisplayWorkload
	return &shown
}

// NewPhaseBState constructs a fresh accumulator for one node's Phase B run.
// Operator-facing identifiers (RecipePath / RecipeName / RecipeVersion) and
// node identity are populated up-front; the per-sub-pass mutable fields
// (ResolvedValues, ParsedWorkload, EmittedFiles, PhaseReached) start at
// their zero values.
func NewPhaseBState(recipe *types.Recipe, nodeID, boundaryPath, recipePath string) *PhaseBState {
	state := &PhaseBState{
		Recipe:       recipe,
		NodeID:       nodeID,
		BoundaryPath: boundaryPath,
		RecipePath:   recipePath,
		PhaseReached: -1, // -1 = no sub-pass has completed yet
	}
	if recipe != nil {
		state.RecipeName = recipe.Metadata.Name
		state.RecipeVersion = recipe.Metadata.Version
	}
	return state
}
