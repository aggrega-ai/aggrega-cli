// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package stepwalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/truthy"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"go.yaml.in/yaml/v3"
)

// stepSkippedCode is the machine-matchable code the advisory carries when an
// optional pipeline step failed and the walk continued without it.
const stepSkippedCode = "pipeline-step-skipped"

// StepInvoker invokes one pipeline step's guest export with the encoded
// [frp.FRPStepInput] payload — the canonical binary encoding the step's guest reads —
// and returns the guest's raw [frp.FRPStepOutput] bytes. The caller binds this to the
// shared describe() gate's authored-export invocation (resolving and loading the
// plugin upstream); the walk reaches the guest only through this seam, so it
// carries no plugin-resolution or loading concern and is unit-testable with a stub
// invoker.
//
// input is the step input the payload carries, handed over as the value the walk
// rendered rather than left for the invoker to read back out of the payload, so a
// check of the step's declared input contract reads it as it stands. checkOutputs
// is the same hand-over in the other direction: the invoker knows the output
// contract the export it ran declares, and the walk holds the outputs it decoded
// from the guest's answer, so the invoker returns the check and the walk applies
// it to those outputs. Neither direction decodes the payload a second time. A nil
// checkOutputs checks nothing.
//
// A non-nil error is treated as a step failure: a guest trap, a refused
// export, or any invoke-path fault. The walk classifies it strict or
// optional per the step's Optional flag.
type StepInvoker func(ctx context.Context, step types.PipelineStep, input types.OrderedMap[any], payload []byte) (raw []byte, checkOutputs OutputsCheck, err error)

// OutputsCheck checks the outputs a step returned against the output contract the
// export it ran declares, returning the refusal when they do not satisfy it.
type OutputsCheck func(outputs types.OrderedMap[any]) error

// TemplateEvaluator renders a Go template string against a data value and
// returns the rendered text. It is the seam through which the walk reaches
// the renderer's single template primitive without importing the renderer:
// the caller passes the renderer's EvalTemplate, so a step's `with` body
// sees the same funcmap, Sprig functions, and missing-key policy as every
// other Recipe template surface. Its signature matches that primitive
// exactly. A test supplies a stub.
type TemplateEvaluator func(name, text string, data any) (string, error)

// WithEvaluator renders a step's `with` template and reads what it produced into
// dst. It reports a failure to evaluate the template and a failure to read its
// output apart, because the walk names the second as what it is — the rendered
// `with` not reading as a map — and passes the first on as the evaluation
// reported it.
type WithEvaluator func(name, text string, data any, dst *types.OrderedMap[any]) (evalErr, decodeErr error)

// WalkOption adjusts how Walk reads what a step's templates produce.
type WalkOption func(*walkOptions)

// walkOptions is what the options passed to Walk decided.
type walkOptions struct {
	evalWith WithEvaluator
}

// EvaluateWithThrough makes Walk render and read every step's `with` through
// evaluate, in place of rendering it through the walk's evaluator and reading the
// result itself. It is for a caller whose reading of a template's output has to
// know more than the output — what of the data the template read, say — so that
// a failure to read the output can be reported without what the output carried.
// Without it the walk reads the output as YAML into the step input, and so must
// evaluate.
func EvaluateWithThrough(evaluate WithEvaluator) WalkOption {
	return func(o *walkOptions) { o.evalWith = evaluate }
}

// WalkResult is the outcome of a successful pipeline walk: the mutated
// document set, the side effects every step accumulated in order, the
// warnings raised along the way (non-error guest diagnostics plus the loud
// warning each soft-failed optional step produces), and the applied ops per
// touched unit so the caller can reassemble surgically.
//
// AppliedOps records, per scope and unit token, the RFC-6902 ops that
// successfully edited that unit's tree across the whole walk in apply order.
// The caller's codec reassembles a patched unit by editing the byte-spans
// these ops address, so it needs the ops, not only the patched tree — the
// surgical-by-ops reassembly never diffs trees. A unit a step rolled back
// (an optional step that soft-failed) contributes nothing here because its
// snapshot is restored and its ops are dropped.
type WalkResult struct {
	Documents   frp.DocumentSet
	SideEffects []frp.SideEffect
	Warnings    []renderctx.Diagnostic
	AppliedOps  AppliedOps
}

