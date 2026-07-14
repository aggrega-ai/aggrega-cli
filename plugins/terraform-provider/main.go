// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command terraform-provider is the terraform Provider Wasm guest. It implements
// the sourced-render path of the terraform provider behind the provider render
// ABI's render_node export. terraform is a flat-output, sourced provider: it
// vendors a Terraform module verbatim into the node's output file set and, when
// the node carries input values, writes them alongside as an auto-loaded tfvars
// overlay. It synthesizes nothing from a Workload — workload synthesis is not a
// terraform concern — so the guest handles a source projection alone and rejects
// a payload that carries a workload.
//
// The guest decodes a RenderNodeInput, resolves the acquired module source from
// the payload's name-only SourceListing, asks the host to pack the module's bytes
// through the pack-source host function (the guest touches no filesystem itself),
// and returns each file's bytes verbatim as a RenderedFile in the pack order.
// When the node's values are non-empty it appends one more file,
// aggrega.auto.tfvars.json, whose top-level keys are those values and whose
// values are their JSON encodings — a module-input overlay Terraform auto-loads.
// The overlay preserves the wire's value order and encodes each value type-native
// (strings, numbers, booleans, lists, maps), so a Terraform interpolation string
// such as ${aws_db_instance.x.endpoint} passes through as a JSON string verbatim,
// unresolved and uninterpreted. A node with no values yields no overlay; a payload
// carrying neither a source nor a workload projection yields an empty output. The
// host writes all returned bytes verbatim and runs no serializer, so the vendored
// module matches the projected source byte-for-byte.
//
// The guest is dependency-free of any Terraform, HCL, or HashiCorp library: it
// copies bytes and emits the overlay as plain JSON, interpreting nothing.
// Interpreting the Terraform content is the runtime's concern, not the compiler's.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o terraform-provider.wasm ./plugins/terraform-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/terraform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	hcljson "github.com/hashicorp/hcl/v2/json"
)

// tfvarsOverlayName is the output filename of the module-input overlay the guest
// writes from the node's values. The .auto.tfvars.json suffix is the name
// Terraform auto-loads without an explicit -var-file flag, so the overlay applies
// to the vendored module with no wrapper wiring.
const tfvarsOverlayName = "aggrega.auto.tfvars.json"

// pluginDescriptorHead is the fixed prefix of the describe() self-report: a
// v1alpha1 provider plugin declaring the render_node export, compatibility with the terraform
// provider, and its render-behavior contract (sourced and
// artifacts-standalone input, no boundary assembly). The host reads this once
// at load and validates it before invoking render_node. terraform resolves its
// module source from the host's name-only listing and asks the host to pack the
// selection, so render_node declares the aggrega_pack_source host function it
// imports; the host cross-checks this declaration against the functions the
// guest actually imports. No assemble_boundary export is offered.
const pluginDescriptorHead = `{"abi_version":"v1alpha1","plugin_type":"provider","capabilities":["terraform"],"exports":[{"name":"render_node","input_pattern":"b","host_functions":["aggrega_pack_source"],"stability":"experimental","description":"vendors a sourced Terraform module verbatim and writes the node's values as an auto-loaded tfvars overlay"},{"name":"validate_node_input","input_pattern":"b","host_functions":["aggrega_pack_source"],"stability":"experimental","description":"structurally parses the sourced Terraform module's HCL and .tf.json files and reports parse errors before render"}],"contract":{"input_kinds":{"sourced":true,"synthesized":false,"artifacts_standalone":true},"boundary_assembly":"none","reusable_instance":true},"metadata":{"author":"aggrega","license":"Apache-2.0"`

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
		// terraform does not synthesize workloads. A workload projection is an
		// orchestrator dispatch error rather than authoring drift, so it halts
		// loudly.
		pdk.SetError(errors.New("terraform-provider: render_node received a workload projection; the terraform provider does not synthesize workloads"))
		return 1
	case in.SourceListing != nil:
		proj, err := packTerraformSource(in.SourceListing)
		if err != nil {
			pdk.SetError(err)
			return 1
		}
		out.Files = make([]rendercore.RenderedFile, 0, len(proj.Files)+2)
		for _, f := range proj.Files {
			out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Path, Content: f.Content})
		}
		// Stamp build provenance as a standalone comment-only .tf file. The
		// vendored module files stay byte-for-byte verbatim and the JSON tfvars
		// overlay cannot carry a comment, so provenance lands in its own file.
		// The shared renderer formatter is the same one the artifacts placement
		// core uses, so both terraform emission paths carry identical provenance.
		nodeCtx := nodeCtxFromInput(in)
		out.Files = append(out.Files, rendercore.RenderedFile{
			Path:    terraform.ProvenanceFileName,
			Content: rendercore.FormatTerraformProvenanceComment(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx)),
		})
		if in.Node.Values.Len() > 0 {
			overlay, err := tfvarsOverlay(in.Node.Values)
			if err != nil {
				pdk.SetError(err)
				return 1
			}
			out.Files = append(out.Files, rendercore.RenderedFile{Path: tfvarsOverlayName, Content: overlay})
		}
	case in.ArtifactsProjection != nil:
		// Artifacts placement: HCL bodies get build provenance prepended as a
		// `#` comment; a .tf.json body cannot carry a comment, so provenance
		// travels in a companion comment-only .tf file. The projection's
		// labels/annotations are intentionally unused here — terraform config is
		// not Kubernetes YAML, so provenance rides via the shared HCL-comment
		// formatter over nodeCtx rather than metadata splicing.
		out.Files = terraform.PlaceArtifacts(*in.ArtifactsProjection, nodeCtxFromInput(in))
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
	// Only the sourced module is a structural input to validate. A workload or
	// artifacts projection carries no Terraform module to parse here — artifacts
	// are host-template-evaluated HCL whose parse is checked inline in render_node
	// against the emitted bytes — so a payload with no source listing yields no
	// diagnostics (a valid, empty check).
	if in.SourceListing != nil {
		proj, err := packTerraformSource(in.SourceListing)
		if err != nil {
			pdk.SetError(err)
			return 1
		}
		out.Diagnostics = validateTerraformModule(proj)
	}

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// validateTerraformModule structurally parses every Terraform configuration file
// in the packed module — .tf as native HCL syntax, .tf.json as the JSON HCL
// variant — and returns one diagnostic per parse finding, in the host's
// deterministic file order. It interprets nothing beyond the file's syntactic
// shape: it does not resolve variables, evaluate expressions, contact a provider,
// or run any Terraform toolchain — it confirms the module's HCL parses. A file
// that is neither .tf nor .tf.json is left untouched (e.g. a README, a vendored
// binary). An empty result means every configuration file parsed cleanly.
func validateTerraformModule(proj *aggrega.SourceProjection) []renderctx.Diagnostic {
	var diags []renderctx.Diagnostic
	for _, f := range proj.Files {
		switch {
		case strings.HasSuffix(f.Path, ".tf.json"):
			_, hd := hcljson.Parse(f.Content, f.Path)
			diags = append(diags, convertHCLDiagnostics(hd)...)
		case strings.HasSuffix(f.Path, ".tf"):
			_, hd := hclsyntax.ParseConfig(f.Content, f.Path, hcl.Pos{Line: 1, Column: 1})
			diags = append(diags, convertHCLDiagnostics(hd)...)
		}
	}
	return diags
}

