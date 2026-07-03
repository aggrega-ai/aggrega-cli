// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command compose-provider is the compose Provider Wasm guest. It implements the
// sourced-render and workload-synthesis paths of the compose engine behind the
// provider render ABI's render_node export, reusing the same filesystem-free
// render cores the in-tree compose engine runs so the two produce byte-identical
// output for identical input.
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
// provider plugin declaring the render_node export and compatibility with
// the compose provider engine. The host reads this once at load and validates
// it before invoking render_node. Compose fetches nothing, so no host functions
// are declared.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["compose"],"exports":[{"name":"render_node","input_pattern":"b","stability":"experimental","description":"renders a compose file source into a merged compose fragment, or synthesizes a workload into a compose service fragment"}],"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

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

	values := orderedMapToPlain(&in.Node.Values)
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

// The guest glue below (nodeCtxFromInput, orderedMapToPlain, plainValue,
// mergeAnnotations, sortedKeys) is byte-identical-critical: it reconstructs the
// render context the render cores stamp from off the wire. It duplicates the
// helm guest's equivalent helpers verbatim; each guest's own A/B proves its
// copy. A shared tag-free guest-glue helper package is a future consolidation —
// deferred for now because extracting it would force a rebuild of every guest.

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
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: in.Node.Provenance.CompilerVersion,
			AggregateName:   in.Node.Provenance.AggregateName,
			Environment:     in.Node.Provenance.Environment,
		},
		YAMLIndent: in.Context.YAMLIndent,
	}
}

// orderedMapToPlain flattens the node's ordered values into the plain
// map[string]any the compose deep-merge consumes. Nested ordered maps decode
// into *OrderedMap[any]; they are flattened recursively so the merge sees plain
// Go maps rather than the wire container. The values feed the overlay merge by
// key, so the author-order the wire payload preserves is not load-bearing here.
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
