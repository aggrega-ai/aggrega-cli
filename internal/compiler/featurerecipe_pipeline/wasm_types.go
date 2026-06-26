// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// DocumentSet is the keyed, scope-rooted set of addressable units a
// pipeline guest receives as its input surface. Own carries the node's own
// rendered output, keyed by unit token. Host carries the rendered output of
// the component a component-scope FeatureRecipe is attached to — the
// cross-entity surface — and is absent for Recipe steps and boundary-scope
// FeatureRecipe steps, which have no host component.
//
// Each entry is a [Unit] — the codec's hybrid view of one piece of output.
// A structured unit carries a Tree (an RFC-6902-addressable view); an opaque
// unit carries only its MediaType and Bytes (no Tree). Opaque units are
// handed to the guest tagged so the guest's view of the output is complete:
// they are SELECT and EMIT targets, but a PATCH against an opaque unit halts
// loudly because it has no addressable tree.
//
// Selection is by content, never by key. The keys are unit tokens — opaque
// positional addresses derived from render-time emitted filenames a plugin
// author cannot know ahead of time. A guest therefore iterates the handed
// set, inspects each structured unit's content (for example `kind ∈
// {Deployment, StatefulSet}`, or "carries a pod template"), selects the
// entries its domain logic targets, learns those keys at runtime, and roots
// its patches at the selected key (`/own/<key>/…` or `/host/<key>/…`). The
// token is only the host's apply-back address; the host hands the whole set
// and applies returned patches by key, never picking a unit or inferring
// intent.
//
// Each root is a types.OrderedMap so the set reaches the wire in a stable
// order and the payload is byte-identical across runs of identical input. A
// plain Go map would randomise the entry order and break the determinism
// contract the rest of the pipeline upholds. A unit's Tree is json.RawMessage:
// the document is opaque to the Go host, which pipes the bytes through
// without interpreting them — the guest, and later the apply engine, parse
// each unit's content.
type DocumentSet struct {
	Own  types.OrderedMap[Unit] `json:"own"`
	Host types.OrderedMap[Unit] `json:"host,omitempty,omitzero"`
}

