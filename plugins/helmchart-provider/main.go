// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helmchart-provider is the helm-chart Provider Wasm guest. It implements
// the sourced-render path of the helm-chart engine behind the provider render
// ABI's render_node export, and the umbrella-assembly path behind its
// assemble_boundary export, reusing the filesystem-free render and assemble
// cores in plugins/internal/helmchartcore rather than carrying its own copy of
// them, so identical input produces byte-identical output.
//
// The helm-chart provider is sourced-only: it copies a user's local Helm chart
// into a sub-chart file set (umbrella + sub-chart composition), verbatim except
// for the chart's own name, which the umbrella owns. It synthesizes nothing from
// a Workload — workload synthesis is the helm provider's concern — so the guest
// handles a source projection alone and rejects a payload that carries none.
//
// The render_node guest decodes a RenderNodeInput, resolves the sub-chart from
// the payload's name-only SourceListing, asks the host to pack the chart's file
// tree through the pack-source host function (the guest touches no filesystem
// itself), and returns each file's bytes as a RenderedFile — every one of them
// the author's own bytes, except the chart manifest's name, which is derived
// from the node's identity so that two instances vendoring charts that declare
// one name remain addressable apart. The assemble_boundary guest decodes an AssembleBoundaryInput,
// reconstructs each sub-chart's pure-data view from the payload (identity,
// per-instance values, and the pristine bytes of the vendored chart's Chart.yaml
// the host read at compile time), and assembles the umbrella Chart.yaml +
// values.yaml through the same core, signalling that per-node files nest under a
// per-node sub-directory. A contributor whose render placed no chart manifest — a
// Feature attachment rendered through another provider — is given a minimal one
// and a template emitting each file it rendered, returned beside the umbrella's
// own files. The host writes all returned bytes verbatim and runs no
// serializer, so the guest's serialization is the on-disk output.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helmchart-provider.wasm ./plugins/helmchart-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/chartignore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmchartcore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// descriptor is the describe() self-report: a v1alpha1 provider plugin declaring
// the render_node, assemble_boundary, carve, and reassemble exports,
// compatibility with the helm-chart provider engine, and its plugin-level
// render-behavior contract (sourced-only input, nest-per-node umbrella assembly,
// the "[["/"]]" artifact-template delimiter pair, and the boundary annotation
// whose value names the assembled umbrella chart). The host reads it once at
// load and validates it before invoking any export. helm-chart resolves its
// chart source from the host's name-only listing and asks the host to pack it,
// so render_node declares the aggrega_pack_source host function it imports; the
// host cross-checks this declaration against the functions the guest actually
// imports. The assemble_boundary, carve, and reassemble exports work from
// payloads and import no host function.
//
// aggrega.MarshalDescriptor folds the build-provenance version and commit into
// the metadata when the Go linker stamps them at build time; a plain build
// leaves them unset, so an unstamped build's descriptor is byte-identical to a
// stamped build's minus the provenance.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "provider",
		Capabilities: []string{"helm-chart"},
		Exports: []aggrega.PluginExport{
			{
				Name:          "render_node",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "copies a sourced Helm chart into a sub-chart file set",
			},
			{
				Name:         "assemble_boundary",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "assembles the per-node sub-charts at one dispatch group into an umbrella Chart.yaml + values.yaml",
			},
			{
				Name:         "name_bound",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "reports whether a proposed component identity yields a component-id label value this target accepts, and how short it would have to be",
			},
			{
				Name:         "carve",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "carves a chart file into addressable units for a pipeline PATCH",
			},
			{
				Name:         "reassemble",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "reassembles a chart file from patched units, byte-faithfully",
			},
		},
		Contract: &aggrega.ProviderContract{
			InputKinds:       aggrega.InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: false},
			BoundaryAssembly: "nest",
			// Each contributor is a sub-chart, and `charts` is what Helm calls the
			// directory it discovers those in — this target's word, declared here so
			// the host composes the layout without holding it.
			NestSegment:            "charts",
			ArtifactDelimiters:     &aggrega.ArtifactDelimiters{Left: "[[", Right: "]]"},
			AssemblyNameAnnotation: &aggrega.AssemblyNameAnnotation{Annotation: "aggrega.ai/chart-name", RequireUnique: true},
			ReusableInstance:       true,
			// What the chart ecosystem never ships beside a chart, the chart's own
			// .helmignore among it — the same declaration the chart-rendering
			// guest makes, so a chart travels alike whichever of the two serves it.
			SourceIgnore: &aggrega.SourceIgnore{Patterns: chartignore.Patterns(), Files: chartignore.Files()},
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
// report on. carve and reassemble keep it for every failure, because their output
// shapes declare no failure channel at all.

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
// how a failure crosses and this stays about what helm-chart renders.
func renderNodeOutput() (rendercore.RenderNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.RenderNodeOutput{}, err
	}
	switch {
	case in.ArtifactsProjection != nil:
		return placeArtifactsFromInput(in)
	case in.SourceListing != nil:
		return renderSourcedFromInput(in)
	}
	// helm-chart is sourced-only for render: it copies a local chart verbatim
	// and synthesizes nothing. A payload with no source listing and no
	// artifacts projection is an orchestrator dispatch error rather than
	// authoring drift, so it halts loudly.
	return rendercore.RenderNodeOutput{}, errors.New(
		"helmchart-provider: render_node input carries neither a source listing nor an artifacts projection")
}

