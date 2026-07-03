// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package abtest holds the host-side A/B proof for the helmchart-provider Wasm
// guest. It lives in its own directory so the host toolchain compiles it without
// pulling in the guest's wasip1-only main.go (guarded by a wasip1 build
// constraint and the go-pdk intrinsics that only link under wasip1).
package abtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestHelmChartProviderSourcedABByteIdentity proves the sourced render path (a
// Recipe declaring source: <chart dir>) reproduces the in-tree helm-chart engine
// byte-for-byte through the provider Wasm guest, end to end:
//
//   - The chart's files arrive in the payload (SourceProjection), not the
//     filesystem: the host projects them at compile time via ProjectSourcedChart,
//     and the guest reconstructs the sub-chart file set in memory — it touches no
//     filesystem.
//   - The guest passes the production wasm loader's AOT-purity import firewall
//     (firewall ON via invokeGuest), which denies filesystem WASI imports.
//     Driving through the production loader — not a raw runtime with a directory
//     mount — is the proof that the guest imports no filesystem capability.
//   - The guest returns each file's bytes and the host writes them verbatim (no
//     host serializer): the decoded RenderNodeOutput files are collected
//     path-to-bytes and compared byte-for-byte to the in-tree engine.
//
// helm-chart copies chart files verbatim, so the output is invariant to values,
// indent, provenance, and annotations; the non-vacuity guard instead asserts the
// copy carried the full multi-file chart (Chart.yaml plus its templates), so a
// byte-identical match is not a match over an empty or single-file set.
func TestHelmChartProviderSourcedABByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	chartDir := chartFixtureDir(t)
	nodeID := "api"
	nodeCtx := renderer.NodeRenderContext{NodeID: nodeID, Source: chartDir}

	// ORACLE — the in-tree Go helm-chart engine. helm-chart ignores values on the
	// verbatim-copy path, so a nil values map matches its contract.
	oracle, err := (&helmchart.HelmEngine{}).Render(ctx, nil, nodeCtx)
	if err != nil {
		t.Fatalf("oracle Render: %v", err)
	}
	if len(oracle) < 2 {
		t.Fatalf("fixture chart produced %d files; want at least Chart.yaml plus a template so the multi-file copy is exercised", len(oracle))
	}

	// CANDIDATE — the Wasm guest through the production loader. The chart's files
	// arrive in the payload's SourceProjection (projected host-side at compile
	// time), NOT via a filesystem mount.
	proj, err := helmchart.ProjectSourcedChart(chartDir)
	if err != nil {
		t.Fatalf("ProjectSourcedChart: %v", err)
	}
	input := renderer.RenderNodeInput{
		Node:             renderer.IRNodeProjection{ID: nodeID},
		SourceProjection: proj,
	}
	payload, err := input.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	rawOut := invokeGuest(t, ctx, wasmPath, payload)

	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(rawOut); err != nil {
		t.Fatalf("decode RenderNodeOutput: %v (raw: %s)", err, rawOut)
	}

	candidate := make(map[string][]byte, len(out.Files))
	for _, f := range out.Files {
		candidate[f.Path] = f.Content
	}

	assertByteIdentical(t, oracle, candidate)
}

// chartFixtureDir returns the absolute path to the self-contained sourced-chart
// fixture (Chart.yaml + values.yaml + two templates), exercising a multi-file,
// nested-directory verbatim copy.
func chartFixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "sourced-chart"))
	if err != nil {
		t.Fatalf("abs fixture path: %v", err)
	}
	return dir
}

// assertByteIdentical fails the test unless the oracle and candidate carry the
// same file-key set and byte-identical content for every key.
func assertByteIdentical(t *testing.T, oracle, candidate map[string][]byte) {
	t.Helper()
	if want, got := keySet(oracle), keySet(candidate); !stringsEqual(want, got) {
		t.Fatalf("file-key sets differ:\n oracle:    %v\n candidate: %v", want, got)
	}
	for k, want := range oracle {
		if got := candidate[k]; string(want) != string(got) {
			t.Errorf("BYTE MISMATCH for %q:\n--- oracle ---\n%s\n--- candidate ---\n%s", k, want, got)
		} else {
			t.Logf("BYTE-IDENTICAL %q (%d bytes)", k, len(want))
		}
	}
}

// keySet returns m's keys in lexical order.
func keySet(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// stringsEqual reports whether two ordered string slices are element-wise equal.
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// loadGuest loads the guest through the production wasm loader (AOT-purity import
// firewall ON, deterministic capability-denied ModuleConfig, synthetic argv from
// the loader). It does NOT mount any host directory: the source travels in the
// payload. If the guest imported a filesystem WASI capability, Loader.Load would
// reject it at the firewall and this call would fail with the offending import
// named — that rejection is a real failure of the proof, never something to route
// around with a mount.
func loadGuest(t *testing.T, ctx context.Context, wasmPath string, inputs wasm.FetchService) *wasm.Plugin {
	t.Helper()
	loader := &wasm.Loader{ExternalInputs: inputs}
	plugin, err := loader.Load(ctx, wasm.PluginSpec{
		Name:       "helm-chart",
		PluginType: wasm.PluginTypeProvider,
		Path:       wasmPath,
		// A standard-Go SDK guest declares a linear-memory minimum above the
		// tight platform default cap; raise this plugin's own cap the same way
		// its catalog entity's runtime block would. Bounded, never unbounded.
		MaxMemoryPages: 4096,
		// The guest's runtime initialization is seconds-scale and slower where
		// the host runs the Wasm engine without native code generation, so raise
		// the per-call cap above the tight platform default — a legitimate render
		// never false-times-out. A byte-identity comparison is unaffected by the cap.
		TimeoutMs: 300000,
	})
	if err != nil {
		t.Fatalf("production loader rejected the guest (firewall): %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })
	return plugin
}

// invokeGuest loads the guest with no external-input service and calls
// render_node, failing the test on any call error.
func invokeGuest(t *testing.T, ctx context.Context, wasmPath string, payload []byte) []byte {
	t.Helper()
	plugin := loadGuest(t, ctx, wasmPath, nil)
	out, err := plugin.Call(ctx, "render_node", payload)
	if err != nil {
		t.Fatalf("guest render_node call: %v", err)
	}
	return out
}

// buildPluginWasm compiles the plugin to GOOS=wasip1 GOARCH=wasm and returns the
// artifact path. Fails the test when the build fails.
func buildPluginWasm(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "helmchart-provider.wasm")
	// -buildmode=c-shared produces a WASI reactor (no main entry; the
	// //go:wasmexport functions are the exports) whose _initialize the runtime
	// calls before the first export, so the Go runtime is initialized before
	// render_node runs.
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "..")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wasip1 build failed: %v\n%s", err, b)
	}
	return out
}
