// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

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

// TestRenderPluginEntryProvenanceDisplay proves the version line renders when the
// descriptor carries provenance and is omitted when it does not.
func TestRenderPluginEntryProvenanceDisplay(t *testing.T) {
	t.Parallel()

	stamped := pluginListEntry{
		source: "embedded",
		uri:    "oci://hub.aggrega.ai/plugin/provider/helm",
		desc: wasm.PluginDescriptor{
			PluginType:          wasm.PluginTypeProvider,
			CompatibleProviders: []string{"helm"},
			Exports:             []wasm.PluginExport{{Name: "render_node"}, {Name: "assemble_boundary"}},
			Capabilities: wasm.Capabilities{
				InputKinds:       wasm.InputKinds{Sourced: true, Synthesized: true},
				BoundaryAssembly: "merge",
			},
			Metadata: wasm.PluginDescriptorMeta{Version: "0.4.0", Commit: "abc1234"},
		},
	}
	var stampedBuf bytes.Buffer
	renderPluginEntry(&stampedBuf, stamped, map[string]bool{})
	got := stampedBuf.String()
	for _, want := range []string{
		"oci://hub.aggrega.ai/plugin/provider/helm",
		"type:         provider",
		"version:      0.4.0 (commit abc1234)",
		"targets:      helm",
		"input kinds:  sourced, synthesized",
		"boundary:     merge",
		"exports:      render_node, assemble_boundary",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stamped entry output missing %q\n---\n%s", want, got)
		}
	}

	unstamped := pluginListEntry{
		source: "embedded",
		uri:    "oci://hub.aggrega.ai/plugin/provider/compose",
		desc: wasm.PluginDescriptor{
			PluginType: wasm.PluginTypeProvider,
			Exports:    []wasm.PluginExport{{Name: "render_node"}},
		},
	}
	var unstampedBuf bytes.Buffer
	renderPluginEntry(&unstampedBuf, unstamped, map[string]bool{})
	if strings.Contains(unstampedBuf.String(), "version:") {
		t.Errorf("unstamped entry should omit the version line, got:\n%s", unstampedBuf.String())
	}
}

// TestRenderPluginEntryDescribeError proves a describe failure is surfaced per
// entry rather than dropping the plugin from the listing.
func TestRenderPluginEntryDescribeError(t *testing.T) {
	t.Parallel()
	entry := pluginListEntry{
		source: "configured",
		uri:    "oci://example.com/plugins/broken",
		origin: "plugins.linter[0]",
		err:    wasm.ErrWasmManifestRejected,
	}
	var buf bytes.Buffer
	renderPluginEntry(&buf, entry, map[string]bool{})
	got := buf.String()
	if !strings.Contains(got, "oci://example.com/plugins/broken") {
		t.Errorf("error entry should still name the URI, got:\n%s", got)
	}
	if !strings.Contains(got, "error:") {
		t.Errorf("error entry should carry an error line, got:\n%s", got)
	}
	if strings.Contains(got, "type:") {
		t.Errorf("error entry should not render metadata, got:\n%s", got)
	}
}

