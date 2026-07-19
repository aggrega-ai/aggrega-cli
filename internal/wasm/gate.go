// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
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

// LoadForDescribe is the describe-only load gate for the introspection surfaces
// that must read a guest's describe() self-report without granting it any real
// host capability — listing configured plugins and generating a catalog. It
// loads the plugin under the describe-only profile (inert host-function stubs,
// the union import allowlist so any plugin type instantiates), invokes
// describe(), validates the descriptor, and cross-checks it — the same post-load
// half LoadAndDescribe runs.
//
// It differs from LoadAndDescribe only in the load half. LoadAndDescribe loads
// under the build profile: real host functions bound and the shared import
// allowlist, so a streaming CLI guest is rejected before describe() can run and
// describe() runs against the real host surface. LoadForDescribe binds inert
// stubs and admits the union allowlist, so a guest of any type — including a
// streaming CLI guest — is describable, and a guest's describe() reaches only
// no-op stubs. That is the right posture for a surface that needs the
// self-report and never the guest's real behavior: a malicious describe() gains
// no capability. The post-load half is the shared DescribeAndValidate
// primitive, so describe/validate/cross-check lives in one place across every
// profile — including the unchanged cross-check of the descriptor's declared
// host functions against the binary's imports.
//
// The same rejection discipline as LoadAndDescribe applies: a load failure, a
// describe() call failure, an unparseable descriptor, or a descriptor the host
// refuses returns an error and no live plugin, with the plugin Closed before the
// error propagates so a rejected load leaks no Wazero instance. Descriptor-
// rejection errors wrap [ErrWasmManifestRejected]. On success the caller invokes
// the plugin's exports through InvokeExport and Closes it when done.
func (l *Loader) LoadForDescribe(ctx context.Context, spec PluginSpec) (*Plugin, PluginDescriptor, error) {
	plugin, err := l.loadForDescribe(ctx, spec)
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
// cross-checks that too. It is the single post-load sequence shared across every
// load profile: LoadAndDescribe pairs it with the build-profile Load,
// LoadForDescribe pairs it with the describe-only load, and the runtime CLI
// runner pairs it with the terminal-profile LoadCLI, so
// describe/validate/cross-check lives in one place rather than being duplicated
// per profile.
//
// When spec carries an expected plugin type (spec.PluginType is non-empty),
// the descriptor's self-reported plugin type must equal it; a mismatch — a
// mutator guest attached at the policy socket, a provider guest at the linter
// socket, a non-CLI guest run as a terminal command — halts with an error
// wrapping [ErrWasmPluginTypeMismatch]. An empty spec.PluginType declares no
// expectation (the introspection path that reads a guest's self-report without
// asserting a kind) and skips the check. This is the single type-match gate
// every load-and-dispatch socket routes through rather than each socket
// re-checking the descriptor's type inline.
//
// The caller supplies a plugin already loaded from spec. On any rejection —
// describe() call failure, an unparseable descriptor, a descriptor the host
// refuses, a host-function/capability cross-check mismatch, or an expected-type
// mismatch — the plugin is Closed before the error propagates, so a rejected
// load leaks no Wazero instance; descriptor-rejection errors wrap
// [ErrWasmManifestRejected]. On success the live plugin remains open for the
// caller to invoke through InvokeExport / InvokeExportStatus and Close when done.
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

	// When the spec declares an expected plugin type (the load-and-dispatch
	// sockets set it), the descriptor's self-reported type must match it. An
	// empty expectation is the introspection path and skips the check. On
	// mismatch, Close before returning so a rejected load leaks no instance,
	// matching the descriptor-rejection discipline above.
	if spec.PluginType != "" {
		if err := gateExpectedType(spec.Name, desc, spec.PluginType); err != nil {
			_ = plugin.Close(ctx)
			return PluginDescriptor{}, err
		}
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

	// When the entity supplied its machine-owned command mirror (the
	// entity-fronted load path), verify it matches the plugin's describe()
	// commands exactly. This is keyed on the explicit CommandsMirrored signal, not
	// on Contract: a CLI plugin carries a nil Contract but a real command mirror,
	// and a bare .wasm load supplies no mirror and is not checked. The mirror is a
	// machine record that must reflect the binary exactly, so the comparison is
	// full equality rather than the authored-claim subset CrossCheckContract runs.
	if spec.CommandsMirrored {
		if !CommandMirrorsEqual(spec.Commands, desc.Commands) {
			_ = plugin.Close(ctx)
			return PluginDescriptor{}, fmt.Errorf(
				"wasm: plugin %q: recorded command mirror %s does not match the plugin describe() commands %s; re-run `aggrega plugin sync` and commit the result: %w",
				spec.Name, FormatCommandMirror(spec.Commands), FormatCommandMirror(desc.Commands), ErrWasmCommandsMirrorMismatch)
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
	if err := desc.UnmarshalJSON(raw); err != nil {
		return PluginDescriptor{}, fmt.Errorf("wasm: plugin %q describe() output is not valid JSON: %v: %w",
			identity, err, ErrWasmManifestRejected)
	}
	if err := desc.Validate(identity); err != nil {
		return PluginDescriptor{}, err
	}
	return desc, nil
}

// gateExpectedType compares a validated descriptor's self-reported plugin type
// against the type expected at the load site. It returns nil when they agree and
// an error wrapping [ErrWasmPluginTypeMismatch] otherwise. The human noun in the
// message comes from the canonical plugin-type registry, so the gate and the
// registry never disagree on what to call a type; when a type carries no registry
// label the raw type string stands in. The message names the plugin identity and
// both the expected and reported types so the author sees exactly which two kinds
// diverged.
func gateExpectedType(pluginIdentity string, desc PluginDescriptor, expected PluginType) error {
	if desc.PluginType == string(expected) {
		return nil
	}
	noun := string(expected)
	if entry, ok := plugintype.Lookup(expected); ok && entry.Label != "" {
		noun = entry.Label
	}
	return fmt.Errorf(
		"wasm: plugin %q: expected a %s plugin (plugin type %q) but its describe() reports plugin type %q: %w",
		pluginIdentity, noun, string(expected), desc.PluginType, ErrWasmPluginTypeMismatch)
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
