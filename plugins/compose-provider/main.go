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
// The guest decodes a RenderNodeInput and routes on what it carries: a workload
// projection is synthesized into a compose service fragment; a source listing is
// resolved to the base compose document, the host is asked to pack it (the guest
// touches no filesystem itself), and it is deep-merged with the node's values into
// a compose fragment. Compose fetches no external inputs; its only host function
// is the pack-source callback its source resolution uses.
//
// The guest returns each rendered file's serialized bytes as a RenderedFile. The
// host writes those bytes verbatim and runs no serializer, so the on-disk output
// matches the in-tree engine's bytes.
//
// The guest also implements the boundary_env_overrides export: from a boundary's
// internal uses edges and the driving feature's infra it computes the
// per-consumer environment overrides that route cross-network consumer endpoints
// through the compose gateway, returning the map the host applies to each named
// consumer's injected env.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o compose-provider.wasm ./plugins/compose-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// pluginDescriptorHead is the fixed prefix of the describe() self-report: a
// v1alpha1 provider plugin declaring the render_node and assemble_boundary exports,
// compatibility with the compose provider engine, and its plugin-level
// render-behavior contract (the input kinds it renders and its boundary-merge
// assembly). The host reads this once at load and validates it before invoking
// either export. Compose resolves its base compose document from the host's
// name-only listing and asks the host to pack it, so render_node declares the
// aggrega_pack_source host function it imports; the host cross-checks this
// declaration against the functions the guest actually imports. The
// assemble_boundary export merges fragments already in the payload and imports
// no host function.
const pluginDescriptorHead = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["compose"],"exports":[{"name":"render_node","input_pattern":"b","host_functions":["aggrega_pack_source"],"stability":"experimental","description":"renders a compose file source into a merged compose fragment, or synthesizes a workload into a compose service fragment"},{"name":"assemble_boundary","input_pattern":"b","stability":"experimental","description":"merges the per-node compose fragments at one dispatch group into a single docker-compose.yaml"},{"name":"boundary_env_overrides","input_pattern":"b","stability":"experimental","description":"computes per-consumer environment overrides that route a boundary's cross-network consumer endpoints through the compose gateway"}],"contract":{"input_kinds":{"sourced":true,"synthesized":true,"artifacts_standalone":false},"boundary_assembly":"merge","reusable_instance":true},"metadata":{"author":"aggrega","license":"Apache-2.0"`

// version and commit are the build-provenance stamp injected via
// -ldflags -X at embedded-build time. A plain go build leaves them empty,
// and describe() then omits the corresponding metadata fields, so an
// unstamped build's descriptor is byte-identical to a stamped build's
// minus the provenance.
var (
	version string
	commit  string
)

// pluginDescriptor composes the describe() self-report, appending the
// optional version and commit build-provenance metadata to the fixed
// prefix when those fields are stamped, then closing the metadata object
// and the descriptor.
func pluginDescriptor() string {
	meta := ""
	if version != "" {
		meta += `,"version":` + strconv.Quote(version)
	}
	if commit != "" {
		meta += `,"commit":` + strconv.Quote(commit)
	}
	return pluginDescriptorHead + meta + `}}`
}

//go:wasmexport describe
func describe() int32 {
	pdk.Output([]byte(pluginDescriptor()))
	return 0
}

//go:wasmexport render_node
func renderNode() int32 {
	var in rendercore.RenderNodeInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	var out rendercore.RenderNodeOutput
	var err error
	switch {
	case in.WorkloadProjection != nil:
		out, err = renderSynthFromInput(in)
	case in.SourceListing != nil:
		out, err = renderSourcedFromInput(in)
	case in.ArtifactsProjection != nil:
		out, err = placeArtifactsFromInput(in)
	default:
		pdk.SetError(errors.New("compose-provider: render_node input carries neither a workload projection, a source listing, nor an artifacts projection"))
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
	var in rendercore.AssembleBoundaryInput
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

//go:wasmexport boundary_env_overrides
func boundaryEnvOverrides() int32 {
	var in rendercore.BoundaryEnvOverridesInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	overrides, err := compose.ComputeBridgeConsumerEnvOverrides(in.InternalUsesEdges, in.Infra)
	if err != nil {
		pdk.SetError(errors.New(errorChainMessage(err)))
		return 1
	}

	encoded, err := rendercore.BoundaryEnvOverridesOutput{Overrides: overrides}.MarshalJSON()
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
func assembleBoundaryFromInput(in rendercore.AssembleBoundaryInput) (rendercore.AssembleBoundaryOutput, error) {
	intermediatesByNode := make(map[string][]rendercore.EmittedFile, len(in.RenderedNodes))
	nodeAnnotations := make(map[string]map[string]string, len(in.RenderedNodes))
	for i := range in.RenderedNodes {
		rn := in.RenderedNodes[i]
		files := make([]rendercore.EmittedFile, 0, len(rn.Rendered.Files))
		for _, f := range rn.Rendered.Files {
			files = append(files, rendercore.EmittedFile{Name: f.Path, Body: f.Content})
		}
		intermediatesByNode[rn.ID] = files
		nodeAnnotations[rn.ID] = orderedStringMapToPlain(&rn.Annotations)
	}

	edges := make([]rendercore.IREdgeInfo, 0, len(in.Edges))
	for _, e := range in.Edges {
		edges = append(edges, rendercore.IREdgeInfo{Source: e.Source, Target: e.Target})
	}

	files, err := compose.AssembleBoundaryCore(intermediatesByNode, nodeAnnotations, edges, in.BoundaryPath, in.Context.YAMLIndent)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	out := rendercore.AssembleBoundaryOutput{Files: make([]rendercore.RenderedFile, 0, len(files))}
	for _, path := range sortedKeys(files) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: files[path]})
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

// renderSourcedFromInput resolves the base compose document from the wire
// input's name-only source listing, asks the host to pack it, reconstructs it in
// memory, runs the same filesystem-free render core the in-tree engine runs, and
// returns the rendered fragment's serialized bytes as a RenderedFile. Compose's
// selection is a single document; a pack that returns a different file count is
// an orchestrator bug rather than authoring drift and halts loudly. The node's
// values, identity, provenance, and merged annotations arrive in the node
// projection and render context.
func renderSourcedFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.SourceListing == nil {
		return rendercore.RenderNodeOutput{}, errors.New("compose-provider: render_node input carries no source listing")
	}
	proj, err := packComposeSource(in.SourceListing)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}
	if len(proj.Files) != 1 {
		return rendercore.RenderNodeOutput{}, fmt.Errorf("compose-provider: packed source carries %d files; the compose base is a single document", len(proj.Files))
	}
	base := proj.Files[0]

	values := rendercore.OrderedMapToPlain(&in.Node.Values)
	nodeCtx := nodeCtxFromInput(in)

	rendered, err := compose.RenderSourcedCore(base.Content, base.Path, values, nodeCtx)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: rendered[path]})
	}
	return out, nil
}

// packComposeSource resolves the base compose document from the host's name-only
// listing and asks the host to pack it. Document resolution is single-sourced in
// the compose package (shared by the guest here), so the guest only wires the
// resolved selection into a pack request.
func packComposeSource(listing *rendercore.SourceListing) (*aggrega.SourceProjection, error) {
	rel, err := compose.ResolveSourceSelection(listing)
	if err != nil {
		return nil, err
	}
	return aggrega.PackSource("file", rel)
}

// placeArtifactsFromInput places the node's pre-evaluated artifact set. compose
// emits each artifact at its author-resolved name and splices the host-computed
// build provenance into the manifest's metadata via the shared placer, using the
// default "{{"/"}}" delimiters (a manifest without metadata is a silent no-op in
// the splice). Entries are placed in author order. The Intermediate marking that
// routes these fragments to the boundary merge is applied host-side from compose's
// boundary-assembly self-report, not here.
func placeArtifactsFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.ArtifactsProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("compose-provider: render_node input carries no artifacts projection")
	}
	indent := in.Context.YAMLIndent
	var out rendercore.RenderNodeOutput
	for _, ent := range in.ArtifactsProjection.Entries {
		body, warning, err := rendercore.SpliceArtifactProvenance(ent.Name, ent.Content, ent.Labels, ent.Annotations, true, indent)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		if warning != "" {
			out.Diagnostics = append(out.Diagnostics, renderctx.Diagnostic{Severity: "warning", Code: "artifact_provenance_skipped", Message: warning})
		}
		out.Files = append(out.Files, rendercore.RenderedFile{Path: ent.Name, Content: body})
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
func renderSynthFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.WorkloadProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("compose-provider: render_node input carries no workload projection")
	}

	nodeCtx := nodeCtxFromInput(in)

	emitted, err := compose.SynthesizeWorkloadCore(&in.WorkloadProjection.Workload, nodeCtx)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(emitted))}
	for _, f := range emitted {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Name, Content: f.Body})
	}
	return out, nil
}

// The guest glue below (nodeCtxFromInput, orderedStringMapToPlain,
// mergeAnnotations, sortedKeys) is byte-identical-critical: it reconstructs the
// render context the render cores stamp from off the wire. The value-flattening
// that renormalizes wire numerics is single-sourced in the renderer package
// (rendercore.OrderedMapToPlain / rendercore.PlainValue), which compiles for both
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
func nodeCtxFromInput(in rendercore.RenderNodeInput) rendercore.NodeRenderContext {
	return rendercore.NodeRenderContext{
		NodeID:           in.Node.ID,
		Annotations:      mergeAnnotations(in.Context.Annotations),
		BoundaryPath:     in.Node.BoundaryPath,
		SyncWave:         in.Node.SyncWave,
		RecipeName:       in.Node.RecipeName,
		RecipeVersion:    in.Node.RecipeVersion,
		LabelComponentID: in.Node.LabelComponentID,
		ProviderName:     in.Node.Provenance.ProviderName,
		ProviderVersion:  in.Node.Provenance.ProviderVersion,
		Provenance: rendercore.ProvenanceContext{
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
