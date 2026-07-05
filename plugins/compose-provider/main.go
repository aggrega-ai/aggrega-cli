// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command compose-provider is the compose Provider Wasm guest. It implements the
// sourced-render and workload-synthesis paths of the compose engine behind the
// provider render ABI's render_node export, and the per-dispatch-group merge
// behind its assemble_boundary export, reusing the same filesystem-free render
// and assemble cores the in-tree compose engine runs so the two produce
// byte-identical output for identical input.
//
// The guest decodes a RenderNodeInput and routes on which projection it carries:
// a workload projection is synthesized into a compose service fragment; a source
// projection is reconstructed in memory (the host read the base compose file at
// compile time and packed it into the payload — the guest touches no filesystem)
// and deep-merged with the node's values into a compose fragment. Compose is
// hermetic-trivially: it fetches nothing, so the guest binds no host functions.
//
// The guest returns each rendered file's serialized bytes as a RenderedFile. The
// host writes those bytes verbatim and runs no serializer, so the on-disk output
// matches the in-tree engine's bytes.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o compose-provider.wasm ./plugins/compose-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	pdk "github.com/extism/go-pdk"
)

// pluginDescriptor is the static describe() self-report: a v1alpha1
// provider plugin declaring the render_node and assemble_boundary exports and
// compatibility with the compose provider engine. The host reads this once at
// load and validates it before invoking either export. Compose fetches nothing,
// so no host functions are declared.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["compose"],"exports":[{"name":"render_node","input_pattern":"b","stability":"experimental","description":"renders a compose file source into a merged compose fragment, or synthesizes a workload into a compose service fragment"},{"name":"assemble_boundary","input_pattern":"b","stability":"experimental","description":"merges the per-node compose fragments at one dispatch group into a single docker-compose.yaml"}],"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

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

	var out renderer.RenderNodeOutput
	var err error
	switch {
	case in.WorkloadProjection != nil:
		out, err = renderSynthFromInput(in)
	case in.SourceProjection != nil:
		out, err = renderSourcedFromInput(in)
	default:
		pdk.SetError(errors.New("compose-provider: render_node input carries neither a workload projection nor a source projection"))
		return 1
	}
	if err != nil {
		pdk.SetError(errors.New(errorChainMessage(err)))
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

//go:wasmexport assemble_boundary
func assembleBoundary() int32 {
	var in renderer.AssembleBoundaryInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	out, err := assembleBoundaryFromInput(in)
	if err != nil {
		pdk.SetError(errors.New(errorChainMessage(err)))
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

// assembleBoundaryFromInput reconstructs the compose assemble core's inputs from
// the wire AssembleBoundaryInput and runs the same filesystem-free merge core the
// in-tree compose engine runs, returning the assembled docker-compose.yaml's bytes
// as a RenderedFile. Each rendered node contributes its render_node fragment files
// (keyed by node id) and its merged annotations (read for the per-node network
// discriminator); the graph's uses edges feed depends_on inference; the boundary
// path is the network-name fallback for a node with no namespace annotation; the
// render context carries the indentation width. Compose emits a flat assembled
// file, so NestPerNode is false.
func assembleBoundaryFromInput(in renderer.AssembleBoundaryInput) (renderer.AssembleBoundaryOutput, error) {
	intermediatesByNode := make(map[string][]renderer.EmittedFile, len(in.RenderedNodes))
	nodeAnnotations := make(map[string]map[string]string, len(in.RenderedNodes))
	for i := range in.RenderedNodes {
		rn := in.RenderedNodes[i]
		files := make([]renderer.EmittedFile, 0, len(rn.Rendered.Files))
		for _, f := range rn.Rendered.Files {
			files = append(files, renderer.EmittedFile{Name: f.Path, Body: f.Content})
		}
		intermediatesByNode[rn.ID] = files
		nodeAnnotations[rn.ID] = orderedStringMapToPlain(&rn.Annotations)
	}

	edges := make([]renderer.IREdgeInfo, 0, len(in.Edges))
	for _, e := range in.Edges {
		edges = append(edges, renderer.IREdgeInfo{Source: e.Source, Target: e.Target})
	}

	files, err := compose.AssembleBoundaryCore(intermediatesByNode, nodeAnnotations, edges, in.BoundaryPath, in.Context.YAMLIndent)
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, err
	}

	out := renderer.AssembleBoundaryOutput{Files: make([]renderer.RenderedFile, 0, len(files))}
	for _, path := range sortedKeys(files) {
		out.Files = append(out.Files, renderer.RenderedFile{Path: path, Content: files[path]})
	}
	out.NestPerNode = false
	return out, nil
}

// errorChainMessage serializes err's full wrap chain into one message. The
// host-side rich error formatter is not available across the plugin ABI, and
// some wrapper types (the compiler's categorised errors) keep their cause out
// of Error(), surfacing it only through Unwrap; appending each unwrapped
// cause not already present keeps the root diagnostic — a malformed compose
// document, a multi-document source — in the message the host receives.
func errorChainMessage(err error) string {
	msg := err.Error()
	for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
		if s := e.Error(); !strings.Contains(msg, s) {
			msg += ": " + s
		}
	}
	return msg
}

// renderSourcedFromInput reconstructs the base compose file in memory from the
// wire input's source projection, runs the same filesystem-free render core the
// in-tree engine runs, and returns the rendered fragment's serialized bytes as a
// RenderedFile. Compose's projection is single-file (the base compose document
// the host read at compile time); a projection with a different file count is an
// orchestrator bug rather than authoring drift and halts loudly. The node's
// values, identity, provenance, and merged annotations arrive in the node
// projection and render context.
func renderSourcedFromInput(in renderer.RenderNodeInput) (renderer.RenderNodeOutput, error) {
	if in.SourceProjection == nil {
		return renderer.RenderNodeOutput{}, errors.New("compose-provider: render_node input carries no source projection")
	}
	if len(in.SourceProjection.Files) != 1 {
		return renderer.RenderNodeOutput{}, fmt.Errorf("compose-provider: source projection carries %d files; the compose base is a single document", len(in.SourceProjection.Files))
	}
	base := in.SourceProjection.Files[0]

	values := renderer.OrderedMapToPlain(&in.Node.Values)
	nodeCtx := nodeCtxFromInput(in)

	rendered, err := compose.RenderSourcedCore(base.Content, base.Path, values, nodeCtx)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	out := renderer.RenderNodeOutput{Files: make([]renderer.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, renderer.RenderedFile{Path: path, Content: rendered[path]})
	}
	return out, nil
}

// renderSynthFromInput synthesizes a compose service fragment from the wire
// input's workload projection, running the same filesystem-free synthesis core
// the in-tree engine runs, and returns the fragment's serialized bytes as a
// RenderedFile. The workload arrives in the workload projection; the node's
// identity, provenance, and merged annotations arrive in the node projection and
// render context. Compose's synthesis reads no per-slot secret provides, so the
// projection's Secrets are ignored by design. The core's emission order is
// deterministic and load-bearing (a single Intermediate=true fragment named
// `<nodeID>.compose-fragment.yaml`), so the files are collected in order and
// never re-sorted.
func renderSynthFromInput(in renderer.RenderNodeInput) (renderer.RenderNodeOutput, error) {
	if in.WorkloadProjection == nil {
		return renderer.RenderNodeOutput{}, errors.New("compose-provider: render_node input carries no workload projection")
	}

	nodeCtx := nodeCtxFromInput(in)

	emitted, err := compose.SynthesizeWorkloadCore(&in.WorkloadProjection.Workload, nodeCtx)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	out := renderer.RenderNodeOutput{Files: make([]renderer.RenderedFile, 0, len(emitted))}
	for _, f := range emitted {
		out.Files = append(out.Files, renderer.RenderedFile{Path: f.Name, Content: f.Body})
	}
	return out, nil
}

// The guest glue below (nodeCtxFromInput, orderedStringMapToPlain,
// mergeAnnotations, sortedKeys) is byte-identical-critical: it reconstructs the
// render context the render cores stamp from off the wire. The value-flattening
// that renormalizes wire numerics is single-sourced in the renderer package
// (renderer.OrderedMapToPlain / renderer.PlainValue), which compiles for both
// the host and the wasip1 guest.

// nodeCtxFromInput builds the full render context for a node from the wire
// input: the node's identity and the provenance the render core stamps onto
// every emitted service (boundary path, deployment wave, Recipe identity,
// component-id override, and build identity) from the node projection, the
// merged annotations from the render context, and the render-wide YAML
// indentation width the guest serializes output at (carried on the render
// context; the render core clamps its zero value to the default width).
// Carrying the full provenance here is what makes the guest's fragments carry
// the same aggrega.ai labels the in-tree engine emits, so the two produce
// byte-identical output for identical input.
func nodeCtxFromInput(in renderer.RenderNodeInput) renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:           in.Node.ID,
		Annotations:      mergeAnnotations(in.Context.Annotations),
		BoundaryPath:     in.Node.BoundaryPath,
		SyncWave:         in.Node.SyncWave,
		RecipeName:       in.Node.RecipeName,
		RecipeVersion:    in.Node.RecipeVersion,
		LabelComponentID: in.Node.LabelComponentID,
		ProviderName:     in.Node.Provenance.ProviderName,
		ProviderVersion:  in.Node.Provenance.ProviderVersion,
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: in.Node.Provenance.CompilerVersion,
			AggregateName:   in.Node.Provenance.AggregateName,
			Environment:     in.Node.Provenance.Environment,
		},
		YAMLIndent: in.Context.YAMLIndent,
	}
}

// orderedStringMapToPlain flattens a node's ordered annotation map into the plain
// map[string]string the assemble core reads. The core reads annotations by key
// (the per-node network discriminator), so the author order the wire preserves is
// not load-bearing here.
func orderedStringMapToPlain(m *types.OrderedMap[string]) map[string]string {
	out := make(map[string]string, m.Len())
	for _, k := range m.Keys() {
		if v, ok := m.Get(k); ok {
			out[k] = v
		}
	}
	return out
}

// mergeAnnotations flattens the three render-context annotation buckets into the
// single merged map the engine reads. Later buckets win on key collision,
// matching the aggregate-then-release-then-catalog precedence.
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
