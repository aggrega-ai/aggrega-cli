// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command terraform-provider is the terraform Provider Wasm guest. It implements
// the sourced-render path of the terraform provider behind the provider render
// ABI's render_node export, and the root-configuration path behind its
// assemble_boundary export. terraform is a sourced provider: it vendors a
// Terraform module verbatim into the node's output file set. It synthesizes
// nothing from a Workload — workload synthesis is not a terraform concern — so
// the guest rejects a payload that carries a workload.
//
// A boundary's components are composed rather than emitted side by side. The
// assemble_boundary guest writes one root configuration for the boundary,
// declaring each component there as a `module` block whose source is that
// component's own sub-directory, and it tells the host to place each component's
// files in that sub-directory. This is Terraform's standard root-composes-children
// shape, and it is what makes a relationship between two components expressible:
// a Terraform dependency graph is per-configuration, so components emitted into
// separate directories are separate graphs, and neither a reference nor a
// depends_on can reach across them. The layout is unconditional — every boundary
// the terraform provider renders gets a root configuration, whether or not
// anything in it declares an ordering — because the alternative is two layouts
// with different addressing rules.
//
// A declared deployment wave becomes ordering in that root configuration: each
// wave's module blocks declare a depends_on naming every component of the
// immediately-preceding populated wave, so the engine completes its actions on the
// earlier wave before it begins the later one. It orders the actions the engine
// takes and is not readiness ordering — a configuration expresses no notion of a
// resource being serviceable. A boundary with fewer than two populated waves
// declares no depends_on at all.
//
// A component's input values are written into that same root configuration, as
// the arguments of the component's own module block. The position is the point:
// a module block argument is an expression the engine evaluates, so a value the
// Recipe wrote as a Terraform interpolation — ${module.other.endpoint}, say —
// arrives somewhere the engine can resolve it. Nothing resolves it while the
// configuration is being written: the value is piped through as the text the
// Recipe supplied, unresolved and uninterpreted, and it is the engine that
// resolves it at apply time. The values are emitted in the order the payload
// carries them and each is encoded from its own type (strings, numbers,
// booleans, lists, maps), so identical input yields byte-identical output. A
// value whose name falls outside Terraform's grammar for an input is refused
// with an error naming it, because an argument's name is the name of the input
// variable it binds and mapping it would bind a variable the module never
// declared.
//
// The guest decodes a RenderNodeInput, resolves the acquired module source from
// the payload's name-only SourceListing, asks the host to pack the module's bytes
// through the pack-source host function (the guest touches no filesystem itself),
// and returns each file's bytes verbatim as a RenderedFile in the pack order. A
// payload with no projection yields an empty output. The host writes all returned
// bytes verbatim and runs no serializer, so the vendored module matches the
// projected source byte-for-byte.
//
// Where the payload marks one of the node's values as carrying material an author
// declared sensitive, the guest appends aggrega_override.tf.json beside the
// module's own files, declaring `sensitive = true` for each marked input.
// Terraform merges a file whose name ends in _override into the configuration
// beside it, so the marked input's own declaration acquires the attribute while
// the vendored module's bytes stay untouched — and the declaration it modifies is
// the module's own `variable` block, which is why the file rides with the module
// rather than with the root configuration that supplies the value. Which input is
// marked is read from the payload and never derived: the guest inspects no
// variable name, no key pattern, and nothing in the module's own text. An
// unmarked node gains no such file.
//
// Terraform's `sensitive` bounds display, not storage: it redacts the value in
// plan and apply output and refuses to export it from an unmarked root output.
// It does not encrypt the value and does not change how state records it — a
// sensitive input is stored in state in the clear, so protecting state remains a
// matter of how the state backend is configured.
//
// The guest never rewrites the module: it passes the vendored bytes through. It
// does read the module's Terraform configuration files — validate_node_input
// parses each one with the HCL library and reports one diagnostic per syntax
// finding. That parse is read-only: it discards the parsed body, so the bytes it
// reports on are the bytes the guest returns. It confirms the module's HCL is
// well-formed and nothing more, resolving no variables, evaluating no
// expressions, contacting no provider, and running no Terraform toolchain. A file
// that does not parse yields an halt-severity diagnostic, which halts the build;
// what the configuration means once it does parse is the runtime's concern.
//
// The same read reports, as a warning that refuses nothing, each `module` block
// whose source the engine fetches when it initialises the configuration — an
// address rather than a local path, or a local path climbing out of the vendored
// tree — because the build's pin does not reach such a module. It reads the
// files the engine loads when the module is called, following local sources from
// the module's own directory, and an authored body's module calls are read the
// same way when render_node places it. Only what a source says is read, never
// what the module it names contains, and the emitted bytes are unchanged.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o terraform-provider.wasm ./plugins/terraform-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
	"github.com/hashicorp/hcl/v2"
)

