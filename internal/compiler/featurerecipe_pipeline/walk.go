// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/jsonpatch"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"gopkg.in/yaml.v3"
)

// StepInvoker invokes one pipeline step's guest export with the marshalled
// [FRPStepInput] payload and returns the raw [FRPStepOutput] bytes. The
// caller binds this to the shared describe() gate's InvokeExport (resolving
// and loading the plugin upstream); the walk reaches the guest only through
// this seam, so it carries no plugin-resolution or loading concern and is
// unit-testable with a stub invoker.
//
// A non-nil error is treated as a step failure: a guest trap, a refused
// export, or any invoke-path fault. The walk classifies it strict or
// optional per the step's Optional flag.
type StepInvoker func(ctx context.Context, step types.PipelineStep, payload []byte) ([]byte, error)

// TemplateEvaluator renders a Go template string against a data value and
// returns the rendered text. It is the seam through which the walk reaches
// the renderer's single template primitive without importing the renderer:
// the caller passes the renderer's EvalTemplate, so a step's `with` body
// sees the same funcmap, Sprig functions, and missing-key policy as every
// other Recipe template surface. Its signature matches that primitive
// exactly. A test supplies a stub.
type TemplateEvaluator func(name, text string, data any) (string, error)

// WalkResult is the outcome of a successful pipeline walk: the mutated
// document set, the side effects every step accumulated in order, and the
// warnings raised along the way (non-error guest diagnostics plus the loud
// warning each soft-failed optional step produces).
type WalkResult struct {
	Documents   DocumentSet
	SideEffects []SideEffect
	Warnings    []renderctx.Diagnostic
}

// Walk executes a node's pipeline steps in order over the keyed document
// set. For each step it snapshots the set, evaluates the step's `with`
// template into the step input, invokes the guest through invoke, and
// applies the returned patches by scope and key; it accumulates side
// effects and threads a record of preceding steps forward.
//
// Failure is strict by default. A step failure — a guest trap or invoke
// fault, a malformed step output, an error-severity diagnostic, a patch
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
// [FRPStepInput.Context]. eval is the template evaluator the walk renders
// each step's `with` body through — injected as a seam (the caller passes
// the renderer's EvalTemplate) so this package never imports the renderer
// and the renderer can call Walk without an import cycle; a test supplies a
// stub evaluator. Walk does not resolve plugins, construct the document
// set, or enforce provider compatibility — those are the caller's.
func Walk(
	ctx context.Context,
	docs DocumentSet,
	steps []types.PipelineStep,
	invoke StepInvoker,
	withCtx *types.TemplateContext,
	renderCtx renderctx.RenderContext,
	eval TemplateEvaluator,
) (WalkResult, error) {
	var sideEffects []SideEffect
	var warnings []renderctx.Diagnostic
	var preceding []PrecedingStep

	// Operate on a private copy so the walk never mutates the caller's set
	// in place — apply writes documents back into the working roots, and a
	// strict halt must leave the caller's input untouched.
	docs = snapshotDocumentSet(docs)

	for i, step := range steps {
		snap := snapshotDocumentSet(docs)

		stepEffects, stepDiags, err := runStep(ctx, &docs, step, i, preceding, invoke, withCtx, renderCtx, eval)

		// Non-error diagnostics surface regardless of the step's outcome.
		for _, d := range stepDiags {
			if d.Severity != "error" {
				warnings = append(warnings, d)
			}
		}

		if err != nil {
			docs = snap // whole-step rollback
			if step.Optional {
				warnings = append(warnings, renderctx.Diagnostic{
					Severity: "warning",
					Code:     "pipeline_step_skipped",
					Message:  fmt.Sprintf("optional pipeline step %q skipped: %v", step.ID, err),
				})
				continue
			}
			return WalkResult{}, err
		}

		sideEffects = append(sideEffects, stepEffects...)
		preceding = append(preceding, PrecedingStep{
			Index:       i,
			PluginURI:   step.Plugin,
			SideEffects: stepEffects,
		})
	}

	return WalkResult{Documents: docs, SideEffects: sideEffects, Warnings: warnings}, nil
}

// runStep evaluates, invokes, and applies one step, mutating docs in place
// on success. It returns the step's side effects and the guest's
// diagnostics. A non-nil error means the step failed; the caller restores
// the snapshot. docs may be partially mutated when the error is non-nil —
// the caller's snapshot restore is what makes rollback whole-step.
func runStep(
	ctx context.Context,
	docs *DocumentSet,
	step types.PipelineStep,
	index int,
	preceding []PrecedingStep,
	invoke StepInvoker,
	withCtx *types.TemplateContext,
	renderCtx renderctx.RenderContext,
	eval TemplateEvaluator,
) ([]SideEffect, []renderctx.Diagnostic, error) {
	stepInput, err := evalWith(step, withCtx, eval)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: step %q (index %d) `with`: %w", ErrPipelineStepFailed, step.ID, index, err)
	}

	payload, err := json.Marshal(FRPStepInput{
		Documents:      *docs,
		StepInput:      stepInput,
		Context:        renderCtx,
		StepIndex:      index,
		PrecedingSteps: preceding,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: step %q (index %d) marshal input: %w", ErrPipelineStepFailed, step.ID, index, err)
	}

	raw, err := invoke(ctx, step, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: step %q (index %d) invoke: %w", ErrPipelineStepFailed, step.ID, index, err)
	}

	var out FRPStepOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("%w: step %q (index %d) unmarshal output: %w", ErrPipelineStepFailed, step.ID, index, err)
	}

	for _, d := range out.Diagnostics {
		if d.Severity == "error" {
			return nil, out.Diagnostics, fmt.Errorf("%w: step %q (index %d) error diagnostic: %s", ErrPipelineStepFailed, step.ID, index, d.Message)
		}
	}

	if err := applyPatches(docs, out.Patches); err != nil {
		return nil, out.Diagnostics, fmt.Errorf("step %q (index %d): %w", step.ID, index, err)
	}

	return out.SideEffects, out.Diagnostics, nil
}