// renderSourcedFromInput copies the sourced sub-chart into a sub-chart file set
// via the shared render core, returning each file's bytes keyed by its
// chart-root-relative path in deterministic order. Every file's bytes are the
// author's own except the chart's manifest name, which the render core derives
// from this node's identity so that two instances vendoring charts that declare
// one name stay addressable apart.
//
// The identity is the CONTRIBUTOR's own — which chart this is, rather than which
// name its content renders against — because the manifest name is how a chart is
// addressed apart from every other chart placed beside it.
//
// Three carries answer that in turn. The component identity the host composed is
// read first: it is the value that names a contributor whose chart arrives
// through another component, so a feature attached to a component declares its
// own identity rather than repeating its host's and leaving the two
// indistinguishable. Failing that, the basis the node carries for names a guest
// derives from source an author wrote, which a contributor reaching a render
// under a dispatch identity the compiler built carries explicitly because that
// identity opens with a character a chart name refuses. Failing both, the node
// id, which is the value for an ordinary component and the same identity the
// umbrella's own assembly derives that sub-chart's alias from.
//
// Only the name a manifest DECLARES is chosen this way. What the author's own
// templates render against is a separate question with a separate answer, and it
// is deliberately not read here: a chart an attachment's author supplied names
// its resources after the release name, so deriving that from the contributor
// identity instead would rename resources already deployed.
func renderSourcedFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	proj, err := packSourcedChart(in.SourceListing)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}
	identity := in.Node.LabelComponentID
	if identity == "" {
		identity = in.Node.DerivedNameBasis
	}
	if identity == "" {
		identity = in.Node.ID
	}
	rendered, err := helmchartcore.RenderSourcedChartCore(proj, identity, in.Context.YAMLIndent)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: rendered[path]})
	}
	return out, nil
}

// placeArtifactsFromInput places the node's pre-evaluated artifact set into the
// chart, splitting by the entry's authored form. An imported entry
// ({$import: <path>}) is a Kubernetes manifest: it lands at templates/<name>
// alongside the sourced chart's own templates and participates in Helm's
// downstream render pass, with the host-computed build provenance spliced into
// its metadata under the non-default delimiter policy — a file whose surviving
// Helm "{{ ... }}" syntax does not round-trip is emitted unchanged with a skip
// diagnostic rather than failing the build. An inline entry is a loose chart
// file placed verbatim at the chart root under its authored key, so the chart's
// own templates can read it via ".Files.Get"/".Files.Glob" (the canonical
// ConfigMap-from-files pattern); it is not a rendered manifest, so it takes no
// templates/ prefix and no provenance splice, which would corrupt a
// non-manifest body. A file whose chart-root path collides with a sourced chart
// file or another artifact is caught downstream by the cross-origin collision
// detector. Entries are placed in author order.
func placeArtifactsFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	indent := in.Context.YAMLIndent
	var out rendercore.RenderNodeOutput
	for _, ent := range in.ArtifactsProjection.Entries {
		if ent.Origin == "inline" {
			out.Files = append(out.Files, rendercore.RenderedFile{Path: ent.Name, Content: ent.Content})
			continue
		}
		body, warning, err := rendercore.SpliceArtifactProvenance(ent.Name, ent.Content, ent.Labels, ent.Annotations, false, indent, ent.SensitiveContent)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		if warning != "" {
			out.Diagnostics = append(out.Diagnostics, rendercore.ArtifactProvenanceSkippedDiagnostic(warning))
		}
		out.Files = append(out.Files, rendercore.RenderedFile{Path: "templates/" + ent.Name, Content: body})
	}
	return out, nil
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

