// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"go.yaml.in/yaml/v3"
)

// renderWasmPluginFile renders a WasmPlugin authored as a standalone file. The
// plugin's declared capability surface is shown first: the content-addressed
// .wasm artifact reference, the socket type(s) it implements, the ABI version
// it targets, and the provider engines it claims compatibility with. The
// exports table names each entry point and its authored capability claim
// (operates / scope / selector); the runtime block reports the execution
// constraints, describing a platform default qualitatively when a limit is
// unset rather than printing a fixed number, so the displayed surface never
// drifts from the loader's actual cap.
//
//   - levelList: identifier plus the plugin types and export count.
//   - levelDefault: plugin, plugin types, ABI version, compatible providers,
//     the exports table, and the runtime block.
//   - levelDetail: adds the signature pointer and the runtime config keys.
func renderWasmPluginFile(_ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var wp types.WasmPlugin
	if err := yaml.Unmarshal(data, &wp); err != nil {
		return fmt.Errorf("parse WasmPlugin at %q: %w", path, err)
	}
	identifier := identifierVersion(wp.Metadata.Name, wp.Metadata.Version)

	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		fmt.Fprintf(opts.out, "types: %s, exports: %d\n",
			joinedOrNone(wp.Spec.PluginTypes), len(wp.Spec.Exports))
		return nil
	}

	renderHeader(opts, "WasmPlugin", identifier)

	plugin := wp.Spec.Plugin
	if plugin == "" {
		plugin = "(none declared)"
	}
	fmt.Fprintf(opts.out, "Plugin: %s\n", plugin)
	fmt.Fprintf(opts.out, "Plugin types: %s\n", joinedOrNone(wp.Spec.PluginTypes))

	abiVersion := wp.Spec.AbiVersion
	if abiVersion == "" {
		abiVersion = "(none declared)"
	}
	fmt.Fprintf(opts.out, "ABI version: %s\n", abiVersion)
	fmt.Fprintf(opts.out, "Compatible providers: %s\n", joinedOrNone(wp.Spec.CompatibleProviders))

	renderWasmPluginExports(opts, wp.Spec.Exports)
	renderWasmPluginRuntime(opts, wp.Spec.Runtime)

	if opts.level == levelDetail {
		renderWasmPluginSignature(opts, wp.Spec.Signature)
		renderWasmPluginConfigKeys(opts, wp.Spec.Runtime)
	}
	return nil
}

// renderWasmPluginExports prints the exports table in author order: each entry
// names the export, then its authored capability claim (operates / scope /
// selector), omitting an unset claim line. An empty export list reports
// `(none declared)`.
func renderWasmPluginExports(opts inspectOptions, exports []types.WasmPluginExport) {
	if len(exports) == 0 {
		fmt.Fprintln(opts.out, "Exports: (none declared)")
		return
	}
	fmt.Fprintln(opts.out, "Exports:")
	for _, exp := range exports {
		fmt.Fprintf(opts.out, "  %s\n", exp.Name)
		if exp.Operates != "" {
			fmt.Fprintf(opts.out, "    operates: %s\n", exp.Operates)
		}
		if exp.Scope != "" {
			fmt.Fprintf(opts.out, "    scope: %s\n", exp.Scope)
		}
		if exp.Selector != "" {
			fmt.Fprintf(opts.out, "    selector: %s\n", exp.Selector)
		}
	}
}

// renderWasmPluginRuntime prints the runtime constraints block. An absent
// runtime reports that platform defaults apply across the board; a present
// runtime reports each cap, describing the platform default qualitatively when
// a limit is unset (zero) rather than printing the host's fixed number, so the
// displayed surface single-sources to the loader and cannot drift from it. The
// declared host functions list reports `(none declared)` when empty.
func renderWasmPluginRuntime(opts inspectOptions, rt *types.WasmPluginRuntime) {
	if rt == nil {
		fmt.Fprintln(opts.out, "Runtime: (platform defaults apply)")
		return
	}
	fmt.Fprintln(opts.out, "Runtime:")
	fmt.Fprintf(opts.out, "  maxMemoryPages: %s\n", limitOrDefault(uint64(rt.MaxMemoryPages)))
	fmt.Fprintf(opts.out, "  timeoutMs: %s\n", limitOrDefault(rt.TimeoutMs))
	if len(rt.DeclaredHostFunctions) == 0 {
		fmt.Fprintln(opts.out, "  declaredHostFunctions: (none declared)")
	} else {
		fmt.Fprintln(opts.out, "  declaredHostFunctions:")
		for _, fn := range rt.DeclaredHostFunctions {
			fmt.Fprintf(opts.out, "    - %s\n", fn)
		}
	}
}

// renderWasmPluginSignature prints the optional signature pointer at --detail:
// the scheme and, when authored, the signature and public-key referrer
// references. An absent block reports `(none declared)`.
func renderWasmPluginSignature(opts inspectOptions, sig *types.WasmPluginSignature) {
	if sig == nil {
		fmt.Fprintln(opts.out, "Signature: (none declared)")
		return
	}
	fmt.Fprintln(opts.out, "Signature:")
	fmt.Fprintf(opts.out, "  type: %s\n", sig.Type)
	if sig.SignatureRef != "" {
		fmt.Fprintf(opts.out, "  signatureRef: %s\n", sig.SignatureRef)
	}
	if sig.PublicKeyRef != "" {
		fmt.Fprintf(opts.out, "  publicKeyRef: %s\n", sig.PublicKeyRef)
	}
}

// renderWasmPluginConfigKeys prints the runtime config keys in author order at
// --detail. It is silent when the runtime is absent or carries no config keys —
// the runtime block itself is already reported at the default level.
func renderWasmPluginConfigKeys(opts inspectOptions, rt *types.WasmPluginRuntime) {
	if rt == nil || rt.ConfigKV.Len() == 0 {
		return
	}
	fmt.Fprintln(opts.out, "Config keys:")
	for k := range rt.ConfigKV.All() {
		fmt.Fprintf(opts.out, "  - %s\n", k)
	}
}

// joinedOrNone joins an ordered string slice with commas, or reports
// `(none declared)` when the slice is empty.
func joinedOrNone(xs []string) string {
	if len(xs) == 0 {
		return "(none declared)"
	}
	return strings.Join(xs, ", ")
}

// limitOrDefault formats a runtime limit: the authored value when set, or
// `(platform default applies)` when zero. The default cap is described
// qualitatively rather than printed, so the displayed surface single-sources to
// the loader's cap and cannot drift from it.
func limitOrDefault(v uint64) string {
	if v == 0 {
		return "(platform default applies)"
	}
	return fmt.Sprintf("%d", v)
}
