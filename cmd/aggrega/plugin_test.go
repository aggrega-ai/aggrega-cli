// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestFormatProvenance covers the version/commit display rule: both present,
// each alone, and neither. It drives the formatting off a synthetic descriptor
// so it does not depend on a specific stamped value in the embedded guests.
func TestFormatProvenance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		meta wasm.PluginDescriptorMeta
		want string
	}{
		{"both", wasm.PluginDescriptorMeta{Version: "0.4.0", Commit: "abc1234"}, "0.4.0 (commit abc1234)"},
		{"version only", wasm.PluginDescriptorMeta{Version: "0.4.0"}, "0.4.0"},
		{"commit only", wasm.PluginDescriptorMeta{Commit: "abc1234"}, "(commit abc1234)"},
		{"neither", wasm.PluginDescriptorMeta{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatProvenance(tc.meta); got != tc.want {
				t.Errorf("formatProvenance(%+v) = %q, want %q", tc.meta, got, tc.want)
			}
		})
	}
}

// TestPluginRowCells covers the four-column table's cell derivation: type and
// capabilities are blank for a describe failure, capabilities are blank for a
// cross-cutting linter, and the version cell prefers an abnormal-state label
// (digest drift, or the readiness label / bare marker on a failure) over the
// provenance string.
func TestPluginRowCells(t *testing.T) {
	t.Parallel()

	provider := pluginops.ListedPlugin{
		Descriptor: wasm.PluginDescriptor{
			PluginType:   string(wasm.PluginTypeProvider),
			Capabilities: []string{"helm"},
			Metadata:     &wasm.PluginDescriptorMeta{Version: "0.4.0", Commit: "abc1234"},
		},
	}
	if got := pluginRowType(provider); got != "provider" {
		t.Errorf("provider type cell = %q, want provider", got)
	}
	if got := pluginRowCapabilities(provider); got != "helm" {
		t.Errorf("provider capabilities cell = %q, want helm", got)
	}
	if got := pluginRowVersion(provider); got != "0.4.0 (commit abc1234)" {
		t.Errorf("provider version cell = %q", got)
	}

	linter := pluginops.ListedPlugin{Descriptor: wasm.PluginDescriptor{PluginType: string(wasm.PluginTypeLinter)}}
	if got := pluginRowCapabilities(linter); got != "" {
		t.Errorf("cross-cutting linter capabilities cell should be empty, got %q", got)
	}

	versionCases := []struct {
		name  string
		entry pluginops.ListedPlugin
		want  string
	}{
		{"digest drift", pluginops.ListedPlugin{Readiness: "digest-drift", Descriptor: wasm.PluginDescriptor{Metadata: &wasm.PluginDescriptorMeta{Version: "1.0.0"}}}, "digest-drift"},
		{"failure with readiness", pluginops.ListedPlugin{Readiness: "unresolved", Err: wasm.ErrWasmManifestRejected()}, "unresolved"},
		{"failure without readiness", pluginops.ListedPlugin{Err: wasm.ErrWasmManifestRejected()}, "describe failed"},
		{"unstamped healthy", pluginops.ListedPlugin{Descriptor: wasm.PluginDescriptor{PluginType: string(wasm.PluginTypeProvider)}}, ""},
	}
	for _, tc := range versionCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pluginRowVersion(tc.entry); got != tc.want {
				t.Errorf("pluginRowVersion(%s) = %q, want %q", tc.name, got, tc.want)
			}
			if tc.entry.Err != nil && pluginRowType(tc.entry) != "" {
				t.Errorf("describe-failure type cell should be empty")
			}
		})
	}
}

