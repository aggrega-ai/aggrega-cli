// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helm-provider is the helm Provider Wasm guest. It implements the
// sourced-render path of the helm engine behind the provider render ABI's
// render_node export, reusing the same filesystem-free render core the in-tree
// helm engine runs so the two produce byte-identical output for identical input.
//
// The guest decodes a RenderNodeInput, reconstructs the Helm chart in memory
// from the payload's SourceProjection (the host read the chart's files at
// compile time and packed them into the payload — the guest touches no
// filesystem), renders it against the node's values through the shared render
// core, and returns each rendered manifest's serialized bytes as a RenderedFile.
// The host writes those bytes verbatim and runs no serializer, so the on-disk
// output matches the in-tree engine's bytes.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helm-provider.wasm ./plugins/helm-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"context"
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	pdk "github.com/extism/go-pdk"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// pluginDescriptor is the static describe() self-report: a v1alpha1
// provider plugin declaring the render_node export and compatibility with
// the helm provider engine. The host reads this once at load and validates
// it before invoking render_node.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["helm"],"exports":[{"name":"render_node","input_pattern":"b","stability":"experimental","description":"renders a Helm chart source into flat Kubernetes manifests"}],"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

//go:wasmexport describe
func describe() int32 {
	pdk.Output([]byte(pluginDescriptor))
	return 0
}

//go:wasmexport render_node
func renderNode() int32 {
	var in renderer.RenderNodeInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	out, err := renderSourcedFromInput(in)
	if err != nil {
		pdk.SetError(err)
		return 1
	}

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// renderSourcedFromInput reconstructs the Helm chart in memory from the wire
// input's source projection, runs the same filesystem-free render core the
// in-tree engine runs, and returns each rendered manifest's serialized bytes as
// a RenderedFile. The chart's files arrive in the source projection (the host
// read them at compile time); the node's values, identity, and merged
// annotations arrive in the node projection and render context.
func renderSourcedFromInput(in renderer.RenderNodeInput) (renderer.RenderNodeOutput, error) {
	if in.SourceProjection == nil {
		return renderer.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no source projection")
	}

	bufs := make([]*loader.BufferedFile, 0, len(in.SourceProjection.Files))
	for _, f := range in.SourceProjection.Files {
		bufs = append(bufs, &loader.BufferedFile{Name: f.Path, Data: f.Content})
	}
	ch, err := loader.LoadFiles(bufs)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	values := orderedMapToPlain(&in.Node.Values)
	nodeCtx := renderer.NodeRenderContext{
		NodeID:      in.Node.ID,
		Annotations: mergeAnnotations(in.Context.Annotations),
		YAMLIndent:  0,
	}

	rendered, err := helm.RenderLoadedChart(context.Background(), ch, values, nodeCtx)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	out := renderer.RenderNodeOutput{Files: make([]renderer.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, renderer.RenderedFile{Path: path, Content: rendered[path]})
	}
	return out, nil
}

// orderedMapToPlain flattens the node's ordered values into the plain
// map[string]any the helm engine's coalesce step consumes. Nested ordered
// maps decode into *OrderedMap[any]; they are flattened recursively so the
// Helm template engine sees plain Go maps rather than the wire container.
// The values feed the template engine by key, so the author-order the wire
// payload preserves is not load-bearing here.
func orderedMapToPlain(m *types.OrderedMap[any]) map[string]any {
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = plainValue(v)
	}
	return out
}

// plainValue recursively converts a decoded wire value into plain Go
// containers: nested ordered maps become map[string]any and slices are
// walked element-wise. Scalars pass through unchanged.
func plainValue(v any) any {
	switch t := v.(type) {
	case *types.OrderedMap[any]:
		return orderedMapToPlain(t)
	case types.OrderedMap[any]:
		return orderedMapToPlain(&t)
	case []any:
		for i := range t {
			t[i] = plainValue(t[i])
		}
		return t
	default:
		return v
	}
}

// mergeAnnotations flattens the three render-context annotation buckets
// into the single merged map the helm engine reads (for the namespace
// annotation). Later buckets win on key collision, matching the
// aggregate-then-release-then-catalog precedence.
func mergeAnnotations(a renderctx.RenderAnnotations) map[string]string {
	out := map[string]string{}
	for _, bucket := range []*types.OrderedMap[string]{&a.Aggregate, &a.Release, &a.Catalog} {
		for _, k := range bucket.Keys() {
			if v, ok := bucket.Get(k); ok {
				out[k] = v
			}
		}
	}
	return out
}

// sortedKeys returns the map's keys in lexical order so file emission is
// deterministic regardless of Go map iteration order.
func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// insertion sort keeps the guest free of the sort package's
	// reflection surface; the file set per node is small.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func main() {}