// assembleBoundaryOutput decodes the assemble_boundary input and runs the
// assembly, returning the error rather than reporting it for the reason
// renderNodeOutput does.
func assembleBoundaryOutput() (rendercore.AssembleBoundaryOutput, error) {
	var in rendercore.AssembleBoundaryInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	return assembleBoundaryFromInput(in)
}

// assembleBoundaryFromInput reconstructs the umbrella assemble core's inputs from
// the wire AssembleBoundaryInput and runs the shared filesystem-free assembly
// core, returning the umbrella Chart.yaml + values.yaml as RenderedFiles, beside
// the chart files the core writes for a contributor whose render placed no
// manifest. Each sub-chart carries its instance identity, its
// per-instance values (flattened from the wire's ordered map), and, for a
// sub-chart vendored from a chart source, the pristine bytes of that chart's
// Chart.yaml the host read at compile time; the core draws the dependency's name
// and version from those bytes. The boundary path is the dispatch path the core
// names the chart from when no chart-name is supplied; the render context carries
// the indentation width. Where a contributor's files sit is declared on this
// guest's describe() contract rather than returned here, so the host holds the
// sub-directory's parent `charts` — the directory a Helm umbrella keeps its child
// charts in — so the host places each node's files where Helm looks for them.
func assembleBoundaryFromInput(in rendercore.AssembleBoundaryInput) (rendercore.AssembleBoundaryOutput, error) {
	// Each contributor arrives on two carriers: the sub-chart projection, which
	// holds the pristine bytes of the chart its author shipped, and the rendered
	// node, which holds where the placement wrote that chart and what the placed
	// manifest declares. The dependency entry needs one value from each, so they
	// are correlated by the key both carry rather than either being derived from
	// the other.
	placedByNode := make(map[string]rendercore.RenderedNode, len(in.RenderedNodes))
	for i := range in.RenderedNodes {
		placedByNode[in.RenderedNodes[i].ID] = in.RenderedNodes[i]
	}

	subs := make([]helmchartcore.CoreSubChart, 0, len(in.SubCharts))
	for i := range in.SubCharts {
		sc := in.SubCharts[i]
		node := placedByNode[sc.InstanceID]
		subs = append(subs, helmchartcore.CoreSubChart{
			InstanceID: sc.InstanceID,
			Version:    sc.Version,
			Values:     rendercore.OrderedMapToPlain(&sc.Values),
			ChartYAML:  sc.ChartYaml,
			// The manifest as PLACED, which is where the name the dependency binds
			// to lives: the render rewrote it to the identity this contributor is
			// addressed by, and the pristine bytes above still carry the name its
			// author wrote.
			PlacedChartYAML: placedChartManifest(node),
			OutputDirectory: node.OutputDirectory,
			// What a contributor that placed no manifest is given a chart from:
			// the identity it is addressed by, and the files its render placed.
			NameBasis:     node.DerivedNameBasis,
			RenderedFiles: renderedFilePaths(node),
		})
	}

	files, err := helmchartcore.AssembleUmbrellaCore(in.BoundaryPath, in.AggregateName, subs, in.ChartName, in.Context.YAMLIndent)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	out := rendercore.AssembleBoundaryOutput{Files: make([]rendercore.RenderedFile, 0, len(files))}
	for _, path := range sortedKeys(files) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: files[path], Format: assembledFileFormat(path)})
	}
	return out, nil
}

