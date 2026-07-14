// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helmchart-provider is the helm-chart Provider Wasm guest. It implements
// the sourced-render path of the helm-chart engine behind the provider render
// ABI's render_node export, and the umbrella-assembly path behind its
// assemble_boundary export, reusing the same filesystem-free render and assemble
// cores the in-tree helm-chart engine runs so the two produce byte-identical
// output for identical input.
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
// serializer, so the on-disk output matches the in-tree engine's bytes.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helmchart-provider.wasm ./plugins/helmchart-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// pluginDescriptorHead is the fixed prefix of the describe() self-report: a
// v1alpha1 provider plugin declaring the render_node and assemble_boundary exports,
// compatibility with the helm-chart provider engine, and its plugin-level
// render-behavior contract (sourced-only input, nest-per-node umbrella assembly,
// the "[["/"]]" artifact-template delimiter pair, and the boundary annotation
// whose value names the assembled umbrella chart). The host reads this
// once at load and validates it before invoking either export. helm-chart
// resolves its chart source from the host's name-only listing and asks the host
// to pack it, so render_node declares the aggrega_pack_source host function it
// imports; the host cross-checks this declaration against the functions the
// guest actually imports. The assemble_boundary export assembles sub-charts
// already in the payload and imports no host function.
const pluginDescriptorHead = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["helm-chart"],"exports":[{"name":"render_node","input_pattern":"b","host_functions":["aggrega_pack_source"],"stability":"experimental","description":"copies a sourced Helm chart into a sub-chart file set"},{"name":"assemble_boundary","input_pattern":"b","stability":"experimental","description":"assembles the per-node sub-charts at one dispatch group into an umbrella Chart.yaml + values.yaml"},{"name":"carve","input_pattern":"b","stability":"experimental","description":"carves a chart file into addressable units for a pipeline PATCH"},{"name":"reassemble","input_pattern":"b","stability":"experimental","description":"reassembles a chart file from patched units, byte-faithfully"}],"contract":{"input_kinds":{"sourced":true,"synthesized":false,"artifacts_standalone":false},"boundary_assembly":"nest","artifact_delimiters":{"left":"[[","right":"]]"},"assembly_name_annotation":{"annotation":"aggrega.ai/chart-name","require_unique":true},"reusable_instance":true},"metadata":{"author":"aggrega","license":"Apache-2.0"`

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
	rendered := helmchart.RenderSourcedChartCore(proj)

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
			out.Diagnostics = append(out.Diagnostics, renderctx.Diagnostic{Severity: "warning", Code: "artifact_provenance_skipped", Message: warning})
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
// the wire AssembleBoundaryInput and runs the same filesystem-free assembly core
// the in-tree helm-chart engine runs, returning the umbrella Chart.yaml +
// values.yaml as RenderedFiles. Each sub-chart carries its instance identity, its
// per-instance values (flattened from the wire's ordered map), and, for a
// sub-chart vendored from a chart source, the pristine bytes of that chart's
// Chart.yaml the host read at compile time; the core draws the dependency's name
// and version from those bytes. The boundary path is the dispatch path the core
// names the chart from when no chart-name is supplied; the render context carries
// the indentation width. helm-chart nests each contributor's per-node files under
// a per-node sub-directory, so NestPerNode is true.
func assembleBoundaryFromInput(in rendercore.AssembleBoundaryInput) (rendercore.AssembleBoundaryOutput, error) {
	subs := make([]helmchart.CoreSubChart, 0, len(in.SubCharts))
	for i := range in.SubCharts {
		sc := in.SubCharts[i]
		subs = append(subs, helmchart.CoreSubChart{
			InstanceID: sc.InstanceID,
			Version:    sc.Version,
			Values:     rendercore.OrderedMapToPlain(&sc.Values),
			ChartYAML:  sc.ChartYaml,
		})
	}

	files, err := helmchart.AssembleUmbrellaCore(in.BoundaryPath, in.AggregateName, subs, in.ChartName, in.Context.YAMLIndent)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	out := rendercore.AssembleBoundaryOutput{Files: make([]rendercore.RenderedFile, 0, len(files))}
	for _, path := range sortedKeys(files) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: files[path]})
	}
	out.NestPerNode = true
	return out, nil
}

//go:wasmexport carve
func carve() int32 {
	var in rendercore.CarveInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	units, err := helmchart.NewHelmChartCodec(in.YamlIndent).Segment(in.Name, in.Body)
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

	body, _, err := helmchart.NewHelmChartCodec(in.YamlIndent).Reassemble(in.Name, in.Body, in.Segs, in.Patched)
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
	if err := helmchart.ValidateSourcedChartListing(listing); err != nil {
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

func main() {}
