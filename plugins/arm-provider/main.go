// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command arm-provider is the Azure Resource Manager Provider Wasm guest. It
// implements the artifacts-placement and sourced-render paths behind the provider
// render ABI's render_node export, the sourced-template check behind its
// validate_node_input export, the per-dispatch-group merge behind its
// assemble_boundary export, and the cross-boundary-linking export pair
// (link_publish and link_resolve).
//
// The guest decodes a RenderNodeInput and routes on what it carries. An artifacts
// projection is the fragment path: each artifact's content, already evaluated by
// the host against the render context, is an ARM template fragment; the guest
// reads it, stamps the node's build provenance on each resource the fragment
// deploys, and returns it as the node's contribution. A source listing is a
// compiled template: the guest selects the file that holds it, asks the host to
// pack that file, and wraps the template as one nested template resource whose
// parameters the node's values bind. ARM synthesizes no workloads, so a workload
// projection is an orchestrator dispatch error that halts loudly.
//
// assemble_boundary folds every contribution of a dispatch group into one resource
// group scope template, written as arm-template.json. render_node declares the
// symbolic names and the output names each node's fragments carry, so the host
// halts the build where two nodes claim one name. All ARM domain logic lives in the
// armcore package the guest calls, so the Wasm host stays agnostic to ARM
// semantics.
//
// The link_publish and link_resolve exports wire a uses edge between two arm
// boundaries, each a template of its own. link_publish, on the producing
// template's side, adds a string output for each value the consumer reads and
// records the name the template is deployed under; link_resolve, on the
// consuming template's side, replaces the placeholder the host stamped there with
// ARM's reference() to that output, addressed by the resource ID built from the
// recorded name and the producer's placement annotations. Every name and ID is a
// deterministic pointer string the guest pipes and never resolves against Azure.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o arm-provider.wasm ./plugins/arm-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// descriptor is the describe() self-report: a provider plugin compatible with the
// arm provider, declaring the render_node, validate_node_input,
// assemble_boundary, link_publish and link_resolve exports and its
// render-behavior contract (sourced and artifacts-standalone input,
// boundary-merge assembly, cross-boundary linking, and its honouring of omit
// entries). The host reads it once at load and validates it before invoking any
// export. The guest resolves a sourced template from the host's name-only listing
// and asks the host to pack the selection, so render_node and validate_node_input
// declare the aggrega_pack_source host function they import; the host cross-checks
// this declaration against the functions the guest actually imports. The
// cross-boundary-linking capability is declared with both link exports as the
// paired contract the load gate's coherence check requires.
//
// aggrega.MarshalDescriptor folds the build-provenance version and commit into
// the metadata when the Go linker stamps them at build time; a plain build leaves
// them unset, so an unstamped build's descriptor is byte-identical to a stamped
// build's minus the provenance.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "provider",
		Capabilities: []string{armcore.TargetName},
		Exports: []aggrega.PluginExport{
			{
				Name:          "render_node",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "stamps provenance on an ARM template fragment, or wraps a sourced ARM template as one nested template resource",
			},
			{
				Name:          "validate_node_input",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "checks that the sourced ARM template parses and has the shape of a template the provider wraps, and reports each fault before render",
			},
			{
				Name:         "assemble_boundary",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "merges the per-node ARM template fragments at one dispatch group into a single template at the scope the boundary deploys at, nesting resource group content in a level of its own at subscription scope, ordering the merged resources by the declared wave",
			},
			{
				Name:         "link_publish",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "adds the template outputs a cross-boundary link reads and records the name the producing template is deployed under",
			},
			{
				Name:         "link_resolve",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "replaces a consuming template's cross-boundary placeholder with a reference() to the producing template's output",
			},
		},
		Contract: &aggrega.ProviderContract{
			InputKinds:           aggrega.InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: true},
			BoundaryAssembly:     "merge",
			ReusableInstance:     true,
			CrossBoundaryLinking: true,
			// A sourced render leaves out every resource an omit entry names, and
			// reports how many resources each entry left out.
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
// how a failure crosses and this stays about what arm renders.
func renderNodeOutput() (rendercore.RenderNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	var out rendercore.RenderNodeOutput
	var symbols, outputs []string
	identity := armcore.ContributorIdentity(armcore.Contribution{NodeID: in.Node.ID, NameBasis: in.Node.DerivedNameBasis})
	switch {
	case in.WorkloadProjection != nil:
		// ARM does not synthesize workloads. A workload projection is an
		// orchestrator dispatch error rather than authoring drift, so it halts
		// loudly.
		return rendercore.RenderNodeOutput{}, errors.New(
			"arm-provider: render_node received a workload projection; the arm provider does not synthesize workloads")
	case in.ArtifactsProjection != nil:
		for _, ent := range in.ArtifactsProjection.Entries {
			fragment, diags, err := armcore.ReadFragment(ent.Content)
			if err != nil {
				return rendercore.RenderNodeOutput{}, err
			}
			out.Diagnostics = append(out.Diagnostics, diags...)
			// The entry's own provenance halves are read rather than the node
			// context's, which is what keeps an attachment-carried artifact stamping
			// the identity the host computed for that entry: recomputing from the
			// node would substitute the consuming Recipe's identity for it.
			set := rendercore.FoldProvenanceSet(ent.Labels, ent.Annotations)
			if advisory := armcore.StampProvenance(map[string]any{"resources": fragment.Sections["resources"]}, set); advisory != "" {
				out.Diagnostics = append(out.Diagnostics, rendercore.ArtifactProvenanceSkippedDiagnostic(advisory))
			}
			stamped, err := armcore.WriteFragment(fragment)
			if err != nil {
				return rendercore.RenderNodeOutput{}, err
			}
			out.Files = append(out.Files, rendercore.RenderedFile{Path: ent.Name, Content: stamped})
			symbols = append(symbols, keysOf(fragment.Sections["resources"])...)
			for _, name := range keysOf(fragment.Sections["outputs"]) {
				outputs = append(outputs, armcore.OutputName(identity, name))
			}
		}
	case in.SourceListing != nil:
		path, err := armcore.SelectTemplate(*in.SourceListing)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		proj, err := aggrega.PackSource("file", path)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		content, err := firstFile(proj)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		nodeCtx := nodeCtxFromInput(in)
		fragment, symbol, omitted, err := armcore.RenderSourced(armcore.SourcedNode{
			Identity:        identity,
			Path:            path,
			Content:         content,
			Values:          in.Node.Values,
			ValuesMapped:    in.Node.ValuesMapped,
			SensitiveValues: in.Node.SensitiveValues,
			Omits:           nodeCtx.Omits,
			Provenance: rendercore.FoldProvenanceSet(
				rendercore.BuildAggregaProvenanceLabels(nodeCtx),
				rendercore.BuildAggregaProvenanceAnnotations(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx))),
		})
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		out.Omitted = omitted
		out.Files = []rendercore.RenderedFile{{Path: armcore.TemplateFileName, Content: fragment}}
		symbols = []string{symbol}
	}
	// A payload with no projection yields an empty output.

	// The names this node's output addresses its content by, declared per key space
	// so the host can confirm no two nodes assembling into one template claim the
	// same one. The host writes the file bytes verbatim without reading them, so the
	// declaration is how the guest asks for a check the host could not otherwise
	// make.
	if symbols = unique(symbols); len(symbols) > 0 {
		out.DerivedKeys.Set(armcore.KeySpaceSymbolicNames, symbols)
	}
	if outputs = unique(outputs); len(outputs) > 0 {
		out.DerivedKeys.Set(armcore.KeySpaceOutputNames, outputs)
	}
	return out, nil
}

