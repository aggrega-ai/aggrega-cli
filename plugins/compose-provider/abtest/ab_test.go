// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package abtest holds the host-side A/B proof for the compose-provider Wasm
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
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestComposeProviderSourcedABByteIdentity proves the sourced render path (a
// Recipe declaring source: <compose file> plus a values overlay) reproduces the
// in-tree compose engine byte-for-byte through the provider Wasm guest, end to
// end:
//
//   - The base compose file arrives in the payload (SourceProjection), not the
//     filesystem: the host projects it at compile time via ProjectComposeSource,
//     and the guest reconstructs it in memory — it touches no filesystem.
//   - The guest passes the production wasm loader's AOT-purity import firewall
//     (firewall ON), which denies filesystem WASI imports. Driving through the
//     production loader — not a raw runtime with a directory mount — is the proof
//     that the guest imports no filesystem capability.
//   - The guest returns the merged fragment's serialized bytes and the host
//     writes them verbatim (no host serializer): the decoded RenderNodeOutput
//     files are collected path-to-bytes and compared byte-for-byte to the in-tree
//     engine.
//
// The node projection carries the full provenance the render path stamps onto
// every service — boundary path, deployment wave, Recipe identity, component-id
// override, and build identity — fed identically to both sides, so the merged
// fragment carries real provenance labels.
func TestComposeProviderSourcedABByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	// A self-contained single-service base compose file the in-tree sourced tests
	// already ship; the overlay adds an environment map, exercising the deep-merge.
	baseCompose := absPath(t, "../../../internal/compiler/testdata/fullstack-app/catalog/recipes/compose-sourced/sources/api.yaml")

	nodeID := "api"

	// The overlay values as they exist in the IR: an ordered map decoded through
	// the same OrderedMap[any] JSON decode the guest performs off the wire. Both
	// the oracle and the guest receive this exact value shape so the A/B compares
	// the render path, not a value-typing mismatch introduced by the test.
	valuesOrdered := valuesFromJSON(t, `{"services":{"api":{"environment":{"PORT":"8080","LOG_LEVEL":"info"}}}}`)
	values := orderedToPlain(&valuesOrdered)

	// The indentation width flows identically to both sides — the oracle's
	// NodeRenderContext.YAMLIndent and the wire's RenderContext.YAMLIndent — so
	// the guest applies the indent carried on the render wire rather than a
	// hardcoded width. The default case (indent 0 → both clamp to 2) covers the
	// absent-indent path; the non-default case (indent 4) re-marshals the merged
	// document visibly wider, so a byte-identical match there proves the carriage,
	// and the cross-case guard below proves the indent is observable.
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
			// ORACLE — the in-tree Go compose engine, fed real provenance and the
			// case's indent so the output serializes at that width.
			oracleCtx := renderer.NodeRenderContext{NodeID: nodeID, Source: baseCompose, YAMLIndent: tc.indent}
			stampOracleProvenance(&oracleCtx)
			oracle, err := (&compose.ComposeEngine{}).Render(ctx, values, oracleCtx)
			if err != nil {
				t.Fatalf("oracle Render: %v", err)
			}

			// CANDIDATE — the Wasm guest through the production loader. The base
			// compose file arrives in the payload's SourceProjection (projected
			// host-side at compile time), NOT via a filesystem mount.
			proj, err := compose.ProjectComposeSource(baseCompose)
			if err != nil {
				t.Fatalf("ProjectComposeSource: %v", err)
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

			candidate := make(map[string][]byte, len(out.Files))
			for _, f := range out.Files {
				candidate[f.Path] = f.Content
			}

			assertByteIdentical(t, oracle, candidate)
			captured[tc.name] = candidate
		})
	}

	// The indent must be observable, or a byte-identical match at every case
	// proves nothing about the carriage.
	if def, ok := captured["default-indent"]; ok {
		if wide, ok := captured["indent-4"]; ok && byteMapsEqual(def, wide) {
			t.Fatal("indent 4 produced byte-identical output to the default indent; the indent carriage is not observable, so the A/B is a no-op")
		}
	}
}

