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
			PluginType: wasm.PluginTypeProvider,
			Targets:    []string{"helm"},
			Exports:    []wasm.PluginExport{{Name: "render_node"}, {Name: "assemble_boundary"}},
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
		ProviderBindings: map[string]string{
			"helm":    "zeta:1.0.0",
			"compose": "alpha:1.0.0",
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
	formatPluginList(&first, embeddedEntries, configuredEntries, embeddedSet)
	formatPluginList(&second, embeddedEntries, configuredEntries, embeddedSet)
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

// TestCollectConfiguredRefsDedupesAndMergesOrigins proves a URI bound at more
// than one socket appears once with its origins merged in a stable order.
func TestCollectConfiguredRefsDedupesAndMergesOrigins(t *testing.T) {
	t.Parallel()
	refs := collectConfiguredRefs(workspaceconfig.Config{
		ProviderBindings: map[string]string{"helm": "shared:1.0.0"},
		Plugins: workspaceconfig.PluginsConfig{
			Linter: []workspaceconfig.PluginRef{{URI: "shared:1.0.0"}},
		},
	})
	if len(refs) != 1 {
		t.Fatalf("want 1 deduped ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].uri != "shared:1.0.0" {
		t.Errorf("uri = %q", refs[0].uri)
	}
	if !strings.Contains(refs[0].origin, "provider_bindings[helm]") ||
		!strings.Contains(refs[0].origin, "plugins.linter[0]") {
		t.Errorf("origins not merged: %q", refs[0].origin)
	}
}