// TestFormatPluginListTable proves the human table renders each group with its
// header, a four-column header row, and one row per plugin, and that an errored
// entry still names its URI rather than dropping from the listing.
func TestFormatPluginListTable(t *testing.T) {
	t.Parallel()

	embeddedEntries := []pluginops.ListedPlugin{
		{Source: "embedded", URI: "provider/helm",
			Descriptor: wasm.PluginDescriptor{
				PluginType:   string(wasm.PluginTypeProvider),
				Capabilities: []string{"helm"},
				Metadata:     &wasm.PluginDescriptorMeta{Version: "0.4.0", Commit: "abc1234"},
			}},
		{Source: "embedded", URI: "linter/publishing",
			Descriptor: wasm.PluginDescriptor{
				PluginType: string(wasm.PluginTypeLinter),
				Metadata:   &wasm.PluginDescriptorMeta{Version: "0.4.0", Commit: "abc1234"},
			}},
	}
	configuredEntries := []pluginops.ListedPlugin{
		{Source: "configured", URI: "oci://example.com/plugins/broken", Origin: "plugins.linter[0]",
			Err: wasm.ErrWasmManifestRejected()},
	}

	var buf bytes.Buffer
	formatPluginList(&buf, embeddedEntries, configuredEntries)
	got := buf.String()
	for _, want := range []string{
		"Embedded built-ins:",
		"TYPE", "NAME", "CAPABILITIES", "VERSION",
		"provider", "provider/helm", "helm", "0.4.0 (commit abc1234)",
		"linter", "linter/publishing",
		"Configured plugins:",
		"oci://example.com/plugins/broken", "describe failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "Installed CLI plugins:") {
		t.Errorf("the separate installed group must not render; got\n---\n%s", got)
	}
}

// TestFormatPluginListDeterministic proves the listing is byte-identical across
// runs and that configured entries are sorted by URI regardless of input order.
func TestFormatPluginListDeterministic(t *testing.T) {
	t.Parallel()

	// Deliberately out of URI order to prove the caller sorts before rendering.
	configured := collectConfiguredRefs(workspaceconfig.Config{
		Plugins: workspaceconfig.PluginsConfig{
			Providers: []workspaceconfig.PluginRef{
				{Capability: "helm", URI: "zeta:1.0.0"},
				{Capability: "compose", URI: "alpha:1.0.0"},
			},
		},
	})
	var configuredEntries []pluginops.ListedPlugin
	for _, ref := range configured {
		configuredEntries = append(configuredEntries, pluginops.ListedPlugin{
			Source: "configured", URI: ref.uri, Origin: ref.origin,
			Descriptor: wasm.PluginDescriptor{PluginType: string(wasm.PluginTypeProvider)},
		})
	}

	var first, second bytes.Buffer
	formatPluginList(&first, nil, configuredEntries)
	formatPluginList(&second, nil, configuredEntries)
	if first.String() != second.String() {
		t.Errorf("listing is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", first.String(), second.String())
	}

	// alpha:1.0.0 must appear before zeta:1.0.0 in the rendered output.
	out := first.String()
	ai := strings.Index(out, "alpha:1.0.0")
	zi := strings.Index(out, "zeta:1.0.0")
	if ai < 0 || zi < 0 || ai > zi {
		t.Errorf("configured entries not sorted by URI (alpha before zeta): alpha@%d zeta@%d\n%s", ai, zi, out)
	}
}

