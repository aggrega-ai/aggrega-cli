// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helm-provider is the helm Provider Wasm guest. It implements the
// sourced-render path of the helm engine behind the provider render ABI's
// render_node export, driving the filesystem-free render core in
// plugins/internal/helmcore rather than carrying its own copy of it, so
// identical input produces byte-identical output.
//
// The guest decodes a RenderNodeInput, resolves the chart root from the payload's
// name-only SourceListing, asks the host to pack the chart's file tree through the
// pack-source host function (the guest touches no filesystem itself),
// reconstructs the Helm chart in memory, renders it against the node's values
// through the shared render core, and returns each rendered manifest's serialized
// bytes as a RenderedFile.
// The host writes those bytes verbatim and runs no serializer, so the guest's
// serialization is the on-disk output.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helm-provider.wasm ./plugins/helm-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/chartignore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// descriptor is the describe() self-report: a v1alpha1 provider plugin declaring
// the render_node export, compatibility with the helm provider engine, and its
// render-behavior contract (the input kinds it renders and how it treats a
// dispatch group's output). render_node declares two host functions it imports:
// aggrega_pack_source, which packs the chart source the guest resolves from the
// host's name-only listing, and aggrega_fetch_external_input, which resolves a
// chart's remote values-schema references through the host's pinned
// external-input channel. The host cross-checks these declarations against the
// functions the guest actually imports. The host reads it once at load and
// validates it before invoking render_node.
//
// aggrega.MarshalDescriptor folds the build-provenance version and commit into
// the metadata when the Go linker stamps them at build time; a plain build
// leaves them unset, so an unstamped build's descriptor is byte-identical to a
// stamped build's minus the provenance.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "provider",
		Capabilities: []string{"helm"},
		Exports: []aggrega.PluginExport{
			{
				Name:          "render_node",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_fetch_external_input", "aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "renders a Helm chart source into flat Kubernetes manifests, or synthesizes a workload into flat Kubernetes manifests",
			},
			{
				Name:         "name_bound",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "reports whether a proposed component identity yields a release name and a component-id label value this target accepts, and how short it would have to be",
			},
			{
				Name:         "rendered_name",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "reports the release name a chart's own templates render against, so a Recipe can publish an address naming what that chart emits",
			},
		},
		Contract: &aggrega.ProviderContract{
			InputKinds:       aggrega.InputKinds{Sourced: true, Synthesized: true, ArtifactsStandalone: true},
			BoundaryAssembly: "none",
			ReusableInstance: true,
			// What the chart ecosystem never ships beside a chart, the chart's own
			// .helmignore among it. The host records these rules when an entity is
			// pinned and applies them to every publication and render of its chart,
			// so a render and a publication of one chart read the same tree.
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
// report on.

//go:wasmexport render_node
func renderNode() int32 {
	out, err := renderNodeOutput()
	if err != nil {
		out = rendercore.RenderNodeFailure(err)
	}

	encoded, mErr := out.MarshalJSON()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// renderNodeOutput decodes the render_node input, renders it, and declares the
// names the rendered output addresses its resources by. It returns the error
// rather than reporting it, so the one export above decides how a failure crosses
// and this stays about what helm renders.
//
// The declaration is taken here, once, over the emitted file set rather than
// inside each render path that has one. A resource's name is read from the emitted
// document the same way whichever path produced it, so one reader serves them all
// and a path added later is covered without anyone remembering to declare in it.
// Declaring per path would be several copies of one reader, agreeing on the day
// they were written, and a path whose copy was forgotten would emit a name nothing
// compares — invisible from every green build.
//
// An ARTIFACTS placement declares alongside every other path, and excluding it
// would leave a real cluster overwrite unreported. What has to be compared is
// the name an emitted document CARRIES rather than
// where that name came from. An artifact's body is emitted as authored, with only
// build provenance spliced into its metadata, so the names in it are the ones its
// author wrote rather than anything this guest derived — but two resources reaching
// one name in one namespace is a cluster overwrite whichever surface the name came
// from, and the emitted document is the same shape either way. Excluding the path
// because its names are authored would leave exactly that overwrite unreported
// while the identical collision through a chart template halts, which is a
// difference in what the reader happened to look at rather than in what the output
// does.
//
// The remedy an author needs is on the authoring surface and is what makes the
// refusal answerable: an `artifacts:` body is a template, and it receives the host
// component and the attachment key, so a name that has to differ per host is
// composed from them. The declared name stays byte-identical to the string the
// author wrote, so a refusal naming it doubles as the term to search their own
// bodies for.
func renderNodeOutput() (rendercore.RenderNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		return rendercore.RenderNodeOutput{}, err
	}
	out, err := renderNodeFor(in)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}
	// The names this node's emitted output addresses its resources by, declared per
	// key space so the host can confirm no two contributors assembling into one
	// output claim the same one. A resource name is unique within its kind and
	// namespace, and the host writes the file bytes verbatim without reading them,
	// so the declaration is how the guest asks for a check the host could not
	// otherwise make.
	if declared := helmcore.DeclaredResourceNames(
		out.Files, helmcore.EffectiveNamespace(nodeCtxFromInput(in)),
	); !declared.IsZero() {
		out.DerivedKeys = declared
	}
	return out, nil
}