// descriptor is the describe() self-report: a provider plugin declaring
// the render_node, validate_node_input and assemble_boundary exports,
// compatibility with the terraform provider, and its render-behavior contract
// (sourced and artifacts-standalone input, nest-per-component boundary
// assembly, and the files a Recipe contributes to the boundary's shared output
// accepted at the root). The host reads it once at load and validates it before invoking the
// exports. terraform resolves its module source from the host's name-only listing
// and asks the host to pack the selection, so the render and validation exports
// declare the aggrega_pack_source host function they import; the host cross-checks
// this declaration against the functions the guest actually imports. The
// assemble_boundary export works from its payload alone and imports no host
// function.
//
// aggrega.MarshalDescriptor folds the build-provenance version and commit into
// the metadata when the Go linker stamps them at build time; a plain build
// leaves them unset, so an unstamped build's descriptor is byte-identical to a
// stamped build's minus the provenance.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "provider",
		Capabilities: []string{"terraform"},
		Exports: []aggrega.PluginExport{
			{
				Name:          "render_node",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "vendors a sourced Terraform module verbatim and marks each input the payload reports as carrying declared-sensitive material",
			},
			{
				Name:          "validate_node_input",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "structurally parses the sourced Terraform module's HCL and .tf.json files, reports parse errors before render, and warns on each module call the engine fetches at initialisation",
			},
			{
				Name:         "assemble_boundary",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "writes the root configuration for one boundary, declaring each component rendered there as a module block that points at that component's own sub-directory and carries that component's values as its arguments",
			},
		},
		Contract: &aggrega.ProviderContract{
			InputKinds:       aggrega.InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: true},
			BoundaryAssembly: "nest",
			ReusableInstance: true,
			// Each contributor is a module of its own, and the directory Terraform
			// calls those is this target's word rather than the host's.
			NestSegment: terraformcore.ModulesDirName,
			// Terraform reads every configuration file in the root module's own
			// directory, so a file a Recipe contributes to the boundary's shared
			// output, written there beside the module blocks this guest assembles, is
			// part of the root configuration. That is the one place Terraform accepts
			// a provider configuration. The guest is handed none of those files and
			// writes none of them: the host evaluates, merges and writes them.
			BoundaryArtifacts: true,
			// What the module ecosystem never ships beside a module. The host
			// records these rules when an entity is pinned and applies them; the
			// guest declares them and filters nothing itself.
			SourceIgnore: &aggrega.SourceIgnore{
				Patterns: terraformcore.SourceIgnorePatterns(),
				Files:    terraformcore.SourceIgnoreFiles(),
			},
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
// how a failure crosses and this stays about what terraform renders.
func renderNodeOutput() (rendercore.RenderNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	var out rendercore.RenderNodeOutput
	switch {
	case in.WorkloadProjection != nil:
		// terraform does not synthesize workloads. A workload projection is an
		// orchestrator dispatch error rather than authoring drift, so it halts
		// loudly.
		return rendercore.RenderNodeOutput{}, errors.New(
			"terraform-provider: render_node received a workload projection; the terraform provider does not synthesize workloads")
	case in.SourceListing != nil:
		proj, err := packTerraformSource(in.SourceListing)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		out.Files = make([]rendercore.RenderedFile, 0, len(proj.Files)+2)
		for _, f := range proj.Files {
			out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Path, Content: f.Content})
		}
		// Stamp build provenance as a standalone comment-only .tf file. The
		// vendored module files stay byte-for-byte verbatim, so provenance cannot
		// be prepended to any of them and lands in its own file instead. The
		// shared renderer formatter is the same one the artifacts placement core
		// uses, so both terraform emission paths carry identical provenance.
		nodeCtx := nodeCtxFromInput(in)
		out.Files = append(out.Files, rendercore.RenderedFile{
			Path:    terraformcore.ProvenanceFileName,
			Content: rendercore.FormatTerraformProvenanceComment(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx)),
		})
		if in.Node.Values.Len() > 0 {
			// The values themselves are not emitted here. They travel to the
			// boundary assembler, which writes them as the arguments of this
			// component's module block — the position the engine evaluates.
			//
			// Translate the payload's sensitivity marking into Terraform's own
			// per-variable primitive. Which module input carries declared-sensitive
			// material is the payload's finding, read here and never derived: the
			// guest inspects no variable name, no key pattern, and nothing in the
			// module's own text. The override rides with the module's own files
			// because Terraform merges an override only into the configuration in
			// its own directory, and the `variable` blocks it marks are declared
			// there — so the file that declares an input and the file that marks it
			// are siblings by construction rather than by a second placement rule.
			override, err := terraformcore.SensitivityOverride(in.Node.Values, in.Node.SensitiveRenderedKeys)
			if err != nil {
				return rendercore.RenderNodeOutput{}, err
			}
			if override != nil {
				out.Files = append(out.Files, rendercore.RenderedFile{Path: terraformcore.OverrideFileName, Content: override})
			}
		}
	case in.ArtifactsProjection != nil:
		// Artifacts placement: HCL bodies get build provenance prepended as a
		// `#` comment; a .tf.json body cannot carry a comment, so provenance
		// travels in a companion comment-only .tf file. The projection's
		// labels/annotations are intentionally unused here — terraform config is
		// not Kubernetes YAML, so provenance rides via the shared HCL-comment
		// formatter over nodeCtx rather than metadata splicing.
		out.Files = terraformcore.PlaceArtifacts(*in.ArtifactsProjection, nodeCtxFromInput(in))
		// An authored body's module calls are read the way a vendored module's
		// are, from the bodies as the Recipe authored them. The warnings travel
		// beside the files and change none of their bytes.
		bodies := make([]terraformcore.ConfigFile, 0, len(in.ArtifactsProjection.Entries))
		for _, ent := range in.ArtifactsProjection.Entries {
			bodies = append(bodies, terraformcore.ConfigFile{Path: ent.Name, Content: ent.Content})
		}
		out.Diagnostics = terraformcore.ModuleFetchedAtInitDiagnostics(bodies)
	}
	// A payload with no projection yields an empty output.

	// Declare each emitted file's rendered format from the terraform provider's
	// own suffix classification, so the host routes the file to a matching parser
	// without inferring the format from the extension. A file whose suffix the
	// classifier does not recognize keeps an empty format and the host falls back
	// to its extension table.
	for i := range out.Files {
		if out.Files[i].Format == "" {
			out.Files[i].Format = terraformFileFormat(out.Files[i].Path)
		}
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

// assembleBoundaryOutput decodes the assemble_boundary input and writes the
// boundary's root configuration, returning the error rather than reporting it for
// the reason renderNodeOutput does.
func assembleBoundaryOutput() (rendercore.AssembleBoundaryOutput, error) {
	var in rendercore.AssembleBoundaryInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}
	return assembleBoundaryFromInput(in)
}