// TestWritePluginListJSON proves the -o json surface carries the full per-plugin
// detail, groups it deterministically, emits an empty array (not null) for an
// empty group, and reports a describe failure's error rather than dropping the
// plugin.
func TestWritePluginListJSON(t *testing.T) {
	t.Parallel()

	embeddedEntries := []pluginops.ListedPlugin{
		{Source: "embedded", URI: "provider/helm",
			Descriptor: wasm.PluginDescriptor{
				PluginType:   string(wasm.PluginTypeProvider),
				Capabilities: []string{"helm"},
				Exports:      []wasm.PluginExport{{Name: "render_node"}, {Name: "assemble_boundary"}},
				Contract: &wasm.ProviderContract{
					InputKinds:       wasm.InputKinds{Sourced: true, Synthesized: true},
					BoundaryAssembly: "none",
				},
				Metadata: &wasm.PluginDescriptorMeta{Version: "0.4.0", Commit: "abc1234"},
			}},
	}
	// The configured group carries a socket binding that failed to describe and
	// a CLI-extension command that described cleanly; both must appear in the one
	// configured array, and the command must carry its cli type and readiness.
	configuredEntries := []pluginops.ListedPlugin{
		{Source: "configured", URI: "oci://example.com/plugins/broken", Origin: "plugins.linter[0]",
			Err: wasm.ErrWasmManifestRejected()},
		{Source: "installed", URI: "oci://example.com/plugins/cli/greet:1.0.0",
			Origin: "pinned abc1234", Readiness: "ready",
			Descriptor: wasm.PluginDescriptor{
				PluginType: string(wasm.PluginTypeCLI),
				Metadata:   &wasm.PluginDescriptorMeta{Version: "1.0.0"},
			}},
	}

	var first, second bytes.Buffer
	if err := writePluginListJSON(&first, embeddedEntries, configuredEntries); err != nil {
		t.Fatalf("writePluginListJSON: %v", err)
	}
	if err := writePluginListJSON(&second, embeddedEntries, configuredEntries); err != nil {
		t.Fatalf("writePluginListJSON (second): %v", err)
	}
	if first.String() != second.String() {
		t.Errorf("json listing is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", first.String(), second.String())
	}

	// The dropped installed key must not reappear on the surface.
	if strings.Contains(first.String(), `"installed"`) {
		t.Errorf("the separate installed json key must be dropped; got\n%s", first.String())
	}

	var doc struct {
		Embedded []struct {
			URI          string   `json:"uri"`
			Type         string   `json:"type"`
			Version      string   `json:"version"`
			Commit       string   `json:"commit"`
			Capabilities []string `json:"capabilities"`
			InputKinds   []string `json:"inputKinds"`
			Boundary     string   `json:"boundary"`
			Exports      []string `json:"exports"`
		} `json:"embedded"`
		Configured []struct {
			URI       string `json:"uri"`
			Type      string `json:"type"`
			Origin    string `json:"origin"`
			Readiness string `json:"readiness"`
			Error     string `json:"error"`
		} `json:"configured"`
	}
	if err := json.Unmarshal(first.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, first.String())
	}

	if len(doc.Embedded) != 1 {
		t.Fatalf("embedded group len = %d, want 1", len(doc.Embedded))
	}
	e := doc.Embedded[0]
	if e.URI != "provider/helm" || e.Type != "provider" || e.Version != "0.4.0" || e.Commit != "abc1234" || e.Boundary != "none" {
		t.Errorf("embedded entry detail: %+v", e)
	}
	if len(e.Capabilities) != 1 || e.Capabilities[0] != "helm" {
		t.Errorf("capabilities = %v", e.Capabilities)
	}
	if len(e.InputKinds) != 2 || e.InputKinds[0] != "sourced" || e.InputKinds[1] != "synthesized" {
		t.Errorf("inputKinds = %v", e.InputKinds)
	}
	if len(e.Exports) != 2 || e.Exports[0] != "render_node" {
		t.Errorf("exports = %v", e.Exports)
	}
	if len(doc.Configured) != 2 {
		t.Fatalf("configured group len = %d, want 2 (socket binding + cli command)", len(doc.Configured))
	}
	if doc.Configured[0].Error == "" {
		t.Errorf("configured socket-binding error not surfaced: %+v", doc.Configured[0])
	}
	cli := doc.Configured[1]
	if cli.URI != "oci://example.com/plugins/cli/greet:1.0.0" || cli.Type != "cli" || cli.Readiness != "ready" {
		t.Errorf("cli command not folded into configured with its type/readiness: %+v", cli)
	}

	// An empty group must serialize as [] (non-nil after unmarshal), not null.
	var empty bytes.Buffer
	if err := writePluginListJSON(&empty, nil, nil); err != nil {
		t.Fatalf("writePluginListJSON (empty): %v", err)
	}
	if !strings.Contains(empty.String(), `"embedded": []`) || !strings.Contains(empty.String(), `"configured": []`) {
		t.Errorf("empty groups should serialize as [] not null:\n%s", empty.String())
	}
}

