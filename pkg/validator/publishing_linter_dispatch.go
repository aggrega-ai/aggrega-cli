// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// The LinterInput / LinterOutput wire types are generated from
// wasm-linter-v1alpha1 into linter_host_gen.go (the host vocabulary realizes the
// dynamically assembled policies payload as an opaque json.RawMessage),
// single-sourcing the wire shape with the portable guest codec in aggrega-abi
// and the PDKs. A finding is the shared [abi.Diagnostic] every socket carries.
// This file carries the dispatch behaviour that consumes them.

// lintExportName is the publishing-linter's default rule-evaluation export.
// It carries the lint_ prefix the shared describe() gate requires of a
// linter plugin's minimum export; the host dispatches every rule
// invocation to a single export, selecting the rule via
// LinterInput.RuleID rather than per-rule exports. The dispatch export is
// this default unless the linter attachment names a per-attachment override,
// which the caller threads through as the dispatchers' export argument.
const lintExportName = "lint_rules"

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
// plugin's memory and per-call wall time. Returns a wrapped error on
// loader/gate failure, plugin call failure, or an output that is not this
// socket's shape — an export whose output carries a field the linter shape does
// not declare is refused rather than read as an empty finding list.
//
// export is the entry function to dispatch to; an empty export selects the
// socket's default (lintExportName), mirroring a linter attachment that names
// no per-attachment override.
func DispatchPublishingLinter(spec wasm.PluginSpec, export string, input LinterInput) ([]abi.Diagnostic, error) {
	if export == "" {
		export = lintExportName
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: marshal input: %w", err)
	}

	// The linter socket only runs linter-typed guests, so the load gate is told
	// to expect one; a guest that self-reports any other kind halts the load.
	spec.PluginType = wasm.PluginTypeLinter

	loader := wasm.NewCachingLoader(nil)
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load: %w", err)
	}
	defer plugin.Close(ctx)

	out, err := wasm.InvokeExport(ctx, plugin, desc, export, payload)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: call %s: %w", export, err)
	}

	var output LinterOutput
	if err := wasm.DecodeGuestOutput(export, out, &output); err != nil {
		return nil, fmt.Errorf("publishing_linter: %w", err)
	}
	if err := wasm.GuestReturnedError(export, wasm.GuestFailure{Message: output.Error}); err != nil {
		return nil, fmt.Errorf("publishing_linter: %w", err)
	}
	if err := wasm.GuestDiagnosticSeverities(spec.Name, export, output.Findings); err != nil {
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
// and wall time.
//
// export is the entry function to dispatch to; an empty export selects the
// socket's default (lintExportName), mirroring a linter attachment that names
// no per-attachment override.
func DispatchPublishingLinterBatch(spec wasm.PluginSpec, export string, inputs []LinterInput) ([][]abi.Diagnostic, error) {
	if export == "" {
		export = lintExportName
	}
	// The linter socket only runs linter-typed guests, so the load gate is told
	// to expect one; a guest that self-reports any other kind halts the load.
	spec.PluginType = wasm.PluginTypeLinter

	loader := wasm.NewCachingLoader(nil)
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load: %w", err)
	}
	defer plugin.Close(ctx)

	results := make([][]abi.Diagnostic, 0, len(inputs))
	for i, input := range inputs {
		payload, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("publishing_linter: marshal input[%d]: %w", i, err)
		}
		out, err := wasm.InvokeExport(ctx, plugin, desc, export, payload)
		if err != nil {
			return nil, fmt.Errorf("publishing_linter: call %s input[%d]: %w", export, i, err)
		}
		var output LinterOutput
		if err := wasm.DecodeGuestOutput(export, out, &output); err != nil {
			return nil, fmt.Errorf("publishing_linter: input[%d]: %w", i, err)
		}
		if err := wasm.GuestReturnedError(export, wasm.GuestFailure{Message: output.Error}); err != nil {
			return nil, fmt.Errorf("publishing_linter: input[%d]: %w", i, err)
		}
		if err := wasm.GuestDiagnosticSeverities(spec.Name, export, output.Findings); err != nil {
			return nil, fmt.Errorf("publishing_linter: input[%d]: %w", i, err)
		}
		results = append(results, output.Findings)
	}
	return results, nil
}
