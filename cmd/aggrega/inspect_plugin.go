// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"go.yaml.in/yaml/v3"
)

// inspectRawPlugin prints the pre-publish authoring view of a raw .wasm artifact
// not yet published or stamped into a Plugin entity: the per-export input/output
// contract the guest declares in its describe() self-report, and the host
// functions it imports.
//
// The export contract is the author-facing "what do I pass / what do I get" view —
// for each export, its pipeline operation (operates / scope / selector) and the
// JSON Schema it declares for its step-local input and its returned outputs. It is
// read from the guest's own describe() through the shared describe-only load path,
// the same one publish and validate read, so the guest is instantiated under inert
// host-function stubs and reaches no real host capability while being described. An
// export that declares no input or output contract omits that line.
//
// The export contract is best-effort. This view is a permissive diagnostic, not a
// validation gate, so a raw .wasm whose describe() self-report is absent or not yet
// wired still has its host functions shown — the export block is skipped and
// reported as unavailable, and the command does not fail.
//
// The host functions are derived directly from the binary's import section via the
// single derivation primitive (wasm.DeriveHostFunctions), the same one the record-
// sync tooling writes with and the loader registers from, so what an author sees
// here is exactly the set publish will require the entity to have recorded. The
// set is labelled as derived-from-binary and not-yet-recorded to distinguish it
// from the entity-read view inspect shows for a published Plugin.
func inspectRawPlugin(out io.Writer, wasmPath string) error {
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return fmt.Errorf("read .wasm %q: %w", wasmPath, err)
	}
	identity := filepath.Base(wasmPath)
	derived, err := wasm.DeriveHostFunctions(identity, wasmBytes)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Plugin .wasm: %s\n", wasmPath)

	// describe() is best-effort here. Inspect on a raw .wasm is a permissive
	// pre-publish diagnostic view, not a validation gate: its always-available job
	// is to show the host functions the binary imports — the set the entity must
	// record — and that works for any reactor .wasm, including one whose describe()
	// self-report is absent or not yet wired. So a describe failure skips only the
	// export-contract block and never aborts; the derived host functions below still
	// print. The Loud Guardrail for a guest that cannot describe itself is
	// `aggrega validate --plugin`, not inspect. The declared plugin type is left
	// empty because a bare .wasm carries no entity front and the describe-only load
	// admits any type, reading the contract from the guest rather than an entity claim.
	if desc, err := wasm.DescribePluginBytes(context.Background(), identity, wasmBytes, wasm.DescribeMaxMemoryPages, ""); err == nil {
		renderRawPluginExports(out, desc.Exports)
	} else {
		fmt.Fprintln(out, "Exports: (unavailable — this .wasm did not report a describe() contract)")
	}

	if len(derived) == 0 {
		fmt.Fprintln(out, "hostFunctions (derived from binary, not yet stamped): (none)")
		return nil
	}
	fmt.Fprintln(out, "hostFunctions (derived from binary, not yet stamped):")
	for _, fn := range derived {
		fmt.Fprintf(out, "  - %s\n", fn)
	}
	return nil
}

// renderRawPluginExports prints the describe()-reported exports of a raw .wasm in
// declared order: each export's name, its pipeline operation claim (operates /
// scope / selector), and the JSON Schema it declares for its step-local input and
// its returned outputs. Each optional line is omitted when the export declares
// none. This renders the describe descriptor's own export type; it is deliberately
// separate from renderPluginExports, which renders the authored-entity export type
// and carries no input/output contract.
func renderRawPluginExports(out io.Writer, exports []wasm.PluginExport) {
	if len(exports) == 0 {
		fmt.Fprintln(out, "Exports: (none declared)")
		return
	}
	fmt.Fprintln(out, "Exports:")
	for _, exp := range exports {
		fmt.Fprintf(out, "  %s\n", exp.Name)
		if exp.Operates != "" {
			fmt.Fprintf(out, "    operates: %s\n", exp.Operates)
		}
		if exp.Scope != "" {
			fmt.Fprintf(out, "    scope: %s\n", exp.Scope)
		}
		if exp.Selector != "" {
			fmt.Fprintf(out, "    selector: %s\n", exp.Selector)
		}
		if s := compactSchema(exp.Input); s != "" {
			fmt.Fprintf(out, "    input: %s\n", s)
		}
		if s := compactSchema(exp.Output); s != "" {
			fmt.Fprintf(out, "    output: %s\n", s)
		}
	}
}

// compactSchema renders a declared JSON Schema document as a single-line string
// with insignificant whitespace removed, preserving the document's key order. It
// returns the empty string when the export declares no schema (the raw message is
// empty), which callers treat as the signal to omit the line.
func compactSchema(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return buf.String()
}

