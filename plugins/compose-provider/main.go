// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command compose-provider is the compose Provider Wasm guest. It implements the
// sourced-render and workload-synthesis paths of the compose engine behind the
// provider render ABI's render_node export, and the per-dispatch-group merge
// behind its assemble_boundary export, reusing the filesystem-free render and
// assemble cores in plugins/internal/composecore rather than carrying its own
// copy of them, so identical input produces byte-identical output.
//
// The guest decodes a RenderNodeInput and routes on what it carries: a workload
// projection is synthesized into a compose service fragment; a source listing is
// resolved to the base compose document, the host is asked to pack it (the guest
// touches no filesystem itself), and it is deep-merged with the node's values into
// a compose fragment. Compose fetches no external inputs; its only host function
// is the pack-source callback its source resolution uses.
//
// The guest returns each rendered file's serialized bytes as a RenderedFile. The
// host writes those bytes verbatim and runs no serializer, so the guest's
// serialization is the on-disk output.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o compose-provider.wasm ./plugins/compose-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/composecore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// descriptor is the describe() self-report: a v1alpha1 provider plugin declaring
// the render_node and assemble_boundary exports,
// compatibility with the compose provider engine, and its plugin-level
// render-behavior contract (the input kinds it renders, its boundary-merge
// assembly and its honouring of omit entries). The host reads it once at load
// and validates it before invoking any export. Compose resolves its base
// compose document from the host's name-only
// listing and asks the host to pack it, so render_node declares the
// aggrega_pack_source host function it imports; the host cross-checks this
// declaration against the functions the guest actually imports. The
// assemble_boundary export works from payloads and imports no host function.
//
// aggrega.MarshalDescriptor folds the build-provenance version and commit into
// the metadata when the Go linker stamps them at build time; a plain build
// leaves them unset, so an unstamped build's descriptor is byte-identical to a
// stamped build's minus the provenance.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "provider",
		Capabilities: []string{"compose"},
		Exports: []aggrega.PluginExport{
			{
				Name:          "render_node",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "renders a compose file source into a merged compose fragment, or synthesizes a workload into a compose service fragment",
			},
			{
				Name:         "assemble_boundary",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "merges the per-node compose fragments at one dispatch group into a single docker-compose.yaml",
			},
		},
		Contract: &aggrega.ProviderContract{
			InputKinds:       aggrega.InputKinds{Sourced: true, Synthesized: true, ArtifactsStandalone: true},
			BoundaryAssembly: "merge",
			ReusableInstance: true,
			// A sourced render leaves out every service, volume and config an omit
			// entry names by its key, reports how many units each entry left out,
			// and refuses a file in which what remains still names one of them.
			Omits: true,
		},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

//go:wasmexport describe
func describe() int32 {
	out, err := aggrega.MarshalDescriptor(descriptor())
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(out)
	return 0
}

// The failure path of an export whose output shape declares the in-band failure
// channel returns 0 and reports the failure on that output, because the identity
// of the condition crosses only on that path: the out-of-band channel carries one
// string, so a guest that reports there has discarded the condition's code before
// the host can read it. rendercore.FailureOf derives what crosses; the host reads
// a present failure message off the output as a failed invocation.
//
// The out-of-band channel remains for the one failure the in-band channel cannot
// carry: an output the guest could not serialize, where there is no envelope to
// report on.

//go:wasmexport render_node
func renderNode() int32 {
	out, err := renderNodeOutput()
	if err != nil {
		out = rendercore.RenderNodeFailure(err)
	}

	encoded, mErr := out.EncodeCBOR()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// renderNodeOutput decodes the render_node input and routes on what it carries.
// It returns the error rather than reporting it, so the one export above decides
// how a failure crosses and this stays about what compose renders.
func renderNodeOutput() (rendercore.RenderNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.RenderNodeOutput{}, err
	}
	switch {
	case in.WorkloadProjection != nil:
		return renderSynthFromInput(in)
	case in.SourceListing != nil:
		return renderSourcedFromInput(in)
	case in.ArtifactsProjection != nil:
		return placeArtifactsFromInput(in)
	}
	return rendercore.RenderNodeOutput{}, errors.New(
		"compose-provider: render_node input carries neither a workload projection, a source listing, nor an artifacts projection")
}

