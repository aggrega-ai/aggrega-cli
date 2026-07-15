// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// describeExportName is the export every plugin implements to
// self-describe. The host calls it once at load to read the plugin's
// declared ABI version, type, and exports before dispatching any work.
const describeExportName = "describe"

// LoadAndDescribe is the shared plugin-load gate: it loads the plugin
// named by spec, invokes its describe() export, validates the returned
// descriptor against the host contract, and returns the live plugin
// alongside the validated descriptor. The caller invokes the plugin's
// exports through InvokeExport and Closes it when done.
//
// The gate is the single sequence every socket that loads a guest goes
// through — load, describe, validate, dispatch — rather than each
// socket reimplementing describe-then-dispatch. A load failure, a
// describe() call failure, an unparseable descriptor, or a descriptor
// the host refuses (ABI-version mismatch, missing minimum export, a
// declared host function outside the allowlist) returns an error and no
// live plugin; the plugin is Closed before the error propagates so a
// rejected load leaks no Wazero instance. Descriptor-rejection errors
// wrap [ErrWasmManifestRejected].
func (l *Loader) LoadAndDescribe(ctx context.Context, spec PluginSpec) (*Plugin, PluginDescriptor, error) {
	plugin, err := l.Load(ctx, spec)
	if err != nil {
		return nil, PluginDescriptor{}, err
	}
	desc, err := l.DescribeAndValidate(ctx, plugin, spec)
	if err != nil {
		return nil, PluginDescriptor{}, err
	}
	return plugin, desc, nil
}

// LoadCLIAndDescribe is the runtime-CLI counterpart to LoadAndDescribe: it loads
// the plugin named by spec under the terminal CLI instantiation profile (binding
// the streaming stdout/stderr host functions to the supplied writers), invokes
// its describe() export, validates the returned descriptor, and returns the live
// plugin alongside the validated descriptor.
//
// It differs from LoadAndDescribe only in the load half: LoadCLI validates the
// guest's imports against the union allowlist, so a CLI guest that imports the
// streaming emitters is admitted, where LoadAndDescribe's build profile would
// reject it before describe() could run. The post-load half is shared —
// DescribeAndValidate is the same primitive both entry points pair with — so the
// describe/validate/cross-check sequence lives in one place.
//
// The same rejection discipline as LoadAndDescribe applies: a load failure, a
// describe() call failure, an unparseable descriptor, or a descriptor the host
// refuses returns an error and no live plugin, with the plugin Closed before the
// error propagates so a rejected load leaks no Wazero instance. On success the
// caller invokes the plugin's exports and Closes it when done.
func (l *Loader) LoadCLIAndDescribe(ctx context.Context, spec PluginSpec, stdout, stderr io.Writer) (*Plugin, PluginDescriptor, error) {
	plugin, err := l.LoadCLI(ctx, spec, stdout, stderr)
	if err != nil {
		return nil, PluginDescriptor{}, err
	}
	desc, err := l.DescribeAndValidate(ctx, plugin, spec)
	if err != nil {
		return nil, PluginDescriptor{}, err
	}
	return plugin, desc, nil
}

// DescribeAndValidate runs the post-load half of the plugin-load gate over an
// already-loaded plugin: it invokes describe(), validates the returned
// descriptor, cross-checks the descriptor's declared host functions against the
// binary's imports, and — when the spec carries an authored capability claim —
// cross-checks that too. It is the single post-load sequence shared by both load
// profiles: LoadAndDescribe pairs it with the build-profile Load, and the
// runtime CLI runner pairs it with the terminal-profile LoadCLI, so
// describe/validate/cross-check lives in one place rather than being duplicated
// per profile.
//
// The caller supplies a plugin already loaded from spec. On any rejection —
// describe() call failure, an unparseable descriptor, a descriptor the host
// refuses, or a host-function/capability cross-check mismatch — the plugin is
// Closed before the error propagates, so a rejected load leaks no Wazero
// instance; descriptor-rejection errors wrap [ErrWasmManifestRejected]. On
// success the live plugin remains open for the caller to invoke through
// InvokeExport / InvokeExportStatus and Close when done.
func (l *Loader) DescribeAndValidate(ctx context.Context, plugin *Plugin, spec PluginSpec) (PluginDescriptor, error) {
	raw, err := plugin.Call(ctx, describeExportName, nil)
	if err != nil {
		_ = plugin.Close(ctx)
		return PluginDescriptor{}, fmt.Errorf("wasm: plugin %q describe(): %w", spec.Name, err)
	}

	desc, err := parseAndValidateDescriptor(raw, spec.Name)
	if err != nil {
		_ = plugin.Close(ctx)
		return PluginDescriptor{}, err
	}

	// The descriptor's declared host_functions is a claim; the .wasm import
	// section derived at load is ground truth. Cross-check them so describe()
	// cannot advertise a host surface the binary lacks nor hide one it uses.
	// This runs for every guest — the check is a property of the plugin, not of
	// any entity claim. On mismatch, Close before returning so a rejected load
	// leaks no instance, matching the descriptor-rejection discipline above.
	if err := crossCheckHostFunctions(spec.Name, desc, plugin.HostFunctions()); err != nil {
		_ = plugin.Close(ctx)
		return PluginDescriptor{}, err
	}

	// When the spec carries an authored capability claim (the entity-fronted
	// load path), cross-check it against the validated descriptor. A nil claim
	// — every non-entity caller and the publish path — skips the cross-check.
	// On mismatch, Close before returning so a rejected load leaks no instance,
	// matching the descriptor-rejection discipline above.
	if spec.Contract != nil {
		if err := CrossCheckContract(spec.Name, *spec.Contract, desc); err != nil {
			_ = plugin.Close(ctx)
			return PluginDescriptor{}, err
		}
	}

	if l.OnDescribe != nil {
		l.OnDescribe(spec.Name)
	}
	return desc, nil
}