// renderPluginFile renders a Plugin authored as a standalone file. The
// plugin's declared capability surface is shown first: the content-addressed
// .wasm artifact reference, the socket type it implements, the ABI version
// it targets, and the targets it claims compatibility with. The
// exports table names each entry point and its authored capability claim
// (operates / scope / selector); the runtime block reports the execution
// constraints, describing a platform default qualitatively when a limit is
// unset rather than printing a fixed number, so the displayed surface never
// drifts from the loader's actual cap.
//
//   - levelList: identifier plus the plugin type and export count.
//   - levelDefault: plugin, plugin type, ABI version, targets,
//     the exports table, and the runtime block.
//   - levelDetail: adds the signature pointer and the runtime config keys.
func renderPluginFile(_ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var wp types.Plugin
	if err := yaml.Unmarshal(data, &wp); err != nil {
		return fmt.Errorf("parse Plugin at %q: %w", path, err)
	}
	identifier := identifierVersion(wp.Metadata.Name, wp.Metadata.Version)

	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		fmt.Fprintf(opts.out, "type: %s, exports: %d\n",
			typeOrNone(wp.Metadata.PluginType), len(wp.Spec.Exports))
		return nil
	}

	renderHeader(opts, "Plugin", identifier)

	plugin := wp.ArtifactReference()
	if plugin == "" {
		plugin = "(none declared)"
	}
	fmt.Fprintf(opts.out, "Plugin: %s\n", plugin)
	fmt.Fprintf(opts.out, "Plugin type: %s\n", typeOrNone(wp.Metadata.PluginType))

	abiVersion := wp.Spec.AbiVersion
	if abiVersion == "" {
		abiVersion = "(none declared)"
	}
	fmt.Fprintf(opts.out, "ABI version: %s\n", abiVersion)
	fmt.Fprintf(opts.out, "Capabilities: %s\n", joinedOrNone(wp.DeclaredCapabilities()))

	renderPluginExports(opts, wp.Spec.Exports)
	renderPluginRuntime(opts, wp.Spec.Runtime)

	if opts.level == levelDetail {
		renderPluginSignature(opts, wp.Spec.Signature)
		renderPluginConfigKeys(opts, wp.Spec.Runtime)
	}
	return nil
}

// renderPluginExports prints the exports table in author order: each entry
// names the export, then its authored capability claim (operates / scope /
// selector), omitting an unset claim line. An empty export list reports
// `(none declared)`.
func renderPluginExports(opts inspectOptions, exports []types.PluginExport) {
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

// renderPluginRuntime prints the runtime constraints block. An absent
// runtime reports that platform defaults apply across the board; a present
// runtime reports each cap, describing the platform default qualitatively when
// a limit is unset (zero) rather than printing the host's fixed number, so the
// displayed surface single-sources to the loader and cannot drift from it. The
// host functions list is the machine-owned stamp read straight from the entity
// (the published .wasm's imports, snapshotted at publish); it reports `(none)`
// when empty.
func renderPluginRuntime(opts inspectOptions, rt *types.PluginRuntime) {
	if rt == nil {
		fmt.Fprintln(opts.out, "Runtime: (platform defaults apply)")
		return
	}
	fmt.Fprintln(opts.out, "Runtime:")
	fmt.Fprintf(opts.out, "  maxMemoryPages: %s\n", limitOrDefault(uint64(rt.MaxMemoryPages)))
	fmt.Fprintf(opts.out, "  timeoutMs: %s\n", limitOrDefault(rt.TimeoutMs))
	if len(rt.HostFunctions) == 0 {
		fmt.Fprintln(opts.out, "  hostFunctions: (none)")
	} else {
		fmt.Fprintln(opts.out, "  hostFunctions:")
		for _, fn := range rt.HostFunctions {
			fmt.Fprintf(opts.out, "    - %s\n", fn)
		}
	}
}

// renderPluginSignature prints the optional signature pointer at --detail:
// the scheme and, when authored, the signature and public-key referrer
// references. An absent block reports `(none declared)`.
func renderPluginSignature(opts inspectOptions, sig *types.PluginSignature) {
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

// renderPluginConfigKeys prints the runtime config keys in author order at
// --detail. It is silent when the runtime is absent or carries no config keys —
// the runtime block itself is already reported at the default level.
func renderPluginConfigKeys(opts inspectOptions, rt *types.PluginRuntime) {
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

// typeOrNone reports a scalar value, or `(none declared)` when it is empty.
func typeOrNone(s string) string {
	if s == "" {
		return "(none declared)"
	}
	return s
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
