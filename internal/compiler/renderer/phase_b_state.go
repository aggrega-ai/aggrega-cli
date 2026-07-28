// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
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
	// orchestrator's per-boundary file collection; not consumed by Phase B
	// sub-passes today but kept for forward compatibility (B.5 may need
	// it once cross-boundary detection lands).
	BoundaryPath string

	// RecipePath / RecipeName / RecipeVersion are operator-facing
	// identifiers threaded into validator errors (Workload validation,
	// featuresSchema check, rule 7 collision detection).
	RecipePath    string
	RecipeName    string
	RecipeVersion string

	// ResolvedValues is the values dictionary B.1 produces (or, for
	// sourceless Recipes today, the values context flattened into a plain
	// map). Engines consume this via engine.Render. Populated at B.1; nil
	// before then.
	ResolvedValues map[string]any

	// ParsedWorkload is the typed Workload B.2 produces after yaml.Unmarshal
	// + validator.ValidateWorkload + validator.ValidateWorkloadAttachments all
	// succeed. nil before B.2 and for Recipes that don't declare workload:.
	ParsedWorkload *types.Workload

	// EmittedFiles accumulates the per-input-kind file contributions across
	// B.1 (sourced), B.3 (artifacts; stub today), and B.4 (workload
	// synthesis). Order is preserved (byte-for-byte deterministic output);
	// B.5 detects cross-origin collisions over this list.
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

	// AttachmentProvides is the per-node `slot → resolved fill provides` map the
	// orchestrator resolves once, through the single attachment-provides
	// resolution, before context assembly. The same bag is surfaced to the
	// consumer as `.features.<name>.provides` AND reused at Phase B.6 as the
	// credential FeatureRecipe's `.provides`, so the consumer's read and the
	// rendered Secret derive from one evaluation (no drift). nil when no
	// credential attachment resolves a provides bag.
	AttachmentProvides map[string]types.OrderedMap[string]
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
