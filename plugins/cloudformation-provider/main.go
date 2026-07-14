// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command cloudformation-provider is the CloudFormation Provider Wasm guest. It
// implements the artifacts-placement and sourced-render paths behind the provider
// render ABI's render_node export, the sourced-template structural check behind its
// validate_node_input export, the per-dispatch-group merge behind its
// assemble_boundary export, and the cross-boundary-linking export pair
// (link_publish and link_resolve) the provider render ABI declares as a paired
// contract.
//
// The guest decodes a RenderNodeInput and routes on what it carries. An artifacts
// projection is the fragment path: the host has already template-evaluated each
// artifact's content against the render context, so each entry is a
// CloudFormation fragment (a YAML or JSON object carrying a Resources map and
// optionally provides/Outputs material); the guest normalizes each fragment to
// pure-JSON long-form intrinsics and returns it as the node's contribution, which
// assemble_boundary later merges. A source listing is a sourced CloudFormation
// template: the guest asks the host to pack the selection (it touches no
// filesystem itself) and normalizes each packed template to pure-JSON long-form
// intrinsics, so no YAML short-form tag survives and every intrinsic carries
// through as its long-form pointer object, never resolved. CloudFormation
// synthesizes no workloads, so a workload projection is an orchestrator dispatch
// error that halts loudly.
//
// validate_node_input structurally parses a sourced template's packed files ahead
// of render and reports each parse finding as a diagnostic; an unparsable template
// halts the build loudly, naming the offending file, rather than surfacing as a
// later render failure.
//
// assemble_boundary folds every per-node fragment in the dispatch group into one
// pure-JSON long-form CloudFormation stack template: the fragments' Resources
// merge into one Resources map with collision-safe logical ids, and each node's
// provides entries become stack Outputs. All CloudFormation domain logic —
// short-form intrinsic expansion, resource merging, and the provides-to-Outputs
// projection — lives in the cfncore package the guest calls, so the Wasm host
// stays blind to CloudFormation semantics.
//
// CloudFormation output is JSON, which carries no comment syntax the host's build
// provenance could ride in without corrupting the document, so this guest emits
// no provenance comment; provenance for CloudFormation output is carried out of
// band rather than spliced into the template.
//
// The link_publish and link_resolve exports wire a cross-boundary uses edge
// between two CloudFormation stacks. link_publish, on the consumed stack's side,
// adds an Export to each referenced Output and returns the export and producer
// stack names as the published identity. link_resolve, on the consuming stack's
// side, replaces the placeholder the host stamped into the consuming template
// with a CloudFormation cross-stack reference: Fn::ImportValue for a peer in the
// same account and region, Fn::GetStackOutput for a peer in another account or
// region, shaped by the peer's placement annotations. All the reference shaping
// lives in the cfncore package; every name, region, and role is a deterministic
// pointer string the guest pipes and never resolves.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o cloudformation-provider.wasm ./plugins/cloudformation-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/plugins/cloudformation-provider/cfncore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// pluginDescriptorHead is the fixed prefix of the describe() self-report: a
// v1alpha1 provider plugin compatible with the cloudformation provider, declaring the render_node,
// validate_node_input, assemble_boundary, link_publish, and link_resolve exports
// and its render-behavior contract (sourced and artifacts-standalone
// input, boundary-merge assembly, and cross-boundary linking). The host reads this
// once at load and validates it before invoking any export. CloudFormation resolves
// a sourced template from the host's name-only listing and asks the host to pack the
// selection, so render_node and validate_node_input declare the aggrega_pack_source
// host function they import; the host cross-checks this declaration against the
// functions the guest actually imports. The cross-boundary-linking capability is
// declared with both link exports as the paired contract the load gate's coherence
// check requires.
const pluginDescriptorHead = `{"abi_version":"v1alpha1","plugin_type":"provider","capabilities":["cloudformation"],"exports":[{"name":"render_node","input_pattern":"b","host_functions":["aggrega_pack_source"],"stability":"experimental","description":"normalizes a CloudFormation fragment or a sourced CloudFormation template to pure-JSON long-form intrinsics"},{"name":"validate_node_input","input_pattern":"b","host_functions":["aggrega_pack_source"],"stability":"experimental","description":"structurally parses the sourced CloudFormation template and reports parse errors before render"},{"name":"assemble_boundary","input_pattern":"b","stability":"experimental","description":"merges the per-node CloudFormation fragments at one dispatch group into a single stack template"},{"name":"link_publish","input_pattern":"b","stability":"experimental","description":"publishes a consumed endpoint's cross-boundary reference identity and producer-side wiring"},{"name":"link_resolve","input_pattern":"b","stability":"experimental","description":"resolves a consuming endpoint's cross-boundary reference against a published identity"}],"contract":{"input_kinds":{"sourced":true,"synthesized":false,"artifacts_standalone":true},"boundary_assembly":"merge","cross_boundary_linking":true,"reusable_instance":true},"metadata":{"author":"aggrega","license":"Apache-2.0"`

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
	switch {
	case in.WorkloadProjection != nil:
		// CloudFormation does not synthesize workloads. A workload projection is
		// an orchestrator dispatch error rather than authoring drift, so it halts
		// loudly.
		pdk.SetError(errors.New("cloudformation-provider: render_node received a workload projection; the cloudformation provider does not synthesize workloads"))
		return 1
	case in.ArtifactsProjection != nil:
		for _, ent := range in.ArtifactsProjection.Entries {
			normalized, err := cfncore.NormalizeFragment(ent.Content)
			if err != nil {
				pdk.SetError(err)
				return 1
			}
			out.Files = append(out.Files, rendercore.RenderedFile{Path: ent.Name, Content: normalized})
		}
	case in.SourceListing != nil:
		// Sourced path: normalize the acquired CloudFormation template to pure-JSON
		// long-form intrinsics. The host has read no content; the guest asks it to
		// pack the selection and normalizes each template in memory, so no YAML
		// short-form tag survives and every intrinsic carries through as its
		// long-form pointer object, never resolved. A directory source vendors its
		// non-template files verbatim.
		proj, err := packSource(in.SourceListing)
		if err != nil {
			pdk.SetError(err)
			return 1
		}
		normalized, err := cfncore.NormalizeSourced(sourcedFiles(proj), in.SourceListing.RootIsDir)
		if err != nil {
			pdk.SetError(err)
			return 1
		}
		out.Files = make([]rendercore.RenderedFile, 0, len(normalized))
		for _, f := range normalized {
			out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Path, Content: f.Content})
		}
	}
	// A payload with no projection yields an empty output.

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