// AppliedOps maps a document scope to the ops that edited each unit token in
// that scope, in apply order across the walk. It is the surgical-reassembly
// input: the caller's codec edits the byte-spans the ops address rather than
// diffing the patched tree against the original.
type AppliedOps struct {
	Own  map[string][]frp.JSONPatchOp
	Host map[string][]frp.JSONPatchOp
}

func (a *AppliedOps) record(scope, key string, op frp.JSONPatchOp) {
	switch scope {
	case frp.DocumentScopeHost:
		if a.Host == nil {
			a.Host = map[string][]frp.JSONPatchOp{}
		}
		a.Host[key] = append(a.Host[key], op)
	default:
		if a.Own == nil {
			a.Own = map[string][]frp.JSONPatchOp{}
		}
		a.Own[key] = append(a.Own[key], op)
	}
}

// Walk executes a node's pipeline steps in order over the keyed document
// set. For each step it snapshots the set, evaluates the step's `with`
// template into the step input, invokes the guest through invoke, and
// applies the returned patches by scope and key; it accumulates side
// effects and threads a record of preceding steps forward.
//
// Failure is strict by default. A step failure — a guest trap or invoke
// fault, a malformed step output, a halt-severity diagnostic, a patch
// targeting an absent document, or a patch the apply engine rejects —
// restores the pre-step snapshot and returns the error, halting the walk.
// A step declaring Optional instead soft-fails: a loud warning is appended
// to the result, the snapshot is restored (discarding the step's output),
// and the walk continues. Rollback is whole-step: a step that fails partway
// through its patch list leaves the set byte-identical to its pre-step
// state.
//
// withCtx is the standard Recipe template context the `with` body evaluates
// against; the caller builds it. renderCtx is threaded into every step's
// [frp.FRPStepInput.Context]. eval is the template evaluator the walk renders
// each step's `with` body through — injected as a seam (the caller passes
// the renderer's EvalTemplate) so this package never imports the renderer
// and the renderer can call Walk without an import cycle; a test supplies a
// stub evaluator. opts adjust how a step's `with` is read; see
// EvaluateWithThrough. Walk does not resolve plugins, construct the document
// set, or enforce provider compatibility — those are the caller's.
func Walk(
	ctx context.Context,
	docs frp.DocumentSet,
	steps []types.PipelineStep,
	invoke StepInvoker,
	withCtx *types.TemplateContext,
	renderCtx renderctx.RenderContext,
	eval TemplateEvaluator,
	opts ...WalkOption,
) (WalkResult, error) {
	var chosen walkOptions
	for _, opt := range opts {
		opt(&chosen)
	}
	var sideEffects []frp.SideEffect
	var warnings []renderctx.Diagnostic
	var preceding []frp.PrecedingStep
	var applied AppliedOps

	// Operate on a private copy so the walk never mutates the caller's set
	// in place — apply writes documents back into the working roots, and a
	// strict halt must leave the caller's input untouched.
	docs = snapshotDocumentSet(docs)

	// Isolate the steps channel to this pipeline. `.steps` is a per-pipeline,
	// within-pipeline channel: a step reads only the outputs of its own
	// pipeline's earlier steps. The caller's context may arrive with foreign
	// `.steps` entries (a shallow copy of a context an earlier pipeline
	// populated) whose backing map the walk's Set calls would also mutate. A
	// fresh map clears any inherited entries and gives the walk its own
	// backing, so a step sees only its own pipeline's siblings and no Set
	// leaks back through a shared context.
	withCtx.Steps = types.OrderedMap[types.StepOutput]{}

	for i, step := range steps {
		// A step gated by a falsy `when` is skipped as a pass-through: the
		// set flows to the next step unchanged, with no snapshot, no invoke,
		// no side effects, and no preceding-step record. The gate decision is
		// the host's alone and is independent of Optional — a broken predicate
		// is an authoring defect, so it halts even for an optional step.
		enabled, err := stepEnabled(step, i, withCtx, eval)
		if err != nil {
			return WalkResult{}, err
		}
		if !enabled {
			continue
		}

		snap := snapshotDocumentSet(docs)

		stepEffects, stepDiags, stepOps, stepOutputs, err := runStep(ctx, &docs, step, i, preceding, invoke, withCtx, renderCtx, eval, chosen)

		// Non-error diagnostics surface regardless of the step's outcome.
		for _, d := range stepDiags {
			if d.Severity != abi.SeverityHalt {
				warnings = append(warnings, d)
			}
		}

		if err != nil {
			docs = snap // whole-step rollback
			// A typed-I/O contract violation is an authoring defect, not a soft
			// step outcome: a step whose rendered `with` input or returned
			// outputs do not satisfy the export's declared schema halts the walk
			// even when the step is Optional — the same way a broken
			// `when`/`with` predicate halts regardless of Optional. Optional
			// tolerates a runtime step failure; it does not tolerate a violated
			// declared contract.
			if step.Optional && !errors.Is(err, frp.ErrPipelineContractViolation()) {
				warnings = append(warnings, renderctx.Diagnostic{
					Severity: abi.SeverityWarn,
					Code:     stepSkippedCode,
					Message:  fmt.Sprintf("optional pipeline step %q skipped: %v", step.ID, err),
				})
				continue
			}
			return WalkResult{}, err
		}

		// The step committed: fold its ops into the walk-level record. A
		// rolled-back step never reaches here, so its ops are dropped with
		// its restored snapshot.
		for _, op := range stepOps {
			applied.record(op.Scope, op.Key, op)
		}

		sideEffects = append(sideEffects, stepEffects...)
		preceding = append(preceding, frp.PrecedingStep{
			Index:       i,
			PluginURI:   step.Plugin,
			SideEffects: stepEffects,
		})

		// Record the committed step's outputs into the inter-step channel so a
		// later step's `when`/`with` can read them as .steps.<id>.outputs.<field>.
		// A rolled-back or soft-failed step never reaches here, so its outputs
		// are dropped with its restored snapshot. A step with an empty ID or no
		// outputs contributes nothing — the channel stays untouched so a
		// no-outputs pipeline renders byte-identically.
		if step.ID != "" && stepOutputs.Len() > 0 {
			out, err := toStepOutput(stepOutputs)
			if err != nil {
				return WalkResult{}, fmt.Errorf("%w: step %q (index %d) record outputs: %w", frp.ErrPipelineStepFailed(), step.ID, i, err)
			}
			withCtx.Steps.Set(step.ID, out)
		}
	}

	return WalkResult{Documents: docs, SideEffects: sideEffects, Warnings: warnings, AppliedOps: applied}, nil
}

