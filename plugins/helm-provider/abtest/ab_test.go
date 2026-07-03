// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package abtest holds the host-side A/B proof for the helm-provider Wasm
// guest. It lives in its own directory so the host toolchain compiles it
// without pulling in the guest's wasip1-only main.go (guarded by a wasip1
// build constraint and the go-pdk intrinsics that only link under wasip1).
package abtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestHelmProviderABByteIdentity proves the three locked rulings of the provider
// render foundation at once, end to end:
//
//   - The chart arrives in the payload (SourceProjection), not the filesystem:
//     the host projects the chart's files at compile time via ProjectChartSource,
//     and the guest reconstructs the chart in memory — it touches no filesystem.
//   - The guest passes the production wasm loader's AOT-purity import firewall
//     (firewall ON), which denies filesystem WASI imports. Driving through the
//     production loader — not a raw runtime with a directory mount — is the proof
//     that the guest imports no filesystem capability.
//   - The guest returns each file's serialized bytes and the host writes them
//     verbatim (no host serializer): the decoded RenderNodeOutput files are
//     collected path-to-bytes and compared byte-for-byte to the in-tree engine.
//
// The oracle is the in-tree HelmEngine.Render (map[string][]byte). The candidate
// is the helm-provider guest driven through render_node via the production
// loader. A byte-identical match proves the SourceProjection + shared render core
// + bytes-verbatim path reproduces the in-tree engine exactly.
func TestHelmProviderABByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	repoTestdata := absPath(t, "../../../internal/compiler/renderer/helm/testdata")
	hostChartDir := filepath.Join(repoTestdata, "minimal-chart")

	nodeID := "api"

	// The node's values as they exist in the IR: an ordered map whose
	// numerics are json.Number (the shape the renderer hands the engine —
	// see the renderer's context decode). Both the oracle and the guest
	// receive this exact value shape so the A/B compares the render path,
	// not a value-typing mismatch introduced by the test.
	valuesOrdered := valuesFromJSON(t, `{"image":"api:2.0.0","replicas":5}`)
	values := orderedToPlain(&valuesOrdered)

	// ORACLE — the in-tree Go helm engine, with empty provenance so the
	// comparison isolates the render path rather than the (deliberately
	// out-of-ABI) provenance labels the guest does not receive.
	oracle, err := (&helm.HelmEngine{}).Render(ctx, values, renderer.NodeRenderContext{NodeID: nodeID, Source: hostChartDir})
	if err != nil {
		t.Fatalf("oracle Render: %v", err)
	}

	// CANDIDATE — the Wasm guest through the production loader. The chart
	// arrives in the payload's SourceProjection (projected host-side at compile
	// time), NOT via a filesystem mount. The guest reads NodeID and values from
	// the node projection and the chart's files from the source projection.
	proj, err := helm.ProjectChartSource(hostChartDir)
	if err != nil {
		t.Fatalf("ProjectChartSource: %v", err)
	}
	input := renderer.RenderNodeInput{
		Node:             renderer.IRNodeProjection{ID: nodeID, Values: valuesOrdered},
		Context:          renderctx.RenderContext{},
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

	// The host runs no serializer: it writes each file's bytes verbatim, so
	// collecting is a path-to-bytes map (the production host collector's
	// behaviour), with a dup-path guard the guest is trusted not to trip here.
	candidate := make(map[string][]byte, len(out.Files))
	for _, f := range out.Files {
		candidate[f.Path] = f.Content
	}

	assertByteIdentical(t, oracle, candidate)
}

func assertByteIdentical(t *testing.T, oracle, candidate map[string][]byte) {
	t.Helper()
	if !reflect.DeepEqual(keySet(oracle), keySet(candidate)) {
		t.Fatalf("file-key sets differ:\n oracle:    %v\n candidate: %v", keySet(oracle), keySet(candidate))
	}
	for k, want := range oracle {
		if got := candidate[k]; string(want) != string(got) {
			t.Errorf("BYTE MISMATCH for %q:\n--- oracle ---\n%s\n--- candidate ---\n%s", k, want, got)
		} else {
			t.Logf("BYTE-IDENTICAL %q (%d bytes)", k, len(want))
		}
	}
}

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

// loadGuest loads the guest through the production wasm loader (AOT-purity
// import firewall ON, deterministic capability-denied ModuleConfig, synthetic
// argv from the loader), carrying inputs as the external-input service — nil
// means no service, so any guest fetch receives the in-band not-available
// envelope. It does NOT mount any host directory: the chart travels in the
// payload. If the guest imported a filesystem WASI capability, Loader.Load
// would reject it at the firewall and this call would fail with the offending
// import named — that rejection is a real failure of the proof, never
// something to route around with a mount.
func loadGuest(t *testing.T, ctx context.Context, wasmPath string, inputs wasm.FetchService) *wasm.Plugin {
	t.Helper()
	loader := &wasm.Loader{ExternalInputs: inputs}
	plugin, err := loader.Load(ctx, wasm.PluginSpec{
		Name:       "helm",
		PluginType: wasm.PluginTypeProvider,
		Path:       wasmPath,
		// A standard-Go SDK guest declares a linear-memory minimum above the
		// tight platform default cap; raise this plugin's own cap the same way
		// its catalog entity's runtime block does. Bounded, never unbounded.
		MaxMemoryPages: 4096,
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

// buildPluginWasm compiles the plugin to GOOS=wasip1 GOARCH=wasm and returns
// the artifact path. Fails the test when the build fails.
func buildPluginWasm(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "helm-provider.wasm")
	// -buildmode=c-shared produces a WASI reactor (no main entry; the
	// //go:wasmexport functions are the exports) whose _initialize the
	// runtime calls before the first export, so the Go runtime is
	// initialized before render_node runs.
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "..")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wasip1 build failed: %v\n%s", err, b)
	}
	return out
}

func absPath(t *testing.T, rel string) string {
	t.Helper()
	a, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("abs %s: %v", rel, err)
	}
	return a
}

// valuesFromJSON decodes a JSON object into an ordered value map through
// the same OrderedMap[any] JSON decode the guest performs off the wire, so
// numerics land as json.Number and nested objects as *OrderedMap[any] on
// both sides of the A/B.
func valuesFromJSON(t *testing.T, j string) types.OrderedMap[any] {
	t.Helper()
	om := types.NewOrderedMap[any]()
	if err := om.UnmarshalJSON([]byte(j)); err != nil {
		t.Fatalf("decode values %q: %v", j, err)
	}
	return *om
}

// orderedToPlain flattens an ordered value map into the plain map[string]any
// the helm engine consumes, recursing into nested ordered maps. It mirrors
// the guest's own flattening so the oracle receives the same value shape.
func orderedToPlain(m *types.OrderedMap[any]) map[string]any {
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = plainVal(v)
	}
	return out
}

func plainVal(v any) any {
	switch t := v.(type) {
	case *types.OrderedMap[any]:
		return orderedToPlain(t)
	case types.OrderedMap[any]:
		return orderedToPlain(&t)
	case []any:
		for i := range t {
			t[i] = plainVal(t[i])
		}
		return t
	default:
		return v
	}
}