//go:wasmexport validate_node_input
func validateNodeInput() int32 {
	out, err := validateNodeOutput()
	if err != nil {
		out = rendercore.ValidateNodeFailure(err)
	}

	encoded, mErr := out.EncodeCBOR()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// validateNodeOutput decodes the validate_node_input payload and runs the check,
// returning the error rather than reporting it for the reason renderNodeOutput
// does.
func validateNodeOutput() (rendercore.ValidateNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.ValidateNodeOutput{}, err
	}

	var out rendercore.ValidateNodeOutput
	// Only the sourced template is an input to validate here. An artifacts
	// projection is host-evaluated text read in render_node, and a workload
	// projection is a dispatch error render_node rejects, so a payload with no
	// source listing yields no diagnostics (a valid, empty check).
	if in.SourceListing != nil {
		path, err := armcore.SelectTemplate(*in.SourceListing)
		if err != nil {
			return rendercore.ValidateNodeOutput{}, err
		}
		proj, err := aggrega.PackSource("file", path)
		if err != nil {
			return rendercore.ValidateNodeOutput{}, err
		}
		content, err := firstFile(proj)
		if err != nil {
			return rendercore.ValidateNodeOutput{}, err
		}
		out.Diagnostics = armcore.ValidateTemplate(path, content)
	}
	return out, nil
}

