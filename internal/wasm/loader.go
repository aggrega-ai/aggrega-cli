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
	"os"

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
//     AOT-purity firewall against the manifest fields.
//  2. Read the plugin Wasm bytes from spec.Path once into memory.
//  3. Walk the bytes' import section and apply the WASI per-import
//     firewall: stubbed-subset names are allowed; capability-
//     granting names are rejected with ErrWasmManifestRejected.
//  4. Select the curated host functions the spec declares plus the
//     deterministic WASI stubs the firewall promises.
//  5. Construct the underlying extism.Plugin from the same in-memory
//     byte slice (via extism.WasmData), with EnableWasi: false. The
//     WASI stub HostFunction values bind under the
//     wasi_snapshot_preview1 namespace at plugin instantiation; the
//     plugin's WASI imports resolve to the host stubs rather than
//     to a real WASI runtime.
//
// Reading the bytes once and threading the same slice through both
// firewall inspection and Extism instantiation eliminates the
// TOCTOU window between validation and execution. Returns an error
// wrapping ErrWasmManifestRejected if the firewall rejects the
// manifest or any import; returns the Extism SDK's error otherwise.
func (l *Loader) Load(ctx context.Context, spec PluginSpec) (*Plugin, error) {
	manifest, err := translateToManifest(spec)
	if err != nil {
		return nil, err
	}

	wasmBytes, err := os.ReadFile(spec.Path)
	if err != nil {
		return nil, fmt.Errorf("wasm: read plugin %q from %s: %w", spec.Name, spec.Path, err)
	}

	if err := validateWasiImports(spec.Name, wasmBytes); err != nil {
		return nil, err
	}

	// Replace the manifest's WasmFile entry (which would have Extism
	// re-read from disk) with WasmData carrying the already-read
	// bytes. The firewall validated these specific bytes; Extism
	// will compile and instantiate the same bytes.
	manifest.Wasm = []extism.Wasm{
		extism.WasmData{Data: wasmBytes, Name: spec.Name},
	}

	functions := selectAllowlistedHostFunctions(spec.DeclaredHostFunctions)
	functions = append(functions, buildWasiStubHostFunctions()...)

	config := extism.PluginConfig{
		EnableWasi: false,
		// ModuleConfig deliberately left nil: no wall-time, no env
		// passthrough, no stdio passthrough. The Extism runtime uses
		// its own zero-config default which is what Aggrega's
		// AOT-purity guarantee depends on. The wasi_snapshot_preview1
		// import slots the firewall permits are satisfied by the
		// stub HostFunction values appended above — not by Extism's
		// bundled WASI module.
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

// callWithPanicRecovery invokes f and converts any panic into a
// wrapped error. The wrap distinguishes two panic shapes:
//
//   - wasiProcExitTrap (panicked by the stubbed proc_exit host
//     function): the plugin invoked WASI's proc_exit during the
//     call. The recovered value carries the exit code; the returned
//     error satisfies errors.Is(err, ErrWasmPluginExited) and
//     records the code in its message.
//   - any other panic: a Go-side panic that escaped Extism's normal
//     error envelope. The returned error satisfies
//     errors.Is(err, ErrWasmGuestPanic).
//
// Extracted from Plugin.Call so that both wrap shapes are
// independently testable without needing a real Wasm-side panic
// source — tests pass a function that panics with the relevant
// typed value directly.
func callWithPanicRecovery(pluginName, funcName string, f func() ([]byte, error)) (output []byte, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if trap, ok := r.(wasiProcExitTrap); ok {
			err = fmt.Errorf("wasm: plugin %q invoked proc_exit(%d) in %s: %w",
				pluginName, trap.code, funcName, ErrWasmPluginExited)
			return
		}
		err = fmt.Errorf("wasm: guest plugin %q panicked in %s: %v: %w",
			pluginName, funcName, r, ErrWasmGuestPanic)
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