// parseAndValidateDescriptor unmarshals a describe() payload into a
// [PluginDescriptor] and runs the host's descriptor contract over it.
// It is pure over the bytes — the live gate and the test suite share it
// — so the parse-and-validate behaviour is exercised without a running
// guest. Both a malformed payload and a contract violation return an
// error wrapping [ErrWasmManifestRejected]; identity names the plugin
// in the message.
func parseAndValidateDescriptor(raw []byte, identity string) (PluginDescriptor, error) {
	var desc PluginDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return PluginDescriptor{}, fmt.Errorf("wasm: plugin %q describe() output is not valid JSON: %v: %w",
			identity, err, ErrWasmManifestRejected)
	}
	if err := desc.Validate(identity); err != nil {
		return PluginDescriptor{}, err
	}
	return desc, nil
}

// InvokeExport calls the named export after confirming the descriptor
// declared it, so the host only ever invokes a function the guest
// advertised in describe(). A call to an undeclared export is refused
// before the guest runs, with an error wrapping [ErrWasmManifestRejected]
// that names the plugin and the missing export.
//
// It applies the compile-time exit-code policy through [Plugin.Call]: a
// non-zero guest exit is a failure wrapping [ErrWasmPluginExited]. The
// runtime CLI counterpart [InvokeExportStatus] applies the opposite policy.
func InvokeExport(ctx context.Context, plugin *Plugin, desc PluginDescriptor, export string, payload []byte) ([]byte, error) {
	if err := ensureDeclared(plugin, desc, export); err != nil {
		return nil, err
	}
	return plugin.Call(ctx, export, payload)
}

// InvokeExportStatus calls the named export like [InvokeExport] — same
// declared-export guard — but returns the guest's exit code as a status value
// rather than folding a non-zero exit into an error. It is the runtime-CLI
// counterpart: for a CLI command a non-zero exit code is an ordinary command
// status (the shell's $?), so the caller reads it as the command's result. Only
// a real Wasm trap or a recovered guest panic is an error here; a clean exit
// with any code, including non-zero, is a status.
//
// This deliberately DIVERGES from the compile-time policy [Plugin.Call] applies,
// where any non-zero exit is a build failure wrapping [ErrWasmPluginExited]. The
// two exit-code policies are a deliberate near-duplicate — a build must treat a
// plugin's non-zero exit as a halt, a terminal command must surface it as an
// ordinary status — and must not be unified.
func InvokeExportStatus(ctx context.Context, plugin *Plugin, desc PluginDescriptor, export string, payload []byte) (uint32, []byte, error) {
	if err := ensureDeclared(plugin, desc, export); err != nil {
		return 0, nil, err
	}
	return plugin.callRaw(ctx, export, payload)
}

// ensureDeclared is the shared declared-export guard both InvokeExport and
// InvokeExportStatus apply before dispatching, so the host only ever invokes a
// function the guest advertised in describe(). A call to an undeclared export
// returns an error wrapping [ErrWasmManifestRejected] naming the plugin and the
// missing export.
func ensureDeclared(plugin *Plugin, desc PluginDescriptor, export string) error {
	if !desc.hasExport(export) {
		return fmt.Errorf("wasm: plugin %q does not declare export %q in describe(): %w",
			plugin.Name(), export, ErrWasmManifestRejected)
	}
	return nil
}