//go:wasmexport assemble_boundary
func assembleBoundary() int32 {
	out, err := assembleBoundaryOutput()
	if err != nil {
		out = rendercore.AssembleBoundaryFailure(err)
	}

	encoded, mErr := out.EncodeCBOR()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// assembleBoundaryOutput decodes the assemble_boundary input and runs the merge,
// returning the error rather than reporting it for the reason renderNodeOutput
// does.
func assembleBoundaryOutput() (rendercore.AssembleBoundaryOutput, error) {
	var in rendercore.AssembleBoundaryInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	return assembleBoundaryFromInput(in)
}

// assembleBoundaryFromInput reconstructs the compose assemble core's inputs from
// the wire AssembleBoundaryInput and runs the filesystem-free merge core,
// returning the assembled docker-compose.yaml's bytes as a RenderedFile. Each
// rendered node contributes its render_node fragment files (keyed by node id),
// its merged annotations (read for the per-node network discriminator) and its
// deployment wave (read for wave-derived ordering between the contributors); the
// graph's uses edges feed depends_on inference; the boundary path is the
// network-name fallback for a node with no namespace annotation; the render
// context carries the indentation width. Compose folds its contributors into one
// document, so no contributor has a position of its own — which the guest states
// on its describe() contract rather than on this reply.
//
// The merge's advisories travel back on the output's diagnostics, which the host
// routes to the build's advisory stream. They never affect the emitted bytes.
func assembleBoundaryFromInput(in rendercore.AssembleBoundaryInput) (rendercore.AssembleBoundaryOutput, error) {
	intermediatesByNode := make(map[string][]rendercore.EmittedFile, len(in.RenderedNodes))
	nodeAnnotations := make(map[string]map[string]string, len(in.RenderedNodes))
	nodeSyncWave := make(map[string]int, len(in.RenderedNodes))
	for i := range in.RenderedNodes {
		rn := in.RenderedNodes[i]
		files := make([]rendercore.EmittedFile, 0, len(rn.Rendered.Files))
		for _, f := range rn.Rendered.Files {
			files = append(files, rendercore.EmittedFile{Name: f.Path, Body: f.Content})
		}
		intermediatesByNode[rn.ID] = files
		nodeAnnotations[rn.ID] = orderedStringMapToPlain(&rn.Annotations)
		nodeSyncWave[rn.ID] = rn.SyncWave
	}

	edges := make([]rendercore.IREdgeInfo, 0, len(in.Edges))
	for _, e := range in.Edges {
		edges = append(edges, rendercore.IREdgeInfo{Source: e.Source, Target: e.Target})
	}

	files, diags, err := composecore.AssembleBoundaryCore(intermediatesByNode, nodeAnnotations, nodeSyncWave, edges, in.BoundaryPath, in.Context.YAMLIndent)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	out := rendercore.AssembleBoundaryOutput{
		Files:       make([]rendercore.RenderedFile, 0, len(files)),
		Diagnostics: diags,
	}
	for _, path := range sortedKeys(files) {
		// The compose boundary merges to a single structured docker-compose
		// document, so every assembled file is YAML. Declaring the format lets
		// the host route the file to the YAML parser without inferring from the
		// extension.
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: files[path], Format: "yaml"})
	}
	return out, nil
}

// renderSourcedFromInput resolves the base compose document from the wire
// input's name-only source listing, asks the host to pack it, reconstructs it in
// memory, runs the shared filesystem-free render core, and returns the rendered
// fragment's serialized bytes as a RenderedFile. Compose's selection is a single
// document; a pack that returns a different file count is
// an orchestrator bug rather than authoring drift and halts loudly. The node's
// values, identity, provenance, release annotations and omit entries arrive in the
// node projection, the render context and the input's omits list, and the counts
// the render core returns for the entries go back on the output — one per entry
// the input carried, and none when it carried none.
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

	rendered, err := composecore.RenderSourcedCore(base.Content, base.Path, values, nodeCtx)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{
		Files:   make([]rendercore.RenderedFile, 0, len(rendered.Files)),
		Omitted: rendered.Omitted,
	}
	for _, path := range sortedKeys(rendered.Files) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: rendered.Files[path]})
	}
	return out, nil
}

// packComposeSource resolves the base compose document from the host's name-only
// listing and asks the host to pack it. Document resolution is single-sourced in
// the compose package (shared by the guest here), so the guest only wires the
// resolved selection into a pack request.
func packComposeSource(listing *rendercore.SourceListing) (*aggrega.SourceProjection, error) {
	rel, err := composecore.ResolveSourceSelection(listing)
	if err != nil {
		return nil, err
	}
	return aggrega.PackSource("file", rel)
}