// convertHCLDiagnostics maps HCL parse diagnostics to the shared Wasm-plugin
// diagnostic shape, preserving order. An HCL error becomes an error-severity
// diagnostic (which halts the build at the host's input-kind gate); any other
// HCL diagnostic becomes a warning. The summary and detail are joined into the
// human-readable message, and the file position — when HCL pins one — travels in
// the diagnostic location so the halt names the offending file and line.
func convertHCLDiagnostics(hd hcl.Diagnostics) []renderctx.Diagnostic {
	if len(hd) == 0 {
		return nil
	}
	out := make([]renderctx.Diagnostic, 0, len(hd))
	for _, d := range hd {
		severity := "warning"
		if d.Severity == hcl.DiagError {
			severity = "error"
		}
		msg := d.Summary
		if d.Detail != "" {
			msg = d.Summary + ": " + d.Detail
		}
		diag := renderctx.Diagnostic{
			Severity: severity,
			Code:     "terraform_hcl_parse",
			Message:  msg,
		}
		if d.Subject != nil {
			diag.Location = &renderctx.DiagnosticLocation{
				Kind: "terraform",
				URI:  d.Subject.Filename,
				Line: d.Subject.Start.Line,
			}
		}
		out = append(out, diag)
	}
	return out
}

// tfvarsOverlay encodes the node's values as a Terraform *.auto.tfvars.json
// module-input overlay. The values arrive as an ordered map whose key order the
// host fixed deterministically on the render wire; marshalling that ordered map
// preserves the order and encodes each value type-native, so the output is
// byte-identical across runs of identical input (no map iteration, no key
// re-sort). A value that is a Terraform interpolation string is a deterministic
// ahead-of-time pointer: it encodes as a JSON string verbatim, never resolved.
// The output is indented for readability, which reformats whitespace only and
// leaves the key and value order untouched.
func tfvarsOverlay(values types.OrderedMap[any]) ([]byte, error) {
	compact, err := values.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

// nodeCtxFromInput reconstructs the render context from the wire input's node
// projection — the identity and build provenance the render core stamps onto
// every emitted artifact (component-id, provider identity, boundary path,
// deployment wave, Recipe identity, and build identity). terraform vendors
// module files verbatim and needs no values, annotations, or source here; the
// context carries only the provenance the comment formatter reads, so the
// standalone provenance file matches what the host artifacts path emits for the
// same node.
func nodeCtxFromInput(in rendercore.RenderNodeInput) rendercore.NodeRenderContext {
	return rendercore.NodeRenderContext{
		NodeID:           in.Node.ID,
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
	}
}

// packTerraformSource resolves the acquired module source from the host's
// name-only listing and asks the host to pack the module's bytes. A directory
// source is vendored whole through a tree pack rooted at the source root; a
// single-file source (a lone module file) is packed as one file. This is the
// guest-side resolution the host formerly performed when it packed source bytes
// directly: terraform vendors all module files verbatim with no ambiguity halt,
// so the resolution is a straight directory-versus-file branch.
func packTerraformSource(listing *rendercore.SourceListing) (*aggrega.SourceProjection, error) {
	if listing.RootIsDir {
		return aggrega.PackSource("tree", "")
	}
	return aggrega.PackSource("file", "")
}

func main() {}