// runStep evaluates, invokes, and applies one step, mutating docs in place
// on success. It returns the step's side effects, the guest's diagnostics,
// the ops that successfully edited a unit (so the caller can record them for
// surgical reassembly), and the wire outputs the step returned (so the caller
// can record them into the inter-step channel). A non-nil error means the
// step failed; the caller restores the snapshot. docs may be partially
// mutated when the error is non-nil — the caller's snapshot restore is what
// makes rollback whole-step, and the dropped applied-ops slice is what keeps
// the walk-level record consistent with the restored set.
func runStep(
	ctx context.Context,
	docs *frp.DocumentSet,
	step types.PipelineStep,
	index int,
	preceding []frp.PrecedingStep,
	invoke StepInvoker,
	withCtx *types.TemplateContext,
	renderCtx renderctx.RenderContext,
	eval TemplateEvaluator,
	opts walkOptions,
) ([]frp.SideEffect, []renderctx.Diagnostic, []frp.JSONPatchOp, types.OrderedMap[any], error) {
	stepInput, err := evalWith(step, withCtx, eval, opts)
	if err != nil {
		return nil, nil, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d) `with`: %w", frp.ErrPipelineStepFailed(), step.ID, index, err)
	}

	payload, err := frp.FRPStepInput{
		Documents:      *docs,
		StepInput:      stepInput,
		Context:        renderCtx,
		StepIndex:      index,
		PrecedingSteps: preceding,
	}.EncodeCBOR()
	if err != nil {
		return nil, nil, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d) marshal input: %w", frp.ErrPipelineStepFailed(), step.ID, index, err)
	}

	raw, checkOutputs, err := invoke(ctx, step, stepInput, payload)
	if err != nil {
		return nil, nil, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d) invoke: %w", frp.ErrPipelineStepFailed(), step.ID, index, err)
	}

	// The recipe names the function each step runs, so the export the answer came
	// back from is the step's own, with the pipeline-step type's export as the
	// default a step naming none would run — the same name the invocation used.
	export := wasm.AuthoredExportName(wasm.StepExport, step.Function)
	var out frp.FRPStepOutput
	if err := wasm.DecodeGuestOutputCBOR(export, raw, &out); err != nil {
		return nil, nil, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d): %w", frp.ErrPipelineStepFailed(), step.ID, index, err)
	}

	// The returned outputs are checked against the export's declared output
	// contract from the value just decoded. The refusal is the invoked export's, so
	// it is reported as the invocation's, and it is raised ahead of the guest's own
	// failure report, so an answer whose outputs violate the contract halts on the
	// contract even where the guest also reported a failure.
	if checkOutputs != nil {
		if err := checkOutputs(out.Outputs); err != nil {
			return nil, nil, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d) invoke: %w", frp.ErrPipelineStepFailed(), step.ID, index, err)
		}
	}

	if gerr := wasm.GuestReturnedError(step.Plugin, export, wasm.GuestFailure{Message: out.Error}); gerr != nil {
		return nil, nil, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d): %w", frp.ErrPipelineStepFailed(), step.ID, index, gerr)
	}

	// A severity the shared diagnostic contract does not declare is a violated
	// output contract rather than a soft step outcome, so it carries
	// ErrPipelineContractViolation and halts the walk even for an Optional step —
	// the same standing a step whose returned outputs fail the declared output
	// schema has. Optional tolerates a step that could not do its job; it does not
	// tolerate output the host cannot route.
	if serr := wasm.GuestDiagnosticSeverities(step.Plugin, export, out.Diagnostics); serr != nil {
		return nil, nil, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d): %w: %w",
			frp.ErrPipelineStepFailed(), step.ID, index, serr, frp.ErrPipelineContractViolation())
	}

	for _, d := range out.Diagnostics {
		if d.Severity == abi.SeverityHalt {
			return nil, out.Diagnostics, nil, types.OrderedMap[any]{}, fmt.Errorf("%w: step %q (index %d) error diagnostic: %s", frp.ErrPipelineStepFailed(), step.ID, index, d.Message)
		}
	}

	if err := frp.ApplyPatches(docs, out.Patches); err != nil {
		return nil, out.Diagnostics, nil, types.OrderedMap[any]{}, fmt.Errorf("step %q (index %d): %w", step.ID, index, err)
	}

	return out.SideEffects, out.Diagnostics, out.Patches, out.Outputs, nil
}

