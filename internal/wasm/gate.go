// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"encoding/json"
	"fmt"
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

	raw, err := plugin.Call(ctx, describeExportName, nil)
	if err != nil {
		_ = plugin.Close(ctx)
		return nil, PluginDescriptor{}, fmt.Errorf("wasm: plugin %q describe(): %w", spec.Name, err)
	}

	desc, err := parseAndValidateDescriptor(raw, spec.Name)
	if err != nil {
		_ = plugin.Close(ctx)
		return nil, PluginDescriptor{}, err
	}

	// The descriptor's declared host_functions is a claim; the .wasm import
	// section derived at load is ground truth. Cross-check them so describe()
	// cannot advertise a host surface the binary lacks nor hide one it uses.
	// This runs for every guest — the check is a property of the plugin, not of
	// any entity claim. On mismatch, Close before returning so a rejected load
	// leaks no instance, matching the descriptor-rejection discipline above.
	if err := crossCheckHostFunctions(spec.Name, desc, plugin.HostFunctions()); err != nil {
		_ = plugin.Close(ctx)
		return nil, PluginDescriptor{}, err
	}

	// When the spec carries an authored capability claim (the entity-fronted
	// load path), cross-check it against the validated descriptor. A nil claim
	// — every non-entity caller and the publish path — skips the cross-check.
	// On mismatch, Close before returning so a rejected load leaks no instance,
	// matching the descriptor-rejection discipline above.
	if spec.Capability != nil {
		if err := CrossCheckCapabilities(spec.Name, *spec.Capability, desc); err != nil {
			_ = plugin.Close(ctx)
			return nil, PluginDescriptor{}, err
		}
	}

	if l.OnDescribe != nil {
		l.OnDescribe(spec.Name)
	}
	return plugin, desc, nil
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
func InvokeExport(ctx context.Context, plugin *Plugin, desc PluginDescriptor, export string, payload []byte) ([]byte, error) {
	if !desc.hasExport(export) {
		return nil, fmt.Errorf("wasm: plugin %q does not declare export %q in describe(): %w",
			plugin.Name(), export, ErrWasmManifestRejected)
	}
	return plugin.Call(ctx, export, payload)
}