// nodeCtxFromInput reconstructs the render context from the wire input's node
// projection: the identity and build provenance every stamped resource carries
// (component-id, provider identity, boundary path, sync wave, Recipe
// identity, the Recipe's own output labels and build identity), and the omit
// entries the input carries. It exists for the sourced path, where the payload
// carries the node's provenance rather than the per-entry halves an artifacts
// projection computes per entry.
func nodeCtxFromInput(in rendercore.RenderNodeInput) rendercore.NodeRenderContext {
	return rendercore.NodeRenderContext{
		NodeID:           in.Node.ID,
		Omits:            rendercore.ResolvedOmitsOf(in.Omits),
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
	}
}

// firstFile returns the bytes of the one file the host packed for a selection.
func firstFile(proj *aggrega.SourceProjection) ([]byte, error) {
	if proj == nil || len(proj.Files) != 1 {
		return nil, errors.New("arm-provider: the host packed no template file for the source; check that the source holds the compiled template")
	}
	return proj.Files[0].Content, nil
}

// keysOf returns the keys of a section's entries in sorted order, so the names a
// node declares are a function of its fragments alone.
func keysOf(entries map[string]any) []string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unique returns names with each repeated name kept once, in sorted order. Two
// fragments of one node restating a name with one body reach the template once.
func unique(names []string) []string {
	sort.Strings(names)
	out := names[:0]
	for i, n := range names {
		if i == 0 || n != names[i-1] {
			out = append(out, n)
		}
	}
	return out
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

// assembleBoundaryOutput decodes the assemble_boundary input and folds the
// dispatch group's fragments into one template, returning the error rather than
// reporting it for the reason renderNodeOutput does.
func assembleBoundaryOutput() (rendercore.AssembleBoundaryOutput, error) {
	var in rendercore.AssembleBoundaryInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	contribs := make([]armcore.Contribution, 0, len(in.RenderedNodes))
	for i := range in.RenderedNodes {
		rn := in.RenderedNodes[i]
		c := armcore.Contribution{NodeID: rn.ID, NameBasis: rn.DerivedNameBasis, SyncWave: rn.SyncWave}
		for _, f := range rn.Rendered.Files {
			c.Files = append(c.Files, f.Content)
			c.Paths = append(c.Paths, f.Path)
		}
		if rn.Annotations.Len() > 0 {
			c.Annotations = make(map[string]string, rn.Annotations.Len())
			for k, v := range rn.Annotations.All() {
				c.Annotations[k] = v
			}
		}
		contribs = append(contribs, c)
	}
	return armcore.Assemble(contribs)
}

//go:wasmexport link_publish
func linkPublish() int32 {
	out, err := linkPublishOutput()
	if err != nil {
		out = rendercore.LinkPublishFailure(err)
	}

	encoded, mErr := out.EncodeCBOR()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// linkPublishOutput decodes the link_publish input and produces the producing
// template's side of the link, returning the error rather than reporting it for
// the reason renderNodeOutput does.
func linkPublishOutput() (rendercore.LinkPublishOutput, error) {
	var in rendercore.LinkPublishInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	return armcore.LinkPublish(in.Edge, in.ProducerFiles)
}

//go:wasmexport link_resolve
func linkResolve() int32 {
	out, err := linkResolveOutput()
	if err != nil {
		out = rendercore.LinkResolveFailure(err)
	}

	encoded, mErr := out.EncodeCBOR()
	if mErr != nil {
		pdk.SetError(mErr)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// linkResolveOutput decodes the link_resolve input and produces the consuming
// template's side of the link, returning the error rather than reporting it for
// the reason renderNodeOutput does.
func linkResolveOutput() (rendercore.LinkResolveOutput, error) {
	var in rendercore.LinkResolveInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	return armcore.LinkResolve(in.Edge, in.Handle, in.ConsumerFiles)
}

func main() {}