// assembleBoundaryFromInput writes the boundary's root configuration from the
// contributors the payload names. The root declares one `module` block per
// contributor, each pointing at the directory that contributor's own files sit
// in — the position the host reports on the payload, which is the position the
// host wrote them to, so the block and the directory cannot drift. The guest
// names the directory those sub-directories sit under on its describe()
// contract, because the host places a contributor's files while that
// contributor renders, a whole pass before this export is called.
//
// The contributors are read from the payload's rendered-node list in the order
// the host supplies, which is fixed deterministically, so identical input yields
// byte-identical output. Each contributor's declared deployment wave travels with
// it, and the root configuration expresses the ordering those waves state as
// `depends_on` between the module blocks. Each contributor's rendered values
// travel with it too and become that component's module block arguments, which is
// where the engine evaluates them. Nothing in the module's own text is read and no
// component's files are rewritten: this export composes and places, and the
// vendored bytes stay verbatim.
func assembleBoundaryFromInput(in rendercore.AssembleBoundaryInput) (rendercore.AssembleBoundaryOutput, error) {
	components := make([]terraformcore.RootComponent, 0, len(in.RenderedNodes))
	for _, n := range in.RenderedNodes {
		components = append(components, terraformcore.RootComponent{ID: n.ID, OutputDirectory: n.OutputDirectory, SyncWave: n.SyncWave, Values: n.Values})
	}

	files, err := terraformcore.AssembleRoot(components)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	out := rendercore.AssembleBoundaryOutput{Files: files}
	for i := range out.Files {
		if out.Files[i].Format == "" {
			out.Files[i].Format = terraformFileFormat(out.Files[i].Path)
		}
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

// validateNodeOutput decodes the validate_node_input payload and runs the
// structural check, returning the error rather than reporting it for the reason
// renderNodeOutput does.
func validateNodeOutput() (rendercore.ValidateNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.ValidateNodeOutput{}, err
	}

	var out rendercore.ValidateNodeOutput
	// Only the sourced module is a structural input to validate. A workload or
	// artifacts projection carries no Terraform module to parse here — this
	// check's payload carries no artifacts body, so an authored body's module
	// calls are read in render_node, where the body is in hand — and a payload
	// with no source listing yields no diagnostics (a valid, empty check).
	if in.SourceListing != nil {
		proj, err := packTerraformSource(in.SourceListing)
		if err != nil {
			return rendercore.ValidateNodeOutput{}, err
		}
		out.Diagnostics = validateTerraformModule(proj)
	}
	return out, nil
}

// terraformFileFormat classifies an emitted file's rendered format from its
// suffix: a .tf.json file is JSON-encoded HCL, a .tf file is native HCL syntax.
// Any other suffix returns the empty string, leaving the host to fall back to its
// extension table. The format values originate here in the provider guest, not in
// the host core.
func terraformFileFormat(path string) string {
	switch {
	case strings.HasSuffix(path, ".tf.json"):
		return "json"
	case strings.HasSuffix(path, ".tf"):
		return "hcl"
	default:
		return ""
	}
}

// validateTerraformModule structurally parses every Terraform configuration file
// in the packed module — .tf as native HCL syntax, .tf.json as the JSON HCL
// variant — and returns one diagnostic per parse finding, in the host's
// deterministic file order, followed by one warning per module call the engine
// fetches when it initialises the configuration. It interprets nothing beyond
// the file's syntactic shape and what a `module` block's source says: it does not
// resolve variables, evaluate expressions, contact a provider, read the module a
// call names, or run any Terraform toolchain. A file that is neither .tf nor
// .tf.json is left untouched (e.g. a README, a vendored binary). An empty result
// means every configuration file parsed cleanly and every module call the engine
// loads reads files the build vendored.
//
// Every packed configuration file is parsed, examples and tests included, while
// the module calls are read only from the files the engine loads when the module
// is called — see [terraformcore.FetchedModuleCalls] — so a call written in an
// example the module ships is not reported as a fetch that never happens.
func validateTerraformModule(proj *aggrega.SourceProjection) []renderctx.Diagnostic {
	var diags []renderctx.Diagnostic
	files := make([]terraformcore.ConfigFile, 0, len(proj.Files))
	for _, f := range proj.Files {
		files = append(files, terraformcore.ConfigFile{Path: f.Path, Content: f.Content})
		if _, hd, ok := terraformcore.ParseConfigFile(f.Path, f.Content); ok {
			diags = append(diags, convertHCLDiagnostics(hd)...)
		}
	}
	return append(diags, terraformcore.ModuleFetchedAtInitDiagnostics(files)...)
}

// convertHCLDiagnostics maps HCL parse diagnostics to the shared Wasm-plugin
// diagnostic shape, preserving order. An HCL error becomes a halt-severity
// diagnostic (which halts the build at the host's input-kind gate); any other
// HCL diagnostic becomes a warning. The summary and detail are joined into the
// human-readable message, and the file position — when HCL pins one — travels in
// the diagnostic location so the halt names the offending file and line.
// terraformHCLParseCode is the machine-matchable code every diagnostic derived
// from an HCL parse finding carries, whichever severity that finding maps to.
const terraformHCLParseCode = "terraform-hcl-parse"

func convertHCLDiagnostics(hd hcl.Diagnostics) []renderctx.Diagnostic {
	if len(hd) == 0 {
		return nil
	}
	out := make([]renderctx.Diagnostic, 0, len(hd))
	for _, d := range hd {
		severity := abi.SeverityWarn
		if d.Severity == hcl.DiagError {
			severity = abi.SeverityHalt
		}
		msg := d.Summary
		if d.Detail != "" {
			msg = d.Summary + ": " + d.Detail
		}
		diag := renderctx.Diagnostic{
			Severity: severity,
			Code:     terraformHCLParseCode,
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

// packTerraformSource resolves the acquired module source from the host's
// name-only listing and asks the host to pack the module's bytes. A directory
// source is vendored whole through a tree pack rooted at the source root; a
// single-file source (a lone module file) is packed as one file. The resolution
// is a straight directory-versus-file branch: this provider vendors all module
// files verbatim with no ambiguity halt.
func packTerraformSource(listing *rendercore.SourceListing) (*aggrega.SourceProjection, error) {
	if listing.RootIsDir {
		return aggrega.PackSource("tree", "")
	}
	return aggrega.PackSource("file", "")
}

func main() {}