// placeArtifactsFromInput places the node's pre-evaluated artifact set. A compose
// artifact is a compose fragment, whose provenance site is each service's own
// `labels:` block rather than a document-level metadata block, so the entries go
// through the compose placer in the compose render core: it folds the two
// host-computed provenance halves into the single service-labels set compose
// emits and stamps every service the fragment declares. A fragment that declares
// none is emitted unchanged and reports it, which becomes the
// artifact-provenance-skipped diagnostic below — so an unstamped fragment is
// always named, never passed through in silence.
//
// Entries are placed at their author-resolved names, in author order. The
// Intermediate marking that routes these fragments to the boundary merge is
// applied host-side from compose's boundary-assembly self-report, not here.
func placeArtifactsFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.ArtifactsProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("compose-provider: render_node input carries no artifacts projection")
	}
	indent := in.Context.YAMLIndent
	var out rendercore.RenderNodeOutput
	for _, ent := range in.ArtifactsProjection.Entries {
		body, warning, err := composecore.SpliceArtifactServiceProvenance(ent.Name, ent.Content, ent.Labels, ent.Annotations, indent, ent.SensitiveContent)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		if warning != "" {
			out.Diagnostics = append(out.Diagnostics, rendercore.ArtifactProvenanceSkippedDiagnostic(warning))
		}
		out.Files = append(out.Files, rendercore.RenderedFile{Path: ent.Name, Content: body})
	}
	return out, nil
}

// renderSynthFromInput synthesizes a compose service fragment from the wire
// input's workload projection, running the shared filesystem-free synthesis
// core, and returns the fragment's serialized bytes as a RenderedFile. The
// workload arrives in the workload projection; the node's
// identity, provenance, and release annotations arrive in the node projection and
// render context. Compose's synthesis reads no attachment provides, so the
// projection's AttachmentProvides are ignored by design. The core's emission
// order is deterministic and load-bearing (a single Intermediate=true fragment
// named `<nodeID>.compose-fragment.yaml`), so the files are collected in order
// and never re-sorted.
func renderSynthFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.WorkloadProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("compose-provider: render_node input carries no workload projection")
	}

	nodeCtx := nodeCtxFromInput(in)

	emitted, diags, err := composecore.SynthesizeWorkloadCore(&in.WorkloadProjection.Workload, nodeCtx)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(emitted))}
	for _, f := range emitted {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Name, Content: f.Body})
	}
	// The synth core's unsupported-field warnings ride back out-of-band; the host
	// folds RenderNodeOutput.Diagnostics into the build/lint warning stream and
	// they never affect the rendered bytes.
	out.Diagnostics = append(out.Diagnostics, diags...)
	return out, nil
}

// The guest glue below (nodeCtxFromInput, orderedStringMapToPlain, sortedKeys) is
// byte-identical-critical: it reconstructs the
// render context the render cores stamp from off the wire. The value-flattening
// that renormalizes wire numerics is single-sourced in the renderer package
// (rendercore.OrderedMapToPlain / rendercore.PlainValue), which compiles for both
// the host and the wasip1 guest.

// nodeCtxFromInput builds the full render context for a node from the wire
// input: the node's identity and the provenance the render core stamps onto
// every emitted service (boundary path, deployment wave, Recipe identity,
// component-id override, derived-name basis, and build identity) from the node
// projection, the render context's release annotations, which the well-known
// aggrega.ai/* keys are read from (the other two buckets never decide one; see
// rendercore.WellKnownAnnotationSet), and the render-wide YAML
// indentation width the guest serializes output at (carried on the render
// context; the render core clamps its zero value to the default width).
// Carrying the full provenance here is what makes the guest's fragments carry
// the whole aggrega.ai label set, so identical input produces byte-identical
// output. The markings over the values and over the workload as a whole are
// rebuilt too: the render core reads them to keep a value the input marks out of
// a failure's text, and they change no output byte.
// The omit entries the input carries are rebuilt as well, so a sourced render
// leaves out what they name; only a source render's input carries any. A marked
// entry is rebuilt with every field withheld, and the render core quotes no
// withheld field in a refusal, so no field of a marked entry reaches a text this
// guest returns.
func nodeCtxFromInput(in rendercore.RenderNodeInput) rendercore.NodeRenderContext {
	return rendercore.NodeRenderContext{
		NodeID:            in.Node.ID,
		Omits:             rendercore.ResolvedOmitsOf(in.Omits),
		SensitiveValues:   in.Node.SensitiveValues,
		SensitiveWorkload: in.WorkloadProjection != nil && in.WorkloadProjection.SensitiveWorkload,
		Annotations:       rendercore.WellKnownAnnotationSet(in.Context.Annotations),
		BoundaryPath:      in.Node.BoundaryPath,
		SyncWave:          in.Node.SyncWave,
		RecipeName:        in.Node.RecipeName,
		RecipeVersion:     in.Node.RecipeVersion,
		LabelComponentID:  in.Node.LabelComponentID,
		DerivedNameBasis:  in.Node.DerivedNameBasis,
		OutputLabels:      in.Node.OutputLabels,
		ProviderName:      in.Node.Provenance.ProviderName,
		ProviderVersion:   in.Node.Provenance.ProviderVersion,
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
