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
// The node projection carries the full provenance the render path stamps onto
// every manifest — boundary path, deployment wave, Recipe identity, component-id
// override, and build identity — fed identically to both sides, so the rendered
// manifests carry real provenance labels and annotations.
//
// The oracle is the in-tree HelmEngine.Render (map[string][]byte). The candidate
// is the helm-provider guest driven through render_node via the production
// loader. A byte-identical match — with provenance labels and annotations
// present — proves the SourceProjection + shared render core + provenance
// carriage + bytes-verbatim path reproduces the in-tree engine exactly.
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

	// The indentation width flows identically to both sides — the oracle's
	// NodeRenderContext.YAMLIndent and the wire's RenderContext.YAMLIndent — so
	// the guest applies the indent carried on the render wire rather than a
	// hardcoded width. The default case (indent 0 → both clamp to 2) covers the
	// absent-indent path; the non-default case (indent 4) re-marshals the
	// injected label/annotation blocks visibly wider, so a byte-identical match
	// there proves the carriage, and the cross-case guard below proves the
	// indent is observable (indent 4 differs from the default) rather than a
	// no-op.
	cases := []struct {
		name   string
		indent int
	}{
		{"default-indent", 0},
		{"indent-4", 4},
	}

	captured := make(map[string]map[string][]byte, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ORACLE — the in-tree Go helm engine, fed real provenance so the
			// A/B exercises the provenance labels and annotations the render
			// path stamps onto every manifest, and the case's indent so the
			// output serializes at that width.
			oracleCtx := renderer.NodeRenderContext{NodeID: nodeID, Source: hostChartDir, YAMLIndent: tc.indent}
			stampOracleProvenance(&oracleCtx)
			oracle, err := (&helm.HelmEngine{}).Render(ctx, values, oracleCtx)
			if err != nil {
				t.Fatalf("oracle Render: %v", err)
			}

			// CANDIDATE — the Wasm guest through the production loader. The chart
			// arrives in the payload's SourceProjection (projected host-side at
			// compile time), NOT via a filesystem mount. The guest reads NodeID,
			// values, the same provenance, and the same indent from the render
			// context, and rebuilds an identical render context; the chart's
			// files arrive in the source projection.
			proj, err := helm.ProjectChartSource(hostChartDir)
			if err != nil {
				t.Fatalf("ProjectChartSource: %v", err)
			}
			node := renderer.IRNodeProjection{ID: nodeID, Values: valuesOrdered}
			stampCandidateProvenance(&node)
			input := renderer.RenderNodeInput{
				Node:             node,
				Context:          renderctx.RenderContext{YAMLIndent: tc.indent},
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

			// The host runs no serializer: it writes each file's bytes verbatim,
			// so collecting is a path-to-bytes map (the production host
			// collector's behaviour), with a dup-path guard the guest is trusted
			// not to trip here.
			candidate := make(map[string][]byte, len(out.Files))
			for _, f := range out.Files {
				candidate[f.Path] = f.Content
			}

			assertByteIdentical(t, oracle, candidate)
			captured[tc.name] = candidate
		})
	}

	// The indent must be observable, or a byte-identical match at every case
	// proves nothing about the carriage. Indent 4 re-marshals the injected
	// blocks wider than the default, so its bytes must differ.
	if def, ok := captured["default-indent"]; ok {
		if wide, ok := captured["indent-4"]; ok && byteMapsEqual(def, wide) {
			t.Fatal("indent 4 produced byte-identical output to the default indent; the indent carriage is not observable, so the A/B is a no-op")
		}
	}
}

// abProv is the single source of the concrete provenance every A/B feeds
// identically to both sides — the oracle as NodeRenderContext fields, the
// candidate as IRNodeProjection fields — so the guest rebuilds a byte-identical
// render context. A non-zero SyncWave and every identity field set exercise the
// provenance label and annotation stamping the shared render path performs, so a
// byte-identical match proves the guest carries provenance across the ABI
// exactly as the in-tree engine stamps it.
var abProv = struct {
	BoundaryPath     string
	SyncWave         int
	RecipeName       string
	RecipeVersion    string
	LabelComponentID string
	CompilerVersion  string
	AggregateName    string
	Environment      string
}{
	BoundaryPath:     "aws-prod/payments",
	SyncWave:         3,
	RecipeName:       "core/web-service/helm",
	RecipeVersion:    "1.4.2",
	LabelComponentID: "payments-api",
	CompilerVersion:  "1.0.0",
	AggregateName:    "payments",
	Environment:      "prod",
}

// stampOracleProvenance sets the shared provenance on an oracle
// NodeRenderContext, leaving the caller's NodeID/Source/values in place.
func stampOracleProvenance(nodeCtx *renderer.NodeRenderContext) {
	nodeCtx.BoundaryPath = abProv.BoundaryPath
	nodeCtx.SyncWave = abProv.SyncWave
	nodeCtx.RecipeName = abProv.RecipeName
	nodeCtx.RecipeVersion = abProv.RecipeVersion
	nodeCtx.LabelComponentID = abProv.LabelComponentID
	nodeCtx.Provenance = renderer.ProvenanceContext{
		CompilerVersion: abProv.CompilerVersion,
		AggregateName:   abProv.AggregateName,
		Environment:     abProv.Environment,
	}
}

// stampCandidateProvenance sets the shared provenance on a candidate
// IRNodeProjection, mirroring stampOracleProvenance so both sides of the A/B
// carry identical provenance and the guest rebuilds the same render context.
func stampCandidateProvenance(node *renderer.IRNodeProjection) {
	node.BoundaryPath = abProv.BoundaryPath
	node.SyncWave = abProv.SyncWave
	node.RecipeName = abProv.RecipeName
	node.RecipeVersion = abProv.RecipeVersion
	node.LabelComponentID = abProv.LabelComponentID
	node.Provenance = renderer.NodeProvenance{
		CompilerVersion: abProv.CompilerVersion,
		AggregateName:   abProv.AggregateName,
		Environment:     abProv.Environment,
	}
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

// byteMapsEqual reports whether two path-to-bytes maps carry the same key set
// and byte-identical content for every key.
func byteMapsEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || string(va) != string(vb) {
			return false
		}
	}
	return true
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
