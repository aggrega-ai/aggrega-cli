// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// The LinterInput / LinterOutput wire types are generated from
// wasm-linter-v1beta1 into linter_host_gen.go, single-sourcing the wire shape
// with the portable guest codec in aggrega-abi and the PDKs: the
// publishing-discipline configuration is the same typed Policies object on both
// sides, and a finding is the shared [abi.Diagnostic] every socket carries.
// This file carries the dispatch behaviour that consumes them.
//
// The socket carries canonical CBOR in both directions: each input is encoded
// with the generated encoder, and each answer is read through the binary
// encoding's strict decode, so an answer carrying a field the linter shape does
// not declare is refused rather than read as an empty finding list. A Release's
// components cross as an ordered list of entries, in the Aggregate's own
// instance order.
//
// The socket's default rule-evaluation export is the linter plugin type's one
// host export (wasm.LintRulesExport). It carries the lint_ prefix the shared
// describe() gate requires of a linter plugin's exports; the host dispatches
// every rule invocation to a single export, selecting the rule via
// LinterInput.RuleID rather than per-rule exports. A linter attachment that
// names an entry function of its own dispatches that one instead, which the
// caller threads through as the dispatchers' export argument.

// DispatchPublishingLinter invokes the publishing-linter Wasm plugin
// against the supplied LinterInput and returns its findings.
//
// spec is the loader input the caller built from the resolved plugin: the bytes
// to compile (produced by the shared plugin-resolution cascade, pkg/pluginpath)
// plus the optional entity capability claim and runtime caps. Resolution — tier
// selection (embed / colocated / local / cache / registry) and any digest-pin
// verification — happens at the caller before dispatch; this dispatcher only
// loads and runs the spec it is handed.
//
// The plugin loads through the shared describe() gate: the host calls
// describe(), validates the descriptor (ABI version, plugin type,
// minimum lint_* export) against its contract, then invokes the
// lint_rules export. A plugin lacking describe() or carrying a bad
// descriptor halts here with a loud guardrail. A non-nil spec.Contract arms
// the load gate's describe()-vs-entity cross-check, and spec's caps bound the
// plugin's memory and per-call execution. Returns a wrapped error on
// loader/gate failure, plugin call failure, or an output that is not this
// socket's shape — an export whose output carries a field the linter shape does
// not declare is refused rather than read as an empty finding list.
//
// export is the entry function to dispatch to; an empty export selects the
// socket's default (the linter type's host export), mirroring a linter
// attachment that names no per-attachment override.
//
// ctx belongs to the caller and governs the guest itself, not merely the gaps
// between calls: the host loads every guest with the runtime configured to close
// a module whose call context is done, so ending ctx unwinds a rule evaluation
// that is already running rather than waiting for it to come back. A caller that
// has already withdrawn is answered before the guest is compiled, so a lint
// nobody is waiting for does not pay for an instantiation.
func DispatchPublishingLinter(ctx context.Context, spec wasm.PluginSpec, export string, input LinterInput) ([]abi.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("publishing_linter: %w", err)
	}
	name := wasm.AuthoredExportName(wasm.LintRulesExport, export)
	payload, err := input.EncodeCBOR()
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: encode input: %w", err)
	}

	// The linter socket only runs linter-typed guests, so the load gate is told
	// to expect one; a guest that self-reports any other kind halts the load.
	spec.PluginType = wasm.PluginTypeLinter

	loader := wasm.NewCachingLoader(nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load: %w", err)
	}
	defer plugin.Close(ctx)

	out, err := wasm.InvokeAuthoredExport(ctx, plugin, desc, wasm.LintRulesExport, export, payload)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: call %s: %w", name, err)
	}

	var output LinterOutput
	if err := wasm.DecodeGuestOutputCBOR(name, out, &output); err != nil {
		return nil, fmt.Errorf("publishing_linter: %w", err)
	}
	if err := wasm.GuestReturnedError(spec.Name, name, wasm.GuestFailure{Message: output.Error}); err != nil {
		return nil, fmt.Errorf("publishing_linter: %w", err)
	}
	if err := wasm.GuestDiagnosticSeverities(spec.Name, name, output.Findings); err != nil {
		return nil, fmt.Errorf("publishing_linter: %w", err)
	}
	return output.Findings, nil
}

// DispatchPublishingLinterBatch invokes the publishing-linter Wasm
// plugin once per input, loading the plugin a single time at batch
// open. The returned slice carries one []abi.Diagnostic per input,
// in input order. Errors from any individual call short-circuit the
// batch.
//
// The batch dispatcher exists because `aggrega lint` invokes the
// plugin once per rule per entity — 6 rules in default mode, 7 with
// --pre-publish. Loading the plugin once amortises the Extism
// instantiation cost across the rule iteration.
//
// spec is the loader input the caller built from the resolved plugin (bytes from
// the shared plugin-resolution cascade, pkg/pluginpath, plus the optional entity
// capability claim and runtime caps); the caller resolves the tier and verifies
// any pin before dispatch. The plugin loads once at batch open: a non-nil
// spec.Contract arms the load gate's describe()-vs-entity cross-check (fired
// once in LoadAndDescribe), and spec's caps bound each per-input Call's memory
// and execution time.
//
// export is the entry function to dispatch to; an empty export selects the
// socket's default (the linter type's host export), mirroring a linter
// attachment that names no per-attachment override.
//
// ctx belongs to the caller and governs every call in the batch, the one already
// running included: the host loads the guest with the runtime configured to close
// a module whose call context is done, so ending ctx unwinds the rule evaluation
// in flight instead of letting it finish and stopping at the next input. That is
// what makes the batch worth cancelling at all — it is the long-running form, and
// a stop that only took effect between inputs would still owe the caller the
// whole of the current one.
func DispatchPublishingLinterBatch(ctx context.Context, spec wasm.PluginSpec, export string, inputs []LinterInput) ([][]abi.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("publishing_linter: %w", err)
	}
	name := wasm.AuthoredExportName(wasm.LintRulesExport, export)
	// The linter socket only runs linter-typed guests, so the load gate is told
	// to expect one; a guest that self-reports any other kind halts the load.
	spec.PluginType = wasm.PluginTypeLinter

	loader := wasm.NewCachingLoader(nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load: %w", err)
	}
	defer plugin.Close(ctx)

	results := make([][]abi.Diagnostic, 0, len(inputs))
	for i, input := range inputs {
		payload, err := input.EncodeCBOR()
		if err != nil {
			return nil, fmt.Errorf("publishing_linter: encode input[%d]: %w", i, err)
		}
		out, err := wasm.InvokeAuthoredExport(ctx, plugin, desc, wasm.LintRulesExport, export, payload)
		if err != nil {
			return nil, fmt.Errorf("publishing_linter: call %s input[%d]: %w", name, i, err)
		}
		var output LinterOutput
		if err := wasm.DecodeGuestOutputCBOR(name, out, &output); err != nil {
			return nil, fmt.Errorf("publishing_linter: input[%d]: %w", i, err)
		}
		if err := wasm.GuestReturnedError(spec.Name, name, wasm.GuestFailure{Message: output.Error}); err != nil {
			return nil, fmt.Errorf("publishing_linter: input[%d]: %w", i, err)
		}
		if err := wasm.GuestDiagnosticSeverities(spec.Name, name, output.Findings); err != nil {
			return nil, fmt.Errorf("publishing_linter: input[%d]: %w", i, err)
		}
		results = append(results, output.Findings)
	}
	return results, nil
}