// stepEnabled reports whether a step's `when` gate admits it. An empty `when`
// always runs. Otherwise the gate renders through the same evaluator and
// context the step's `with` body uses, and the rendered text is judged by the
// shared truthiness rule: a falsy result skips the step. A render error is a
// step failure regardless of the step's Optional flag — a broken gate
// predicate is an authoring defect, not a soft step outcome.
func stepEnabled(step types.PipelineStep, index int, withCtx *types.TemplateContext, eval TemplateEvaluator) (bool, error) {
	if step.When == "" {
		return true, nil
	}
	rendered, err := eval(fmt.Sprintf("pipeline-step[%s].when", step.ID), step.When, withCtx)
	if err != nil {
		return false, fmt.Errorf("%w: step %q (index %d) `when`: %w", frp.ErrPipelineStepFailed(), step.ID, index, err)
	}
	return truthy.Bool(rendered), nil
}

// evalWith renders the step's `with` template against the standard Recipe
// template context and parses the result into the step input, preserving
// author order. An empty `with` yields an empty step input. The evaluator
// is injected ([TemplateEvaluator]) so the walk reaches the renderer's
// single template primitive without importing the renderer — the walk does
// not build a second one, so a `with` body sees the same funcmap, Sprig
// functions, and missing-key policy as every other Recipe template surface.
// Where opts carry a WithEvaluator, it renders and reads the body instead.
func evalWith(step types.PipelineStep, withCtx *types.TemplateContext, eval TemplateEvaluator, opts walkOptions) (types.OrderedMap[any], error) {
	var stepInput types.OrderedMap[any]
	if step.With == "" {
		return stepInput, nil
	}
	name := fmt.Sprintf("pipeline-step[%s].with", step.ID)
	var decodeErr error
	if opts.evalWith != nil {
		var evalErr error
		if evalErr, decodeErr = opts.evalWith(name, step.With, withCtx, &stepInput); evalErr != nil {
			return stepInput, evalErr
		}
	} else {
		rendered, err := eval(name, step.With, withCtx)
		if err != nil {
			return stepInput, err
		}
		// `with` evaluates to a JSON/YAML map; YAML is a JSON superset, so one
		// unmarshal handles both, and OrderedMap.UnmarshalYAML preserves the
		// rendered key order onto the wire.
		decodeErr = yaml.Unmarshal([]byte(rendered), &stepInput)
	}
	if decodeErr != nil {
		return stepInput, fmt.Errorf("parse rendered `with` as a map: %w", decodeErr)
	}
	return stepInput, nil
}