// renderNodeFor routes one decoded input to the render path its payload names.
func renderNodeFor(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	switch {
	case in.WorkloadProjection != nil:
		return renderSynthFromInput(in)
	case in.SourceListing != nil:
		return renderSourcedFromInput(in)
	case in.ArtifactsProjection != nil:
		return placeArtifactsFromInput(in)
	}
	return rendercore.RenderNodeOutput{}, errors.New(
		"helm-provider: render_node input carries neither a workload projection, a source listing, nor an artifacts projection")
}

// nameBound answers whether a proposed component identity yields names this
// target accepts. It reports a constraint — usable, or bounded to a shorter
// length, with the target's own reason — and never a name of its own: a name
// built here and returned would put the compiler back in the business of
// carrying names for a target it knows nothing about.
//
//go:wasmexport name_bound
func nameBound() int32 {
	var in rendercore.NameBoundInput
	out := rendercore.NameBoundOutput{}
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		out = rendercore.NameBoundFailure(err)
	} else {
		out = helmcore.NameBoundOutput(in.Proposed, in.Authored)
	}
	encoded, mErr := out.MarshalJSON()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// renderedName reports the name this target gives the resources it renders from
// a node's author-supplied source: the release name, which is what a chart's own
// templates read as .Release.Name and what those templates routinely name their
// resources after.
//
// It reports NO name for a node with no author-supplied source, and that is the
// answer rather than an omission. What this guest emits for a workload or an
// artifact set is named after the node's identity instead — there are no author
// templates for a release name to reach — so reporting one here would hand a
// recipe author a name none of that node's emitted resources carries.
//
// The name is derived through the one derivation the sourced render itself uses,
// so what is reported and what is rendered cannot part.
//
//go:wasmexport rendered_name
func renderedName() int32 {
	var in rendercore.RenderedNameInput
	out := rendercore.RenderedNameOutput{}
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		out = rendercore.RenderedNameFailure(err)
	} else if in.Sourced {
		out.Name = helmcore.ReleaseNameFor(in.NodeID, in.DerivedNameBasis)
	}
	encoded, mErr := out.MarshalJSON()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// nodeCtxFromInput builds the full render context for a node from the wire