// TestFormatPluginListDeterministic proves the listing is byte-identical across
// runs and that configured entries are sorted by URI regardless of input order.
func TestFormatPluginListDeterministic(t *testing.T) {
	t.Parallel()

	embeddedEntries := []pluginListEntry{
		{source: "embedded", uri: "oci://hub.aggrega.ai/plugin/provider/helm",
			desc: wasm.PluginDescriptor{PluginType: wasm.PluginTypeProvider}},
	}
	// Deliberately out of URI order to prove the caller sorts before rendering.
	configured := collectConfiguredRefs(workspaceconfig.Config{
		Plugins: workspaceconfig.PluginsConfig{
			Providers: []workspaceconfig.ProviderRef{
				{Target: "helm", URI: "zeta:1.0.0"},
				{Target: "compose", URI: "alpha:1.0.0"},
			},
		},
	})
	var configuredEntries []pluginListEntry
	for _, ref := range configured {
		configuredEntries = append(configuredEntries, pluginListEntry{
			source: "configured", uri: ref.uri, origin: ref.origin,
			desc: wasm.PluginDescriptor{PluginType: wasm.PluginTypeProvider},
		})
	}

	embeddedSet := map[string]bool{"oci://hub.aggrega.ai/plugin/provider/helm": true}

	var first, second bytes.Buffer
	formatPluginList(&first, embeddedEntries, configuredEntries, nil, embeddedSet)
	formatPluginList(&second, embeddedEntries, configuredEntries, nil, embeddedSet)
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

// TestInstalledReadiness covers the readiness derivation: an unresolvable plugin
// (no fresh digest), a resolvable-but-undescribable plugin, a digest that drifted
// from the pin, a matched pin, and a not-yet-pinned plugin.
func TestInstalledReadiness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		pinned, fresh string
		err          error
		want         string
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

// TestRenderPluginEntry_ReadinessLine proves the readiness line renders for an
// installed entry — including on a resolve error, where it appears alongside the
// error line — and is omitted for a group carrying no readiness signal.
func TestRenderPluginEntry_ReadinessLine(t *testing.T) {
	t.Parallel()

	drifted := pluginListEntry{
		source:    "installed",
		uri:       "oci://example/moo:v1",
		origin:    `command "moo"`,
		readiness: "digest-drift",
		desc:      wasm.PluginDescriptor{PluginType: wasm.PluginTypeCLI},
	}
	var driftBuf bytes.Buffer
	renderPluginEntry(&driftBuf, drifted, map[string]bool{})
	if !strings.Contains(driftBuf.String(), "readiness:    digest-drift") {
		t.Errorf("installed entry missing readiness line:\n%s", driftBuf.String())
	}

	unresolved := pluginListEntry{
		source:    "installed",
		uri:       "oci://example/gone:v1",
		readiness: "unresolved",
		err:       wasm.ErrWasmManifestRejected,
	}
	var unresBuf bytes.Buffer
	renderPluginEntry(&unresBuf, unresolved, map[string]bool{})
	got := unresBuf.String()
	if !strings.Contains(got, "readiness:    unresolved") {
		t.Errorf("unresolved entry should still show readiness before the error:\n%s", got)
	}
	if !strings.Contains(got, "error:") {
		t.Errorf("unresolved entry should carry the error line:\n%s", got)
	}

	// An embedded entry carries no readiness label, so no readiness line renders.
	embeddedEntry := pluginListEntry{
		source: "embedded",
		uri:    "oci://hub.aggrega.ai/plugin/provider/helm",
		desc:   wasm.PluginDescriptor{PluginType: wasm.PluginTypeProvider},
	}
	var embBuf bytes.Buffer
	renderPluginEntry(&embBuf, embeddedEntry, map[string]bool{})
	if strings.Contains(embBuf.String(), "readiness:") {
		t.Errorf("embedded entry must not render a readiness line:\n%s", embBuf.String())
	}
}

// TestFormatPluginList_InstalledGroup proves the third group renders with its
// header, its entries, and the empty-group placeholder when absent.
func TestFormatPluginList_InstalledGroup(t *testing.T) {
	t.Parallel()

	installed := []pluginListEntry{{
		source:    "installed",
		uri:       "oci://example/moo:v1",
		origin:    `command "moo"`,
		readiness: "ready",
		desc:      wasm.PluginDescriptor{PluginType: wasm.PluginTypeCLI},
	}}
	var withInstalled bytes.Buffer
	formatPluginList(&withInstalled, nil, nil, installed, map[string]bool{})
	got := withInstalled.String()
	for _, want := range []string{
		"Installed CLI plugins:",
		"oci://example/moo:v1",
		`origin:       command "moo"`,
		"readiness:    ready",
		"type:         cli",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("installed-group output missing %q\n---\n%s", want, got)
		}
	}

	var empty bytes.Buffer
	formatPluginList(&empty, nil, nil, nil, map[string]bool{})
	if !strings.Contains(empty.String(), "Installed CLI plugins:") {
		t.Errorf("installed group header should render even when empty:\n%s", empty.String())
	}
}

// TestCollectConfiguredRefsDedupesAndMergesOrigins proves a URI bound at more
// than one socket appears once with its origins merged in a stable order.
func TestCollectConfiguredRefsDedupesAndMergesOrigins(t *testing.T) {
	t.Parallel()
	refs := collectConfiguredRefs(workspaceconfig.Config{
		Plugins: workspaceconfig.PluginsConfig{
			Providers: []workspaceconfig.ProviderRef{{Target: "helm", URI: "shared:1.0.0"}},
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