// FRPStepInput is the JSON payload a FeatureRecipe-pipeline Wasm guest
// receives for one pipeline step. It carries the keyed document set the
// host hands whole ([FRPStepInput.Documents]), the step-local input the
// Recipe declared for this step (StepInput), the render-time
// [renderctx.RenderContext], the step's position in the pipeline
// (StepIndex), and a record of the steps that already ran
// (PrecedingSteps).
//
// The FRP step is a distinct guest contract from the mutator socket
// (topology.MutatorInput / MutatorOutput). The mutator runs once over the
// whole topology graph and returns opaque label patches; an FRP step runs
// per inline `pipeline:` entry declared in a Recipe or FeatureRecipe,
// receives the keyed document set plus its step input, and returns
// key-addressed RFC 6902 patches plus side effects and diagnostics.
// Different attachment point, different payload — do not conflate the two.
//
// Documents is the keyed `{own[,host]}` set (see [DocumentSet]). The host
// hands it whole and is blind to its content; the guest selects the
// documents it targets by content and addresses its patches by key.
//
// StepInput is a types.OrderedMap so the step's rendered `with` block
// reaches the wire in author order and the payload is byte-identical
// across runs of identical input. A plain Go map would randomise that
// order and break the determinism contract the rest of the pipeline
// upholds.
//
// Context reuses [renderctx.RenderContext] — the same render-time context
// shape every plugin boundary carries — rather than a package-local
// redeclaration, so a render context is one type across every Wasm socket.
//
// **Status: contract definition only — not yet executed.** No production
// caller constructs this payload today; the FRP runtime that builds the
// keyed set and dispatches the guest is later work.
type FRPStepInput struct {
	Documents      DocumentSet             `json:"documents"`
	StepInput      types.OrderedMap[any]   `json:"step_input,omitempty,omitzero"`
	Context        renderctx.RenderContext `json:"context"`
	StepIndex      int                     `json:"step_index"`
	PrecedingSteps []PrecedingStep         `json:"preceding_steps,omitempty"`
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

// DocumentScope names the document root a patch or emit targets: own (the
// node's own rendered documents) or host (the documents of the component a
// component-scope FeatureRecipe is attached to). It is the root half of a
// patch's address; the key half is the emitted-filename key within that
// root.
type DocumentScope string

const (
	// DocumentScopeOwn targets the node's own rendered documents.
	DocumentScopeOwn DocumentScope = "own"

	// DocumentScopeHost targets the documents of the component a
	// component-scope FeatureRecipe is attached to. A host-scoped
	// operation from a boundary-scope step has no host documents to act
	// on; the runtime halts such an operation.
	DocumentScopeHost DocumentScope = "host"
)

// FRPStepOutput is the JSON payload a FeatureRecipe-pipeline Wasm guest
// returns for one pipeline step: the RFC 6902 patches the apply engine
// applies to the keyed document set (Patches), the side effects this step
// produced (SideEffects), and any diagnostics the guest raised
// (Diagnostics).
//
// Patches are structured [JSONPatchOp] values — the guest names each
// target document by scope and key, then names the op, path, and value
// explicitly. This is deliberately a different shape from the mutator
// socket's opaque patch slice (topology.MutatorOutput.Patches,
// []json.RawMessage): an FRP step's patches are applied by the apply
// engine and restored by snapshot on a failed step (the apply +
// snapshot-restore engine is later work), so the operations are explicit
// here rather than opaque. The two patch surfaces serve different guest
// contracts and are intentionally not unified.
//
// Diagnostics reuses [renderctx.Diagnostic] so a diagnostic carries one
// shape across every plugin boundary.
//
// **Status: contract definition only — not yet executed.** The apply
// engine that applies these patches and the machinery that emits these
// side effects are later work.
type FRPStepOutput struct {
	Patches     []JSONPatchOp          `json:"patches,omitempty"`
	SideEffects []SideEffect           `json:"side_effects,omitempty"`
	Diagnostics []renderctx.Diagnostic `json:"diagnostics,omitempty"`
}

// JSONPatchOp is one RFC 6902 JSON-Patch operation an FRP step emits
// against one document in the keyed set. Scope and Key name the target
// document — Scope is own or host, Key is the emitted-filename key within
// that root, the key the guest learned at runtime from the document it
// selected by content. Op is one of the six RFC 6902 operations — add,
// remove, replace, move, copy, test. Path is an RFC 6901 JSON Pointer to
// the target location WITHIN the named document. Value carries the operand
// for add, replace, and test, and is omitted otherwise. From carries the
// source pointer for move and copy, and is omitted otherwise.
//
// Value is typed `any` because a patch value is an arbitrary JSON document
// fragment the guest authored, not an author-ordered key set the host
// owns; the host pipes it through opaquely. If the key order of a patch
// value ever needs to be deterministic, that is the emitting guest's
// concern, not the host wire type's.
//
// This is the type only. The engine that applies a patch to the named
// document and restores the set by snapshot on a failed step is later
// work, not this package.
type JSONPatchOp struct {
	Scope DocumentScope `json:"scope"`
	Key   string        `json:"key"`
	Op    string        `json:"op"`
	Path  string        `json:"path"`
	Value any           `json:"value,omitempty"`
	From  string        `json:"from,omitempty"`
}

// SideEffect is one effect a FeatureRecipe-pipeline step produces beyond
// patching an existing document. Kind names the effect — emit_crd or
// emit_file. Kind is an open string rather than a closed Go enum so a
// future effect kind extends the contract without an ABI break.
//
// Scope names the document root the emit contributes to: own (the node's
// own set) or host (the attached host component's set). An emit is
// additive and so works universally, including host-scope on assembler-
// hosted components. An absent Scope means own at apply time; the
// enforcing default is the apply machinery's, not this wire type's.
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
	Kind  string          `json:"kind"`
	Scope DocumentScope   `json:"scope,omitempty"`
	CRD   json.RawMessage `json:"crd,omitempty"`
	File  *SideEffectFile `json:"file,omitempty"`
}

// SideEffectFile is the file an emit_file [SideEffect] writes: the target
// Path and the file Content.
type SideEffectFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
