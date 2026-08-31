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
// verbatim into a sub-chart file set (umbrella + sub-chart composition). It
// synthesizes nothing from a Workload — workload synthesis is the helm provider's
// concern — so the guest handles a source projection alone and rejects a payload
// that carries none.
//
// The render_node guest decodes a RenderNodeInput, resolves the sub-chart from
// the payload's name-only SourceListing, asks the host to pack the chart's file
// tree through the pack-source host function (the guest touches no filesystem
// itself), and returns each file's bytes verbatim as a
// RenderedFile. The assemble_boundary guest decodes an AssembleBoundaryInput,
// reconstructs each sub-chart's pure-data view from the payload (identity,
// per-instance values, and the pristine bytes of the vendored chart's Chart.yaml
// the host read at compile time), and assembles the umbrella Chart.yaml +
// values.yaml through the same core, signalling that per-node files nest under a
// per-node sub-directory. The host writes all returned bytes verbatim and runs no
// serializer, so the guest's serialization is the on-disk output.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helmchart-provider.wasm ./plugins/helmchart-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
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
			InputKinds:             aggrega.InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: false},
			BoundaryAssembly:       "nest",
			ArtifactDelimiters:     &aggrega.ArtifactDelimiters{Left: "[[", Right: "]]"},
			AssemblyNameAnnotation: &aggrega.AssemblyNameAnnotation{Annotation: "aggrega.ai/chart-name", RequireUnique: true},
			ReusableInstance:       true,
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
	case in.ArtifactsProjection != nil:
		out, err = placeArtifactsFromInput(in)
	case in.SourceListing != nil:
		out, err = renderSourcedFromInput(in)
	default:
		// helm-chart is sourced-only for render: it copies a local chart verbatim
		// and synthesizes nothing. A payload with no source listing and no
		// artifacts projection is an orchestrator dispatch error rather than
		// authoring drift, so it halts loudly.
		pdk.SetError(errors.New("helmchart-provider: render_node input carries neither a source listing nor an artifacts projection"))
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

// renderSourcedFromInput copies the sourced sub-chart verbatim into a sub-chart
// file set via the shared render core, returning each file's bytes keyed by its
// chart-root-relative path in deterministic order.
func renderSourcedFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	proj, err := packSourcedChart(in.SourceListing)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}
	rendered := helmchartcore.RenderSourcedChartCore(proj)

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
		body, warning, err := rendercore.SpliceArtifactProvenance(ent.Name, ent.Content, ent.Labels, ent.Annotations, false, indent)
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

// assembleBoundaryFromInput reconstructs the umbrella assemble core's inputs from
// the wire AssembleBoundaryInput and runs the shared filesystem-free assembly
// core, returning the umbrella Chart.yaml + values.yaml as RenderedFiles. Each
// sub-chart carries its instance identity, its
// per-instance values (flattened from the wire's ordered map), and, for a
// sub-chart vendored from a chart source, the pristine bytes of that chart's
// Chart.yaml the host read at compile time; the core draws the dependency's name
// and version from those bytes. The boundary path is the dispatch path the core
// names the chart from when no chart-name is supplied; the render context carries
// the indentation width. helm-chart nests each contributor's per-node files under
// a per-node sub-directory, so NestPerNode is true, and it names that
// sub-directory's parent `charts` — the directory a Helm umbrella keeps its child
// charts in — so the host places each node's files where Helm looks for them.
func assembleBoundaryFromInput(in rendercore.AssembleBoundaryInput) (rendercore.AssembleBoundaryOutput, error) {
	subs := make([]helmchartcore.CoreSubChart, 0, len(in.SubCharts))
	for i := range in.SubCharts {
		sc := in.SubCharts[i]
		subs = append(subs, helmchartcore.CoreSubChart{
			InstanceID: sc.InstanceID,
			Version:    sc.Version,
			Values:     rendercore.OrderedMapToPlain(&sc.Values),
			ChartYAML:  sc.ChartYaml,
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
	out.NestPerNode = true
	out.NestSegment = "charts"
	return out, nil
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
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	units, err := helmchartcore.NewHelmChartCodec(in.YamlIndent).Segment(in.Name, in.Body)
	if err != nil {
		pdk.SetError(errors.New(errorChainMessage(err)))
		return 1
	}

	encoded, err := rendercore.CarveOutput{Units: units}.MarshalJSON()
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
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	body, _, err := helmchartcore.NewHelmChartCodec(in.YamlIndent).Reassemble(in.Name, in.Body, in.Segs, in.Patched)
	if err != nil {
		pdk.SetError(errors.New(errorChainMessage(err)))
		return 1
	}

	encoded, err := rendercore.ReassembleOutput{Body: body}.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// errorChainMessage serializes err's full wrap chain into one message. The
// host-side rich error formatter is not available across the plugin ABI, and
// some wrapper types (the compiler's categorised errors) keep their cause out of
// Error(), surfacing it only through Unwrap; appending each unwrapped cause not
// already present keeps the root diagnostic — an empty or multi-document
// Chart.yaml, an empty name or version — in the message the host receives.
func errorChainMessage(err error) string {
	msg := err.Error()
	for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
		if s := e.Error(); !strings.Contains(msg, s) {
			msg += ": " + s
		}
	}
	return msg
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
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}
	out := rendercore.NameBoundOutput{
		Names: []rendercore.DerivedNameVerdict{rendercore.ComponentIDLabelVerdict(in.Proposed)},
	}
	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

func main() {}
