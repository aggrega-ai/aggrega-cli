// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// FRPStepInput is the JSON payload a FeatureRecipe-pipeline Wasm guest
// receives for one pipeline step. It carries the cumulative rendered AST
// after every preceding step's patches ([FRPStepInput.AST]), the
// step-local input the Recipe declared for this step (StepInput), the
// render-time [renderer.RenderContext], the step's position in the
// pipeline (StepIndex), and a record of the steps that already ran
// (PrecedingSteps).
//
// The FRP step is a distinct guest contract from the mutator socket
// (topology.MutatorInput / MutatorOutput). The mutator runs once over the
// whole topology graph and returns opaque label patches; an FRP step runs
// per inline `pipeline:` entry declared in a Recipe or FeatureRecipe,
// receives the cumulative AST plus its step input, and returns structured
// RFC 6902 patches plus side effects and diagnostics. Different attachment
// point, different payload — do not conflate the two.
//
// AST is opaque to the Go host: it is the in-progress rendered document
// the guest parses and patches. The host pipes the bytes through without
// interpreting them — it does not infer meaning from, or validate, the
// AST's content.
//
// StepInput is a types.OrderedMap so the step's rendered `with` block
// reaches the wire in author order and the payload is byte-identical
// across runs of identical input. A plain Go map would randomise that
// order and break the determinism contract the rest of the pipeline
// upholds.
//
// Context reuses [renderer.RenderContext] — the same render-time context
// shape every plugin boundary carries — rather than a package-local
// redeclaration, so a render context is one type across every Wasm socket.
//
// **Status: contract definition only — not yet executed.** No production
// caller constructs this payload today; the FRP runtime orchestrator that
// does is later work.
type FRPStepInput struct {
	AST            json.RawMessage        `json:"ast"`
	StepInput      types.OrderedMap[any]  `json:"step_input,omitempty,omitzero"`
	Context        renderer.RenderContext `json:"context"`
	StepIndex      int                    `json:"step_index"`
	PrecedingSteps []PrecedingStep        `json:"preceding_steps,omitempty"`
}

// PrecedingStep records one pipeline step that ran before the current
// step, so a guest can inspect what came before it: the step's pipeline
// index, the Wasm plugin URI that implemented it, and the side effects
// that step emitted.
type PrecedingStep struct {
	Index       int          `json:"index"`
	PluginURI   string       `json:"plugin_uri"`
	SideEffects []SideEffect `json:"side_effects,omitempty"`
}

// FRPStepOutput is the JSON payload a FeatureRecipe-pipeline Wasm guest
// returns for one pipeline step: the RFC 6902 patches the orchestrator
// applies to the cumulative AST (ASTPatches), the side effects this step
// produced (SideEffects), and any diagnostics the guest raised
// (Diagnostics).
//
// ASTPatches are structured [JSONPatchOp] values — the guest names each
// op, path, and value explicitly. This is deliberately a different shape
// from the mutator socket's opaque patch slice
// (topology.MutatorOutput.Patches, []json.RawMessage): an FRP step's
// patches are inspected and inverted by the orchestrator (the patch
// apply + rollback engine is later work), so the operations are explicit
// here rather than opaque. The two patch surfaces serve different guest
// contracts and are intentionally not unified.
//
// Diagnostics reuses [renderer.Diagnostic] so a diagnostic carries one
// shape across every plugin boundary.
//
// **Status: contract definition only — not yet executed.** The
// orchestrator that applies these patches and emits these side effects is
// later work.
type FRPStepOutput struct {
	ASTPatches  []JSONPatchOp         `json:"ast_patches,omitempty"`
	SideEffects []SideEffect          `json:"side_effects,omitempty"`
	Diagnostics []renderer.Diagnostic `json:"diagnostics,omitempty"`
}

// JSONPatchOp is one RFC 6902 JSON-Patch operation an FRP step emits
// against the cumulative AST. Op is one of the six RFC 6902 operations —
// add, remove, replace, move, copy, test. Path is an RFC 6901 JSON
// Pointer to the target location. Value carries the operand for add,
// replace, and test, and is omitted otherwise. From carries the source
// pointer for move and copy, and is omitted otherwise.
//
// Value is typed `any` because a patch value is an arbitrary JSON document
// fragment the guest authored, not an author-ordered key set the host
// owns; the host pipes it through opaquely. If the key order of a patch
// value ever needs to be deterministic, that is the emitting guest's
// concern, not the host wire type's.
//
// This is the type only. The engine that applies a patch to the AST and
// inverts it for rollback on a failed step is later work, not this
// package.
type JSONPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
	From  string `json:"from,omitempty"`
}

// SideEffect is one effect a FeatureRecipe-pipeline step produces beyond
// patching the AST. Kind names the effect — emit_crd or emit_file. Kind is
// an open string rather than a closed Go enum so a future effect kind
// extends the contract without an ABI break.
//
// When Kind is emit_crd, CRD carries the CRD document the step emits and
// File is omitted. When Kind is emit_file, File carries the file to write
// ([SideEffectFile]) and CRD is omitted.
//
// The emit_crd effect is how a pipeline step honours compile-time purity
// for work that can only happen at runtime. A fetcher step does NOT fetch
// a secret at compile time: the fetcher writes a CRD that drives a runtime
// controller to fetch a credential from a vault at runtime. The guest's
// output is deterministic — the same input produces the same CRD — and the
// CRD's controller does the fetching at deploy time, so the build output
// stays reproducible.
//
// CRD is json.RawMessage: the CRD document is opaque to the Go host, which
// pipes it through without interpreting it.
type SideEffect struct {
	Kind string          `json:"kind"`
	CRD  json.RawMessage `json:"crd,omitempty"`
	File *SideEffectFile `json:"file,omitempty"`
}

// SideEffectFile is the file an emit_file [SideEffect] writes: the target
// Path and the file Content.
type SideEffectFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