//go:wasmexport validate_node_input
func validateNodeInput() int32 {
	var in rendercore.RenderNodeInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	var out rendercore.ValidateNodeOutput
	// Only the sourced template is a structural input to validate. An artifacts
	// projection is host-template-evaluated CloudFormation whose parse is checked
	// inline in render_node against the emitted bytes, and a workload projection is
	// a dispatch error render_node rejects — so a payload with no source listing
	// yields no diagnostics (a valid, empty check).
	if in.SourceListing != nil {
		proj, err := packSource(in.SourceListing)
		if err != nil {
			pdk.SetError(err)
			return 1
		}
		out.Diagnostics = cfncore.ValidateSourced(sourcedFiles(proj), in.SourceListing.RootIsDir)
	}

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// sourcedFiles maps a packed source projection to the core's file shape, preserving
// the host's deterministic pack order so render and validation walk identical input.
func sourcedFiles(proj *aggrega.SourceProjection) []cfncore.SourcedFile {
	files := make([]cfncore.SourcedFile, 0, len(proj.Files))
	for _, f := range proj.Files {
		files = append(files, cfncore.SourcedFile{Path: f.Path, Content: f.Content})
	}
	return files
}

//go:wasmexport assemble_boundary
func assembleBoundary() int32 {
	var in rendercore.AssembleBoundaryInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	nodes := make([]cfncore.NodeFragment, 0, len(in.RenderedNodes))
	for i := range in.RenderedNodes {
		rn := in.RenderedNodes[i]
		files := make([][]byte, 0, len(rn.Rendered.Files))
		for _, f := range rn.Rendered.Files {
			files = append(files, f.Content)
		}
		nodes = append(nodes, cfncore.NodeFragment{NodeID: rn.ID, Files: files})
	}

	stack, err := cfncore.AssembleStack(nodes)
	if err != nil {
		pdk.SetError(err)
		return 1
	}

	out := rendercore.AssembleBoundaryOutput{
		Files:       []rendercore.RenderedFile{{Path: cfncore.StackTemplateName, Content: stack}},
		NestPerNode: false,
	}
	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

//go:wasmexport link_publish
func linkPublish() int32 {
	var in rendercore.LinkPublishInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}
	out, err := cfncore.LinkPublish(in.Edge)
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

//go:wasmexport link_resolve
func linkResolve() int32 {
	var in rendercore.LinkResolveInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}
	out, err := cfncore.LinkResolve(in.Edge, in.Handle, in.ConsumerFiles)
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

// packSource resolves the acquired CloudFormation template source from the host's
// name-only listing and asks the host to pack its bytes. A directory source is
// vendored whole through a tree pack; a single-file source is packed as one file.
// The guest touches no filesystem itself — the host reads and returns the bytes.
func packSource(listing *rendercore.SourceListing) (*aggrega.SourceProjection, error) {
	if listing.RootIsDir {
		return aggrega.PackSource("tree", "")
	}
	return aggrega.PackSource("file", "")
}

func main() {}