// TestComposeProviderSynthABByteIdentity proves the synthesized render path (a
// Recipe declaring workload:, no compose source) reproduces the in-tree compose
// engine byte-for-byte through the provider Wasm guest, end to end:
//
//   - The workload arrives in the payload (WorkloadProjection), not the
//     filesystem: the host hands the guest the evaluated Workload and the guest
//     synthesizes the compose service fragment in memory.
//   - The guest passes the production wasm loader's AOT-purity import firewall
//     (firewall ON via invokeGuest), which denies filesystem WASI imports.
//   - The guest returns the fragment's serialized bytes and the host writes them
//     verbatim: the decoded RenderNodeOutput files are compared, in emission
//     order, byte-for-byte to the in-tree engine's EmittedFiles — including the
//     fragment file Name and its single-element emission order.
//
// Compose's synthesis reads no per-slot secret provides; the WorkloadProjection's
// Secrets are ignored by design. The secrets case below still carries a populated
// Secrets map on the wire to prove the guest ignores it exactly as the in-tree
// engine does.
func TestComposeProviderSynthABByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	cases := []struct {
		name    string
		nodeID  string
		build   func() *types.Workload
		secrets map[string]types.OrderedMap[string]
	}{
		{"minimal-single-container", "api", minimalWorkload, nil},
		{"multi-container-main-and-sidecar", "api", multiContainerWorkload, nil},
		{"secrets-as-env-and-file", "store", secretsWorkload, map[string]types.OrderedMap[string]{
			"db-password": synthProvides("db-creds", "password"),
		}},
	}

	indents := []struct {
		name   string
		indent int
	}{
		{"default-indent", 0},
		{"indent-4", 4},
	}

	captured := make(map[string][]renderer.EmittedFile)
	for _, ind := range indents {
		for _, tc := range cases {
			t.Run(ind.name+"/"+tc.name, func(t *testing.T) {
				w := tc.build()

				// ORACLE — the in-tree engine, fed the natural Workload, real
				// provenance, and the case's indent.
				oracleCtx := renderer.NodeRenderContext{NodeID: tc.nodeID, YAMLIndent: ind.indent}
				stampOracleProvenance(&oracleCtx)
				oracle, err := (&compose.ComposeEngine{}).SynthesizeWorkload(ctx, nil, w, oracleCtx)
				if err != nil {
					t.Fatalf("oracle SynthesizeWorkload: %v", err)
				}

				// CANDIDATE — the Wasm guest through the production loader. The
				// workload arrives in the payload's WorkloadProjection; the node
				// projection carries the same provenance and the render context the
				// same indent, so the guest rebuilds an identical render context and
				// synthesizes in memory.
				node := renderer.IRNodeProjection{ID: tc.nodeID}
				stampCandidateProvenance(&node)
				input := renderer.RenderNodeInput{
					Node:               node,
					Context:            renderctx.RenderContext{YAMLIndent: ind.indent},
					WorkloadProjection: &renderer.WorkloadProjection{Workload: *w, Secrets: tc.secrets},
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

				assertSynthByteIdentical(t, oracle, out.Files)
				captured[ind.name+"/"+tc.name] = oracle
			})
		}
	}

	// The indent must be observable, or a byte-identical match at every case
	// proves nothing about the carriage. The multi-container case marshals
	// nested service blocks, so indent 4 must differ from the default width.
	const probe = "multi-container-main-and-sidecar"
	if def, ok := captured["default-indent/"+probe]; ok {
		if wide, ok := captured["indent-4/"+probe]; ok && emittedFilesEqual(def, wide) {
			t.Fatal("indent 4 produced byte-identical output to the default indent; the indent carriage is not observable, so the A/B is a no-op")
		}
	}
}

// abProv is the single source of the concrete provenance every A/B feeds
// identically to both sides — the oracle as NodeRenderContext fields, the
// candidate as IRNodeProjection fields — so the guest rebuilds a byte-identical
// render context. A non-zero SyncWave and every identity field set exercise the
// provenance label stamping the shared render path performs, so a byte-identical
// match proves the guest carries provenance across the ABI exactly as the
// in-tree engine stamps it.
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
	RecipeName:       "core/web-service/compose",
	RecipeVersion:    "1.4.2",
	LabelComponentID: "",
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

// minimalWorkload — single container, ClusterIP service.
func minimalWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	c.Variables.Set("LOG_LEVEL", "info")
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)
	return w
}