// evalWith renders the step's `with` template against the standard Recipe
// template context and parses the result into the step input, preserving
// author order. An empty `with` yields an empty step input. The evaluator
// is injected ([TemplateEvaluator]) so the walk reaches the renderer's
// single template primitive without importing the renderer — the walk does
// not build a second one, so a `with` body sees the same funcmap, Sprig
// functions, and missing-key policy as every other Recipe template surface.
func evalWith(step types.PipelineStep, withCtx *types.TemplateContext, eval TemplateEvaluator) (types.OrderedMap[any], error) {
	var stepInput types.OrderedMap[any]
	if step.With == "" {
		return stepInput, nil
	}
	rendered, err := eval(fmt.Sprintf("pipeline-step[%s].with", step.ID), step.With, withCtx)
	if err != nil {
		return stepInput, err
	}
	// `with` evaluates to a JSON/YAML map; YAML is a JSON superset, so one
	// unmarshal handles both, and OrderedMap.UnmarshalYAML preserves the
	// rendered key order onto the wire.
	if err := yaml.Unmarshal([]byte(rendered), &stepInput); err != nil {
		return stepInput, fmt.Errorf("parse rendered `with` as a map: %w", err)
	}
	return stepInput, nil
}

// applyPatches applies each returned patch to the document it addresses by
// scope and key. It is the single site of the ABI→engine operation
// mapping: a [JSONPatchOp] is the wire shape a guest returns, while
// [jsonpatch.Op] is the apply engine's deliberately separate local type
// (see the Op godoc in package jsonpatch for why the two are not unified —
// keeping the engine free of the ABI import is what makes it a reusable
// leaf). The mapping lives here and only here.
//
// docs is mutated in place. An absent (scope, key) target returns
// [ErrPipelineTargetAbsent]; any engine rejection propagates wrapped. On
// error docs may be partially mutated; the caller's snapshot restore makes
// rollback whole-step.
func applyPatches(docs *DocumentSet, ops []JSONPatchOp) error {
	for _, op := range ops {
		root, err := scopeRoot(docs, op.Scope)
		if err != nil {
			return err
		}

		raw, ok := root.Get(op.Key)
		if !ok {
			return fmt.Errorf("%w: scope %q key %q", ErrPipelineTargetAbsent, op.Scope, op.Key)
		}

		doc := types.NewOrderedMap[any]()
		if err := json.Unmarshal(raw, doc); err != nil {
			return fmt.Errorf("%w: scope %q key %q: parse document: %w", ErrPipelineStepFailed, op.Scope, op.Key, err)
		}

		// The one place a wire JSONPatchOp becomes an engine jsonpatch.Op.
		engineOp := jsonpatch.Op{Op: op.Op, Path: op.Path, Value: op.Value, From: op.From}
		if err := jsonpatch.Apply(doc, []jsonpatch.Op{engineOp}); err != nil {
			return fmt.Errorf("%w: scope %q key %q: %w", ErrPipelineStepFailed, op.Scope, op.Key, err)
		}

		newRaw, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("%w: scope %q key %q: re-marshal document: %w", ErrPipelineStepFailed, op.Scope, op.Key, err)
		}
		root.Set(op.Key, newRaw)
	}
	return nil
}

// scopeRoot returns the document root a patch scope addresses. An unknown
// scope is a step failure. Host-scope availability (a host root exists only
// for a component-scope FeatureRecipe attached to a host) is the caller's
// concern; here an addressed-but-empty host root simply yields an
// absent-target on Get.
func scopeRoot(docs *DocumentSet, scope DocumentScope) (*types.OrderedMap[json.RawMessage], error) {
	switch scope {
	case DocumentScopeOwn:
		return &docs.Own, nil
	case DocumentScopeHost:
		return &docs.Host, nil
	default:
		return nil, fmt.Errorf("%w: unknown patch scope %q", ErrPipelineStepFailed, scope)
	}
}

// snapshotDocumentSet returns a deep, order-preserving copy of the set so a
// failing step can be rolled back to its pre-step state byte-for-byte. Each
// document value is a json.RawMessage ([]byte); the copy must duplicate the
// backing bytes (append([]byte(nil), v...)) because the apply path replaces
// a document's RawMessage in place — a shallow copy would alias the
// original and defeat the rollback. A generic OrderedMap clone is
// deliberately not used: it would shallow-copy the RawMessage values and
// silently alias them.
func snapshotDocumentSet(ds DocumentSet) DocumentSet {
	return DocumentSet{
		Own:  cloneRawMap(ds.Own),
		Host: cloneRawMap(ds.Host),
	}
}

// cloneRawMap copies a keyed RawMessage map in author order, duplicating
// each value's bytes so the copy aliases nothing in the source.
func cloneRawMap(src types.OrderedMap[json.RawMessage]) types.OrderedMap[json.RawMessage] {
	var out types.OrderedMap[json.RawMessage]
	for k, v := range src.All() {
		out.Set(k, append([]byte(nil), v...))
	}
	return out
}
