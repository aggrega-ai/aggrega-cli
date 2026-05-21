// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package wasm is the sole construction site for Extism plugins.
// Per the project's loader-containment discipline, only this package
// imports github.com/extism/go-sdk and constructs *extism.Plugin
// values. Every consumer in the compiler — the publishing-linter, the
// future mutator / policy-validator / helm-renderer / pipeline /
// linter sockets — receives a *Plugin constructed by Loader.Load and
// invokes it through Plugin.Call.
//
// The package's AOT-purity firewall (see manifest_translate.go) rejects
// any plugin configuration that would let guest code reach beyond its
// in-memory inputs at run time: outbound network access via
// AllowedHosts, filesystem access via AllowedPaths, WASI stdio
// passthrough, wall-time access, or host functions outside the curated
// allowlist (see hostfuncs.go). The discipline preserves Aggrega's
// determinism and ahead-of-time guarantees regardless of which plugin
// runs.
//
// Plugin.Call wraps the Extism call boundary with a deferred recover()
// that converts a Go-side panic into an ErrWasmGuestPanic-wrapped
// error. The wrap is a safety net behind Extism's normal error
// envelope; most Wasm-side traps surface as ordinary Go errors through
// the SDK and never trigger the panic path.
package wasm

import (
	"context"
	"fmt"

	extism "github.com/extism/go-sdk"
)

// Loader is the package's entry point for constructing a Plugin from a
// PluginSpec. A zero-value Loader is ready to use; callers SHOULD reuse
// a single Loader across the life of the compiler process so that any
// future runtime-configuration knobs the loader picks up (Wazero
// runtime caches, observability adapters, and so on) amortise across
// plugin instances.
type Loader struct {
	// Reserved for future runtime-configuration knobs the loader
	// itself owns (a shared Wazero compilation cache, for example).
	// Today the struct is empty; declared as a struct rather than a
	// free function so future knobs can land without changing every
	// call site.
}

// Plugin wraps an Extism plugin handle with Aggrega-specific lifecycle
// and call-boundary behaviour. Constructed only by Loader.Load.
type Plugin struct {
	name   string
	plugin *extism.Plugin
}

// Load constructs a Plugin from a PluginSpec. The flow:
//
//  1. Translate the spec into an Extism manifest and run the
//     AOT-purity firewall.
//  2. Select the host functions the spec declares from the curated
//     allowlist.
//  3. Construct the underlying extism.Plugin with EnableWasi: false
//     (no WASI stdio passthrough) and no custom ModuleConfig (no
//     wall-time or env access).
//
// Returns an error wrapping ErrWasmManifestRejected if the firewall
// rejects the manifest; returns the Extism SDK's error otherwise (for
// example, a missing .wasm file at the spec's Path).
func (l *Loader) Load(ctx context.Context, spec PluginSpec) (*Plugin, error) {
	manifest, err := translateToManifest(spec)
	if err != nil {
		return nil, err
	}

	functions := selectAllowlistedHostFunctions(spec.DeclaredHostFunctions)

	config := extism.PluginConfig{
		EnableWasi: false,
		// ModuleConfig deliberately left nil: no wall-time, no env
		// passthrough, no stdio passthrough. The Extism runtime uses
		// its own zero-config default which is what Aggrega's
		// AOT-purity guarantee depends on.
	}

	plugin, err := extism.NewPlugin(ctx, manifest, config, functions)
	if err != nil {
		return nil, fmt.Errorf("wasm: load plugin %q: %w", spec.Name, err)
	}

	return &Plugin{name: spec.Name, plugin: plugin}, nil
}

// Name returns the plugin's diagnostic identifier (the Name field of
// the PluginSpec it was loaded from).
func (p *Plugin) Name() string {
	return p.name
}

// Call invokes an exported function on the plugin. Returns the output
// bytes the guest wrote to its output buffer.
//
// On a guest panic that escapes Extism's normal error envelope, the
// panic-recovery wrap converts the panic into an error wrapping
// ErrWasmGuestPanic. The error message names the plugin and the
// invoked function.
//
// On non-zero exit code from the guest function, returns an error
// citing the exit code; this matches Aggrega's preference for exit
// code zero as the only "ran successfully" signal under the AOT-purity
// discipline.
func (p *Plugin) Call(ctx context.Context, funcName string, input []byte) ([]byte, error) {
	return callWithPanicRecovery(p.name, funcName, func() ([]byte, error) {
		exitCode, output, err := p.plugin.CallWithContext(ctx, funcName, input)
		if err != nil {
			return nil, fmt.Errorf("wasm: plugin %q call to %s: %w", p.name, funcName, err)
		}
		if exitCode != 0 {
			return nil, fmt.Errorf("wasm: plugin %q call to %s exited with code %d", p.name, funcName, exitCode)
		}
		return output, nil
	})
}

// callWithPanicRecovery invokes f and converts any panic into an
// ErrWasmGuestPanic-wrapped error. Extracted from Plugin.Call so that
// the panic-recovery wrap is independently testable without needing a
// real Wasm-side panic source — the test passes a function that
// panics directly. The wrap's shape is the contract: any panic the
// guest causes returns to the caller as an error satisfying
// errors.Is(err, ErrWasmGuestPanic).
func callWithPanicRecovery(pluginName, funcName string, f func() ([]byte, error)) (output []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("wasm: guest plugin %q panicked in %s: %v: %w",
				pluginName, funcName, r, ErrWasmGuestPanic)
		}
	}()
	return f()
}

// Close releases the resources held by the underlying Extism plugin.
// Safe to call once; subsequent calls return the SDK's error if any.
// Callers SHOULD invoke Close when the plugin is no longer needed —
// typically deferred immediately after Load.
func (p *Plugin) Close(ctx context.Context) error {
	if p == nil || p.plugin == nil {
		return nil
	}
	return p.plugin.CloseWithContext(ctx)
}