// toStepOutput converts one step's wire outputs into the string-valued
// [types.StepOutput] the .steps.<id>.outputs channel carries, preserving
// author order (the wire OrderedMap is walked in order and each value is
// stringified in place) so a no-change re-render is byte-identical.
func toStepOutput(outputs types.OrderedMap[any]) (types.StepOutput, error) {
	var out types.StepOutput
	for k, v := range outputs.All() {
		s, err := stringifyStepOutput(v)
		if err != nil {
			return types.StepOutput{}, err
		}
		out.Outputs.Set(k, s)
	}
	return out, nil
}

// stringifyStepOutput renders one pipeline-step output value into the string
// form the .steps.<id>.outputs.<field> channel carries. This is the injection
// boundary — the String Pipe: the host does not interpret what an output means and
// pipes it through as a string. A scalar reaches the next step's context in
// its natural string form — the same stringify every other injection-boundary
// scalar takes — so a number, boolean, or string lands unquoted; a JSON null
// lands as the empty string. A composite value (a nested object or array a
// guest placed in an output slot the channel type intends for scalars) has no
// natural scalar form, so it renders as its canonical JSON encoding, an
// author-order-preserving representation (a decoded nested object is an
// OrderedMap, which marshals in author order) so identical input yields
// byte-identical output. This is the single home of the FRP step-output
// scalar-to-string conversion.
func stringifyStepOutput(v any) (string, error) {
	switch v.(type) {
	case nil:
		return "", nil
	case *types.OrderedMap[any], []any:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return fmt.Sprint(v), nil
	}
}

// snapshotDocumentSet returns a deep, order-preserving copy of the set so a
// failing step can be rolled back to its pre-step state byte-for-byte. Each
// unit's Tree and Bytes are []byte; the copy must duplicate the backing
// bytes because the apply path replaces a unit's Tree in place — a shallow
// copy would alias the original and defeat the rollback. A generic
// OrderedMap clone is deliberately not used: it would shallow-copy the unit
// values and silently alias their byte slices.
func snapshotDocumentSet(ds frp.DocumentSet) frp.DocumentSet {
	return frp.DocumentSet{
		Own:  cloneUnitMap(ds.Own),
		Host: cloneUnitMap(ds.Host),
	}
}

// cloneUnitMap copies a keyed unit map in author order, duplicating each
// unit's Tree and Bytes so the copy aliases nothing in the source.
func cloneUnitMap(src types.OrderedMap[frp.Unit]) types.OrderedMap[frp.Unit] {
	var out types.OrderedMap[frp.Unit]
	for k, u := range src.All() {
		out.Set(k, frp.Unit{
			Token:     u.Token,
			Bytes:     cloneBytes(u.Bytes),
			MediaType: u.MediaType,
			Tree:      cloneBytes(u.Tree),
		})
	}
	return out
}

// cloneBytes duplicates a byte slice, preserving nil (so an opaque unit's
// absent Tree stays absent and a structured unit's tree stays non-nil).
func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}