// placedChartManifest returns the bytes of the chart manifest a contributor's
// render actually placed, or nothing where that contributor placed none.
//
// It reads the rendered file set rather than the pristine source projection,
// because the two carry different documents: the render rewrote the manifest's
// name to the identity the placed chart is addressed by, and the dependency has
// to bind to that name rather than to the one the chart's author wrote. A
// contributor that placed no manifest rendered through another provider, and the
// assembly writes a chart for it, named for the identity it is addressed by.
func placedChartManifest(node rendercore.RenderedNode) []byte {
	for i := range node.Rendered.Files {
		if node.Rendered.Files[i].Path == helmchartcore.ChartMetadataFileName {
			return node.Rendered.Files[i].Content
		}
	}
	return nil
}

// renderedFilePaths returns the path of each file a contributor's render placed,
// in the order the render emitted them, which is the order a chart written for
// that contributor emits them in.
func renderedFilePaths(node rendercore.RenderedNode) []string {
	paths := make([]string, 0, len(node.Rendered.Files))
	for i := range node.Rendered.Files {
		paths = append(paths, node.Rendered.Files[i].Path)
	}
	return paths
}

// assembledFileFormat reports the honest output syntax of an assembled umbrella
// file so the host can route it to a matching parser. The umbrella Chart.yaml and
// values.yaml are freshly marshaled structured YAML. Any other emitted path is
// left unformatted so the host falls back to extension-based selection: a
// Go-templated chart file is not valid YAML before render, so the assembler
// declares a format only for the files it knows are structured.
func assembledFileFormat(path string) string {
	switch path {
	case "Chart.yaml", "values.yaml":
		return "yaml"
	default:
		return ""
	}
}

//go:wasmexport carve
func carve() int32 {
	var in rendercore.CarveInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	units, err := helmchartcore.NewHelmChartCodec(in.YamlIndent).Segment(in.Name, in.Body)
	if err != nil {
		pdk.SetError(errors.New(rendercore.FailureOf(err).Message))
		return 1
	}

	encoded, err := rendercore.CarveOutput{Units: units}.EncodeCBOR()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

//go:wasmexport reassemble
func reassemble() int32 {
	var in rendercore.ReassembleInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	body, _, err := helmchartcore.NewHelmChartCodec(in.YamlIndent).Reassemble(in.Name, in.Body, in.Segs, in.Patched)
	if err != nil {
		pdk.SetError(errors.New(rendercore.FailureOf(err).Message))
		return 1
	}

	encoded, err := rendercore.ReassembleOutput{Body: body}.EncodeCBOR()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
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

// packSourcedChart validates the sourced chart against the host's name-only
// listing, asks the host to pack its file tree, and returns the packed projection
// the render core rebuilds the sub-chart's file map from. Listing validation is
// single-sourced in the helmchart package (shared by the guest here); the whole
// tree is then vendored recursively.
func packSourcedChart(listing *rendercore.SourceListing) (*rendercore.SourceProjection, error) {
	if err := helmchartcore.ValidateSourcedChartListing(listing); err != nil {
		return nil, err
	}
	packed, err := aggrega.PackSource("tree", "")
	if err != nil {
		return nil, err
	}
	proj := &rendercore.SourceProjection{Files: make([]rendercore.SourceFile, len(packed.Files))}
	for i, f := range packed.Files {
		proj.Files[i] = rendercore.SourceFile{Path: f.Path, Content: f.Content}
	}
	return proj, nil
}

// nameBound answers whether a proposed component identity yields names this
// target accepts.
//
// One kind is reported, and the absence of a second is the point. This provider
// copies a chart's own resources through rather than deriving a release name of
// its own, so the only name it derives from a component identity is the
// component-id label value it stamps on every resource it emits. It is judged
// where that label is written rather than restated here, so the rule and the
// write stay one thing.
//
// It answers at all — rather than declining the export and being asked nothing —
// because declining would leave that label's own bound with no holder: the
// compiler holds no target's limits, so a target that stamps a bounded name and
// says nothing about it gets a name nobody bounded.
//
//go:wasmexport name_bound
func nameBound() int32 {
	var in rendercore.NameBoundInput
	var out rendercore.NameBoundOutput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		out = rendercore.NameBoundFailure(err)
	} else {
		out = rendercore.NameBoundOutput{
			Names: []rendercore.DerivedNameVerdict{rendercore.ComponentIDLabelVerdict(in.Proposed, in.Authored)},
		}
	}
	encoded, mErr := out.EncodeCBOR()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

func main() {}