// input: the node's identity and the provenance the render core stamps onto
// every emitted manifest (boundary path, deployment wave, Recipe identity,
// component-id override, derived-name basis, and build identity) from the node
// projection, the merged annotations from the render context, and the render-wide YAML
// indentation width the guest serializes output at (carried on the render
// context; the render core clamps its zero value to the default width).
// Carrying the full provenance here is what makes the guest's rendered
// manifests carry the whole aggrega.ai label and annotation set, so identical
// input produces byte-identical output.
func nodeCtxFromInput(in rendercore.RenderNodeInput) rendercore.NodeRenderContext {
	return rendercore.NodeRenderContext{
		NodeID:           in.Node.ID,
		Annotations:      mergeAnnotations(in.Context.Annotations),
		BoundaryPath:     in.Node.BoundaryPath,
		SyncWave:         in.Node.SyncWave,
		RecipeName:       in.Node.RecipeName,
		RecipeVersion:    in.Node.RecipeVersion,
		LabelComponentID: in.Node.LabelComponentID,
		DerivedNameBasis: in.Node.DerivedNameBasis,
		OutputLabels:     in.Node.OutputLabels,
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

// renderSourcedFromInput resolves the Helm chart root from the wire input's
// name-only source listing, asks the host to pack the chart's file tree,
// reconstructs the chart in memory, runs the shared filesystem-free render core,
// and returns each rendered manifest's serialized bytes as a RenderedFile. The
// node's values, identity, provenance, and merged annotations arrive in the node
// projection and render context.
//
// Values-schema validation runs hermetically: the chart's values.schema.json
// (and dependency charts' schemas) are validated by the render core's
// hermetic validator, whose chart-local references resolve from the packed
// chart files and whose remote http/https references resolve through the
// host's pinned external-input channel. The SDK's own live-fetching validator
// never runs in the guest. Validation only gates — it never alters values —
// so rendered bytes are byte-identical for any accepted input.
func renderSourcedFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.SourceListing == nil {
		return rendercore.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no source listing")
	}

	proj, err := packChartSource(in.SourceListing)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	bufs := make([]*loader.BufferedFile, 0, len(proj.Files))
	for _, f := range proj.Files {
		bufs = append(bufs, &loader.BufferedFile{Name: f.Path, Data: f.Content})
	}
	ch, err := loader.LoadFiles(bufs)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	values := rendercore.OrderedMapToPlain(&in.Node.Values)
	nodeCtx := nodeCtxFromInput(in)

	rendered, err := helmcore.RenderLoadedChart(context.Background(), ch, values, nodeCtx,
		helmcore.NewHermeticValuesValidator(aggrega.FetchExternalInput))
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: rendered[path]})
	}
	return out, nil
}

// renderSynthFromInput synthesizes provider-native manifests from the wire
// input's workload projection, running the shared filesystem-free synthesis
// core, and returns each synthesized manifest's serialized bytes as a
// RenderedFile. The workload and its attachments' resolved provides
// arrive in the workload projection; the node's identity, provenance, and merged
// annotations arrive in the node projection and render context. The core's
// emission order is deterministic and load-bearing, so the files are collected
// in order and never re-sorted.
func renderSynthFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.WorkloadProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no workload projection")
	}

	nodeCtx := nodeCtxFromInput(in)

	emitted, err := helmcore.SynthesizeWorkloadCore(&in.WorkloadProjection.Workload, in.WorkloadProjection.AttachmentProvides, nodeCtx)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(emitted))}
	for _, f := range emitted {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Name, Content: f.Body})
	}
	return out, nil
}

// placeArtifactsFromInput places the node's pre-evaluated artifact set into flat
// Kubernetes manifests. helm emits each artifact at its author-resolved name and
// splices the host-computed build provenance into the manifest's metadata via the
// shared placer, using the default "{{"/"}}" delimiters (helm's artifact bodies
// carry no surviving downstream template syntax, so a template-output parse
// failure is a hard authoring error). Entries are placed in author order.
func placeArtifactsFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.ArtifactsProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no artifacts projection")
	}
	indent := in.Context.YAMLIndent
	var out rendercore.RenderNodeOutput
	for _, ent := range in.ArtifactsProjection.Entries {
		body, warning, err := rendercore.SpliceArtifactProvenance(ent.Name, ent.Content, ent.Labels, ent.Annotations, true, indent)
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

// mergeAnnotations flattens the three render-context annotation buckets
// into the single merged map the helm engine reads for the well-known
// aggrega.ai/* annotations. Later buckets win on key collision, matching the
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

// packChartSource resolves the chart root from the host's name-only listing and
// asks the host to pack the chart's file tree rooted there. Chart-root resolution
// is single-sourced in the helm package (shared by the guest here), so the guest
// only wires the resolved subtree into a pack request. The chart root is packed
// as a tree, so the packed projection carries every chart file keyed by its
// chart-root-relative path — the same file set the host formerly packed.
func packChartSource(listing *rendercore.SourceListing) (*aggrega.SourceProjection, error) {
	rel, err := helmcore.ResolveChartRootRel(listing)
	if err != nil {
		return nil, fmt.Errorf("helm: locate chart root in %q: %w", listing.Root, err)
	}
	return aggrega.PackSource("tree", rel)
}

func main() {}