// TestInstalledReadiness covers the readiness derivation: an unresolvable plugin
// (no fresh digest), a resolvable-but-undescribable plugin, a digest that drifted
// from the pin, a matched pin, and a not-yet-pinned plugin.
func TestInstalledReadiness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		pinned, fresh string
		err           error
		want          string
	}{
		{"unresolved", "", "", errString("resolve failed"), "unresolved"},
		{"describe-failed", "", "abc", errString("instantiate failed"), "describe-failed"},
		{"drift", "abc", "def", nil, "digest-drift"},
		{"ready matched pin", "abc", "abc", nil, "ready"},
		{"ready not yet pinned", "", "abc", nil, "ready"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := installedReadiness(tc.pinned, tc.fresh, tc.err); got != tc.want {
				t.Errorf("installedReadiness(%q,%q,%v) = %q; want %q", tc.pinned, tc.fresh, tc.err, got, tc.want)
			}
		})
	}
}

// TestFormatPluginList_CLIInConfiguredGroup proves the CLI-extension commands
// render inside the one configured group — with their cli type and their
// digest-pin readiness label (a drift label surfacing in the version cell) — and
// that no separate installed group renders. It also proves the configured header
// still renders with the empty-group placeholder when the group is absent.
func TestFormatPluginList_CLIInConfiguredGroup(t *testing.T) {
	t.Parallel()

	configured := []pluginops.ListedPlugin{
		{
			Source:    "installed",
			URI:       "oci://example/moo:v1",
			Origin:    `command "moo"`,
			Readiness: "ready",
			Descriptor: wasm.PluginDescriptor{
				PluginType: string(wasm.PluginTypeCLI),
				Metadata:   &wasm.PluginDescriptorMeta{Version: "1.0.0"},
			},
		},
		{
			Source:    "installed",
			URI:       "oci://example/drift:v1",
			Origin:    `command "drift"`,
			Readiness: "digest-drift",
			Descriptor: wasm.PluginDescriptor{
				PluginType: string(wasm.PluginTypeCLI),
				Metadata:   &wasm.PluginDescriptorMeta{Version: "1.0.0"},
			},
		},
	}
	var withCLI bytes.Buffer
	formatPluginList(&withCLI, nil, configured)
	got := withCLI.String()
	for _, want := range []string{
		"Configured plugins:",
		"oci://example/moo:v1",
		"cli",
		"1.0.0",
		"oci://example/drift:v1",
		"digest-drift",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("configured-group output missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "Installed CLI plugins:") {
		t.Errorf("the separate installed group must not render; got\n---\n%s", got)
	}

	var empty bytes.Buffer
	formatPluginList(&empty, nil, nil)
	emptyOut := empty.String()
	if !strings.Contains(emptyOut, "Configured plugins:") {
		t.Errorf("configured group header should render even when empty:\n%s", emptyOut)
	}
	if !strings.Contains(emptyOut, "(none)") {
		t.Errorf("empty group should render the (none) placeholder:\n%s", emptyOut)
	}
}

// TestCollectConfiguredRefsDedupesAndMergesOrigins proves a URI bound at more
// than one socket appears once with its origins merged in a stable order.
func TestCollectConfiguredRefsDedupesAndMergesOrigins(t *testing.T) {
	t.Parallel()
	refs := collectConfiguredRefs(workspaceconfig.Config{
		Plugins: workspaceconfig.PluginsConfig{
			Providers: []workspaceconfig.PluginRef{{Capability: "helm", URI: "shared:1.0.0"}},
			Linter:    []workspaceconfig.PluginRef{{URI: "shared:1.0.0"}},
		},
	})
	if len(refs) != 1 {
		t.Fatalf("want 1 deduped ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].uri != "shared:1.0.0" {
		t.Errorf("uri = %q", refs[0].uri)
	}
	if !strings.Contains(refs[0].origin, "plugins.providers[0]") ||
		!strings.Contains(refs[0].origin, "plugins.linter[0]") {
		t.Errorf("origins not merged: %q", refs[0].origin)
	}
}