// multiContainerWorkload — main + sidecar; sidecar shares the main's network
// namespace via network_mode: "service:<main>". NodePort service publishes ports.
func multiContainerWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	main := types.ContainerSpec{Image: "api:2.0"}
	main.Ports.Set("http", 8080)
	w.Containers.Set("main", main)

	sidecar := types.ContainerSpec{Image: "logger:1.0"}
	w.Containers.Set("logger", sidecar)

	pubSvc := types.ServiceSpec{Type: "NodePort"}
	pubSvc.Ports.Set("http", 8080)
	w.Services.Set("public", pubSvc)
	return w
}

// secretsWorkload — one container with an env secret and a file secret plus a
// persistent volume, exercising the secret env/file translation and the volume
// declaration. Compose consumes no per-slot secret provides, so the wire's
// Secrets map is ignored regardless of its content.
func secretsWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "store:1.0"}
	c.Ports.Set("api", 9000)
	c.Secrets.Set("DB_PASSWORD", types.SecretRef{From: "db-password", As: "env"})
	c.Secrets.Set("TLS_CERT", types.SecretRef{From: "tls-cert", As: "file", Path: "/etc/tls/cert.pem"})
	c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/var/lib/store"}}
	w.Containers.Set("main", c)

	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi", Class: "gp3"}})

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("api", 9000)
	w.Services.Set("store", svc)
	return w
}

// synthProvides builds a resolved secret-slot provides bag; compose ignores it,
// but the synth A/B carries it on the wire to prove the guest drops it exactly
// as the in-tree engine does.
func synthProvides(name, key string) types.OrderedMap[string] {
	p := types.OrderedMap[string]{}
	p.Set("name", name)
	p.Set("key", key)
	return p
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

// assertSynthByteIdentical compares the oracle's EmittedFiles to the guest's
// RenderedFiles pairwise AND in order: synth emission order is deterministic and
// load-bearing, so the two file lists must line up index-for-index with equal
// name/path and equal bytes.
func assertSynthByteIdentical(t *testing.T, oracle []renderer.EmittedFile, candidate []renderer.RenderedFile) {
	t.Helper()
	if len(oracle) != len(candidate) {
		t.Fatalf("file count differs: oracle %d, candidate %d\n oracle:    %v\n candidate: %v",
			len(oracle), len(candidate), emittedNames(oracle), renderedPaths(candidate))
	}
	for i := range oracle {
		if oracle[i].Name != candidate[i].Path {
			t.Fatalf("file[%d] name/path differ: oracle %q, candidate %q", i, oracle[i].Name, candidate[i].Path)
		}
		if string(oracle[i].Body) != string(candidate[i].Content) {
			t.Errorf("BYTE MISMATCH for %q:\n--- oracle ---\n%s\n--- candidate ---\n%s", oracle[i].Name, oracle[i].Body, candidate[i].Content)
		} else {
			t.Logf("BYTE-IDENTICAL %q (%d bytes)", oracle[i].Name, len(oracle[i].Body))
		}
	}
}

// emittedFilesEqual reports whether two emitted-file lists line up
// index-for-index with equal name and byte-identical body.
func emittedFilesEqual(a, b []renderer.EmittedFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || string(a[i].Body) != string(b[i].Body) {
			return false
		}
	}
	return true
}

func emittedNames(files []renderer.EmittedFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

func renderedPaths(files []renderer.RenderedFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

// loadGuest loads the guest through the production wasm loader (AOT-purity
// import firewall ON, deterministic capability-denied ModuleConfig, synthetic
// argv from the loader). It does NOT mount any host directory: the source
// travels in the payload. If the guest imported a filesystem WASI capability,
// Loader.Load would reject it at the firewall and this call would fail with the
// offending import named — that rejection is a real failure of the proof, never
// something to route around with a mount.
func loadGuest(t *testing.T, ctx context.Context, wasmPath string, inputs wasm.FetchService) *wasm.Plugin {
	t.Helper()
	loader := &wasm.Loader{ExternalInputs: inputs}
	plugin, err := loader.Load(ctx, wasm.PluginSpec{
		Name:       "compose",
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

// buildPluginWasm compiles the plugin to GOOS=wasip1 GOARCH=wasm and returns
// the artifact path. Fails the test when the build fails.
func buildPluginWasm(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "compose-provider.wasm")
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
// the compose deep-merge consumes, recursing into nested ordered maps. It
// mirrors the guest's own flattening so the oracle receives the same value shape.
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
