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
// The guest decodes a RenderNodeInput, reconstructs the module file set in memory
// from the payload's SourceProjection (the host read the module's files at compile
// time and packed them into the payload — the guest touches no filesystem), and
// returns each file's bytes verbatim as a RenderedFile in the projection's order.
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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	pdk "github.com/extism/go-pdk"
)

// tfvarsOverlayName is the output filename of the module-input overlay the guest
// writes from the node's values. The .auto.tfvars.json suffix is the name
// Terraform auto-loads without an explicit -var-file flag, so the overlay applies
// to the vendored module with no wrapper wiring.
const tfvarsOverlayName = "aggrega.auto.tfvars.json"

// provenanceFileName is the output filename of the standalone build-provenance
// file the guest emits alongside the vendored module. The overlay carrying the
// node's values is JSON (aggrega.auto.tfvars.json) and cannot hold a comment, so
// provenance is stamped in a separate comment-only .tf file the Terraform loader
// parses as an empty (comment-only) config.
const provenanceFileName = "aggrega.provenance.tf"

// pluginDescriptor is the static describe() self-report: a v1alpha1 provider
// plugin declaring the render_node export, compatibility with the terraform
// provider, and its plugin-level capability self-report (sourced and
// artifacts-standalone input, no boundary assembly). The host reads this once
// at load and validates it before invoking render_node. terraform vendors
// module files verbatim and fetches nothing, so no host functions are declared
// and no assemble_boundary export is offered.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["terraform"],"exports":[{"name":"render_node","input_pattern":"b","stability":"experimental","description":"vendors a sourced Terraform module verbatim and writes the node's values as an auto-loaded tfvars overlay"}],"capabilities":{"input_kinds":{"sourced":true,"synthesized":false,"artifacts_standalone":true},"boundary_assembly":"none"},"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

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
	switch {
	case in.WorkloadProjection != nil:
		// terraform does not synthesize workloads. A workload projection is an
		// orchestrator dispatch error rather than authoring drift, so it halts
		// loudly.
		pdk.SetError(errors.New("terraform-provider: render_node received a workload projection; the terraform provider does not synthesize workloads"))
		return 1
	case in.SourceProjection != nil:
		out.Files = make([]renderer.RenderedFile, 0, len(in.SourceProjection.Files)+2)
		for _, f := range in.SourceProjection.Files {
			out.Files = append(out.Files, renderer.RenderedFile{Path: f.Path, Content: f.Content})
		}
		// Stamp build provenance as a standalone comment-only .tf file. The
		// vendored module files stay byte-for-byte verbatim and the JSON tfvars
		// overlay cannot carry a comment, so provenance lands in its own file.
		// The shared renderer formatter is the same one the host artifacts path
		// uses, so both terraform emission paths carry identical provenance.
		nodeCtx := nodeCtxFromInput(in)
		out.Files = append(out.Files, renderer.RenderedFile{
			Path:    provenanceFileName,
			Content: renderer.FormatTerraformProvenanceComment(nodeCtx, renderer.ProvenanceRecipeURIFromNodeCtx(nodeCtx)),
		})
		if in.Node.Values.Len() > 0 {
			overlay, err := tfvarsOverlay(in.Node.Values)
			if err != nil {
				pdk.SetError(err)
				return 1
			}
			out.Files = append(out.Files, renderer.RenderedFile{Path: tfvarsOverlayName, Content: overlay})
		}
	}
	// A payload with neither projection yields an empty output.

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
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
func nodeCtxFromInput(in renderer.RenderNodeInput) renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:           in.Node.ID,
		BoundaryPath:     in.Node.BoundaryPath,
		SyncWave:         in.Node.SyncWave,
		RecipeName:       in.Node.RecipeName,
		RecipeVersion:    in.Node.RecipeVersion,
		LabelComponentID: in.Node.LabelComponentID,
		ProviderName:     in.Node.Provenance.ProviderName,
		ProviderVersion:  in.Node.Provenance.ProviderVersion,
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: in.Node.Provenance.CompilerVersion,
			AggregateName:   in.Node.Provenance.AggregateName,
			Environment:     in.Node.Provenance.Environment,
		},
	}
}

func main() {}
