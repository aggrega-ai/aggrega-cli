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
// A sourced template takes values through the parameters it declares. Where the
// node's values are the output of the Recipe's values mapping, each key binds the
// parameter it names in every template of the source, since the source's templates
// assemble into one stack: a Ref to the parameter becomes the value, an Fn::Sub
// naming it receives it through its variable map, and the parameter leaves every
// template declaring it. A value that is wholly one short-form intrinsic is read as
// that intrinsic, a dynamic reference and a cross-stack placeholder stay whole for
// CloudFormation and the link pass to resolve, and a literal is converted as
// CloudFormation converts it at deploy and checked against the parameter's
// constraints. A key naming no parameter halts, and so does a literal bound to a
// NoEcho parameter or produced by a mapping that read a value declared sensitive; an
// intrinsic is written as given, the text inside it included. Values the node
// carries for any other reason are left unread.
//
// A sourced render then leaves out every resource the node's omit entries name by
// its Type and its logical ID, across every template the source carries, reports
// how many resources each entry left out, and halts where a remaining template
// still names a left-out resource by Ref, Fn::GetAtt, DependsOn or a Fn::Sub
// variable — a template naming a resource it does not declare is one CloudFormation
// refuses.
//
// validate_node_input structurally parses a sourced template's packed files ahead
// of render and reports each parse finding as a diagnostic; an unparsable template
// halts the build loudly, naming the offending file, rather than surfacing as a
// later render failure.
//
// assemble_boundary folds every per-node fragment in the dispatch group into one
// pure-JSON long-form CloudFormation stack template: the fragments' Resources
// merge into one Resources map under the logical ids their Recipes authored, and
// each node's provides entries become stack Outputs named for the contributing
// node and the entry together. A fragment's Parameters, Mappings, Conditions,
// Metadata and Rules are carried in under exactly the names it authored, so a
// resource or output referencing one of those names meets the declaration it
// refers to; two contributors declaring one name with one body merge, and with
// different bodies the assembly halts naming the name and both of them. Where the
// contributing nodes declare more than one deployment wave, the merge also records
// that ordering as DependsOn between the merged resources, so the earlier wave's
// resources are created before the later wave's are begun and deleted after them;
// a group carrying one wave records nothing. All CloudFormation domain logic —
// short-form intrinsic expansion, resource merging, output naming, the
// provides-to-Outputs projection, the section carrying, and the wave ordering —
// lives in the cfncore package the guest calls, so the Wasm host stays agnostic to
// CloudFormation semantics.
//
// A parameter a contributor's binding filled is declared nowhere in that
// contributor's source any more, so the assembly halts where content from elsewhere
// in the stack still names one and no contributor declares the name, naming each
// such contributor, its template and the parameter. It learns which names a binding
// filled from the mark the host carries on each rendered node whose values a
// mapping produced.
//
// The assembled stack's Transform is the union of every contributor's, in
// CloudFormation's documented order, and is refused by name where carrying it would
// change how another contributor's content deploys; SAM's Globals travel with the
// transform that reads them. A template-wide description states something about
// a whole template rather than keying content by names an author chose, so a merge
// across contributors has no defensible answer for it: render_node reports one a
// node authored as a warn-severity diagnostic naming the node and the section, as
// it does any top-level section the stack does not carry, and the assembled bytes
// are the same whether or not anything is listening.
//
// A logical id is unique within one template while several nodes assemble into
// one stack, so render_node returns the names each node's fragments will be
// addressed by — its resource logical ids, and the output logical ids its
// provides and Outputs entries project to — as the derived names it declares.
// For a node that assembles, those are exactly the names the assembled stack
// carries for it: the node's fragments are read as one set, so a name two of
// them restate with the same body is declared once, the way the stack carries
// it. Two fragments claiming one name with different bodies keep the name
// declared twice, because one of the two bodies would be lost and the merge
// refuses the node.
// The host writes those bytes verbatim and reads nothing inside them, so the
// declaration is how the guest asks it to confirm no two nodes claimed one name;
// a duplicate halts the build naming the name and every node that claimed it.
// The host compares the declared strings and learns nothing about what they mean,
// which is what keeps CloudFormation's naming rules here in the guest.
//
// Two refusals happen before any declaration is handed over, and they are here
// rather than in the host for the same reason. An output name is built from the
// node and the entry name by dropping every separator and capitalising each word,
// which can carry two different authored entries to one name; render_node refuses
// that while it still holds both entries, so the halt names what the author wrote
// rather than only the name they landed on. And a fragment writing one mapping key
// twice is refused as the fragment is read, since converting it would keep one of
// the two bodies and drop the other — a resource or output the author wrote that
// the stack would then not carry. Both live in the cfncore package with the rest
// of CloudFormation's naming rules.
//
// Build provenance lands on each resource's Metadata attribute, under one
// reserved-prefix key, on both input paths — the resources a sourced template
// declares and the resources an artifact fragment contributes carry the same set
// from the same source. The Metadata attribute is chosen because the template
// language grants it to every resource whatever its type, so one placement rule
// reaches every resource; a resource tag is a per-type property, absent from many
// types and shaped differently across the types that have it, and a template
// naming a property its type does not declare is refused whole at deploy time. A
// resource's Metadata is readable back from the deployed stack, so provenance
// stays reachable from the stack and not only from the file. CloudFormation output
// is JSON and carries no comment syntax, so nothing rides as a comment. A template
// declaring no resource has no site for provenance: it is emitted unchanged and
// named in a warn-severity advisory, so unstamped and unreported never happen
// together.
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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// descriptor is the describe() self-report: a v1alpha1 provider plugin compatible
// with the cloudformation provider, declaring the render_node,
// validate_node_input, assemble_boundary, link_publish, and link_resolve exports
// and its render-behavior contract (sourced and artifacts-standalone input,
// boundary-merge assembly, cross-boundary linking, and its honouring of omit
// entries). The host reads it once at
// load and validates it before invoking any export. CloudFormation resolves a
// sourced template from the host's name-only listing and asks the host to pack the
// selection, so render_node and validate_node_input declare the aggrega_pack_source
// host function they import; the host cross-checks this declaration against the
// functions the guest actually imports. The cross-boundary-linking capability is
// declared with both link exports as the paired contract the load gate's coherence
// check requires.
//
// aggrega.MarshalDescriptor folds the build-provenance version and commit into
// the metadata when the Go linker stamps them at build time; a plain build
// leaves them unset, so an unstamped build's descriptor is byte-identical to a
// stamped build's minus the provenance.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "provider",
		Capabilities: []string{"cloudformation"},
		Exports: []aggrega.PluginExport{
			{
				Name:          "render_node",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "normalizes a CloudFormation fragment or a sourced CloudFormation template to pure-JSON long-form intrinsics",
			},
			{
				Name:          "validate_node_input",
				InputPattern:  "b",
				HostFunctions: []string{"aggrega_pack_source"},
				Stability:     "experimental",
				Description:   "structurally parses the sourced CloudFormation template and reports parse errors before render",
			},
			{
				Name:         "assemble_boundary",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "merges the per-node CloudFormation fragments at one dispatch group into a single stack template, ordering the merged resources by the declared deployment wave",
			},
			{
				Name:         "link_publish",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "publishes a consumed endpoint's cross-boundary reference identity and producer-side wiring",
			},
			{
				Name:         "link_resolve",
				InputPattern: "b",
				Stability:    "experimental",
				Description:  "resolves a consuming endpoint's cross-boundary reference against a published identity",
			},
		},
		Contract: &aggrega.ProviderContract{
			InputKinds:           aggrega.InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: true},
			BoundaryAssembly:     "merge",
			ReusableInstance:     true,
			CrossBoundaryLinking: true,
			// A sourced render leaves out every resource an omit entry names by its
			// Type and its logical ID, reports how many resources each entry left
			// out, and refuses a template in which what remains still names one.
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
// how a failure crosses and this stays about what cloudformation renders.
func renderNodeOutput() (rendercore.RenderNodeOutput, error) {
	var in rendercore.RenderNodeInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	var out rendercore.RenderNodeOutput
	var resourceIDs, outputIDs []string
	switch {
	case in.WorkloadProjection != nil:
		// CloudFormation does not synthesize workloads. A workload projection is
		// an orchestrator dispatch error rather than authoring drift, so it halts
		// loudly.
		return rendercore.RenderNodeOutput{}, errors.New(
			"cloudformation-provider: render_node received a workload projection; the cloudformation provider does not synthesize workloads")
	case in.ArtifactsProjection != nil:
		// Every fragment is normalized first and the whole set is declared at
		// once. The declaration speaks for the node, and what the assembled stack
		// carries for a node is a property of all its fragments together: two of
		// them restating one name with the same body reach the stack once, which
		// only a reader holding every fragment can see.
		fragments := make([][]byte, 0, len(in.ArtifactsProjection.Entries))
		for _, ent := range in.ArtifactsProjection.Entries {
			normalized, err := cfncore.NormalizeFragment(ent.Content)
			if err != nil {
				return rendercore.RenderNodeOutput{}, err
			}
			// The entry's own provenance halves are read rather than the node
			// context's, which is what keeps an attachment-carried artifact stamping
			// the identity the host computed for that entry: recomputing from the
			// node would substitute the consuming Recipe's identity for it.
			//
			// Stamped before the names are declared and the sections read, so every
			// reader downstream holds the bytes the assembled stack will carry. The
			// alternative leaves the declaration speaking for one set of bodies and
			// the merge writing another.
			stamped, warning, err := cfncore.StampResourceProvenance(
				ent.Name, normalized, cfncore.ProvenanceSet(ent.Labels, ent.Annotations))
			if err != nil {
				return rendercore.RenderNodeOutput{}, err
			}
			if warning != "" {
				out.Diagnostics = append(out.Diagnostics, rendercore.ArtifactProvenanceSkippedDiagnostic(warning))
			}
			fragments = append(fragments, stamped)
			out.Files = append(out.Files, rendercore.RenderedFile{Path: ent.Name, Content: stamped})
		}
		// Declared under the identity this node's names are BUILT from, which is what
		// the assembly will build them from too. The node id alone is that identity for
		// an ordinary component and for an attachment rendered against its host, and
		// is not for a contributor whose id is a dispatch marker: declaring under the
		// id there would offer the author names the assembled stack never carries,
		// while the names it does carry would be declared by nobody.
		declaredRes, declaredOut, err := cfncore.DeclaredNames(
			cfncore.ContributorNameIdentity(cfncore.NodeFragment{NodeID: in.Node.ID, NameBasis: in.Node.DerivedNameBasis}),
			fragments)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		resourceIDs, outputIDs = declaredRes, declaredOut
		// A section whose content is one statement about the whole template has no
		// defensible merge across the contributors a dispatch group folds together,
		// so the assembled stack carries neither and the author is told here, where
		// the node that wrote it can be named. Out of band: the emitted bytes are
		// the same whether or not anything is listening.
		notices, err := cfncore.SectionNotices(fragments)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		out.Diagnostics = append(out.Diagnostics, notices...)
	case in.SourceListing != nil:
		// Sourced path: normalize the acquired CloudFormation template to pure-JSON
		// long-form intrinsics. The host has read no content; the guest asks it to
		// pack the selection and normalizes each template in memory, so no YAML
		// short-form tag survives and every intrinsic carries through as its
		// long-form pointer object, never resolved. A directory source vendors its
		// non-template files verbatim.
		proj, err := packSource(in.SourceListing)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		normalized, err := cfncore.NormalizeSourced(sourcedFiles(proj), in.SourceListing.RootIsDir)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		// The template's declared parameters take the values the Recipe's values
		// mapping produced, before anything below reads the templates, so the omit
		// entries' reference check, the provenance stamp and the declared names all
		// read the bound templates. Values a mapping did not produce are left unread.
		normalized, err = cfncore.BindSourced(normalized, in.SourceListing.RootIsDir, cfncore.BindInput{
			Values:          in.Node.Values,
			Mapped:          in.Node.ValuesMapped,
			SensitiveValues: in.Node.SensitiveValues,
		}, in.Node.ID)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		nodeCtx := nodeCtxFromInput(in)
		// The resources the node's omit entries name leave every template here,
		// before anything below reads them, so a left-out resource is neither
		// stamped nor declared. The counts go back on the output — one per entry the
		// input carried, and none when it carried none.
		normalized, omitted, err := cfncore.LeaveOutSourced(normalized, in.SourceListing.RootIsDir, nodeCtx.Omits, nodeCtx.NodeID)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		out.Omitted = omitted
		// The same stamp the fragment path applies, over the templates a sourced
		// selection carries. The payload carries no per-entry provenance halves here,
		// so the set is derived from the node's own render context — through the same
		// shared builders, so a sourced resource and an artifact-contributed one carry
		// the same keys with the same values for one node.
		normalized, provWarnings, err := cfncore.StampSourcedProvenance(
			normalized, in.SourceListing.RootIsDir, cfncore.ProvenanceSetFromNodeContext(nodeCtx))
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		for _, warning := range provWarnings {
			out.Diagnostics = append(out.Diagnostics, rendercore.ArtifactProvenanceSkippedDiagnostic(warning))
		}
		// The same identity the fragment path declares under, for the same reason: the
		// assembly builds this node's names from it whichever input kind produced the
		// templates.
		resourceIDs, outputIDs, err = cfncore.DeclaredNamesSourced(
			cfncore.ContributorNameIdentity(cfncore.NodeFragment{NodeID: in.Node.ID, NameBasis: in.Node.DerivedNameBasis}),
			normalized, in.SourceListing.RootIsDir)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		// The same out-of-band report the fragment path makes, over the templates a
		// sourced selection carries.
		notices, err := cfncore.SectionNoticesSourced(normalized, in.SourceListing.RootIsDir)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		out.Diagnostics = append(out.Diagnostics, notices...)
		out.Files = make([]rendercore.RenderedFile, 0, len(normalized))
		for _, f := range normalized {
			out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Path, Content: f.Content})
		}
	}
	// A payload with no projection yields an empty output.

	// The names this node's output addresses its content by, declared per kind so
	// the host can confirm no two nodes assembling into one stack claim the same
	// one. A logical id is unique within a template, and the host writes the file
	// bytes verbatim without reading them, so the declaration is how the guest
	// asks for a check the host could not otherwise make.
	if len(resourceIDs) > 0 {
		out.DerivedKeys.Set(cfncore.KeySpaceResourceLogicalIDs, resourceIDs)
	}
	if len(outputIDs) > 0 {
		out.DerivedKeys.Set(cfncore.KeySpaceOutputLogicalIDs, outputIDs)
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
	// Only the sourced template is a structural input to validate. An artifacts
	// projection is host-template-evaluated CloudFormation whose parse is checked
	// inline in render_node against the emitted bytes, and a workload projection is
	// a dispatch error render_node rejects — so a payload with no source listing
	// yields no diagnostics (a valid, empty check).
	if in.SourceListing != nil {
		proj, err := packSource(in.SourceListing)
		if err != nil {
			return rendercore.ValidateNodeOutput{}, err
		}
		out.Diagnostics = cfncore.ValidateSourced(sourcedFiles(proj), in.SourceListing.RootIsDir)
	}
	return out, nil
}

// nodeCtxFromInput reconstructs the render context from the wire input's node
// projection — the identity and build provenance every emitted resource is
// stamped with (component-id, provider identity, boundary path, deployment wave,
// Recipe identity, the Recipe's own output labels, and build identity).
//
// It exists for the sourced path, where the payload carries the node's provenance
// rather than the per-entry halves an artifacts projection computes per entry.
// CloudFormation reads no values, annotations or source through this context — a
// sourced template's parameters read the node's values from the input itself: it
// carries what the shared provenance builders read, so the set a sourced template
// is stamped with is the set the host computes for the same node, and the omit
// entries the input carries, so a sourced render leaves out what they name; only a
// source render's input carries any. A marked entry is rebuilt with every field
// withheld, and the render core quotes no withheld field in a refusal, so no field
// of a marked entry reaches a text this guest returns.
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
// dispatch group's fragments into one stack template, returning the error rather
// than reporting it for the reason renderNodeOutput does.
func assembleBoundaryOutput() (rendercore.AssembleBoundaryOutput, error) {
	var in rendercore.AssembleBoundaryInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	nodes := make([]cfncore.NodeFragment, 0, len(in.RenderedNodes))
	for i := range in.RenderedNodes {
		rn := in.RenderedNodes[i]
		files := make([][]byte, 0, len(rn.Rendered.Files))
		paths := make([]string, 0, len(rn.Rendered.Files))
		for _, f := range rn.Rendered.Files {
			files = append(files, f.Content)
			paths = append(paths, f.Path)
		}
		// A node whose values a mapping produced had every key bound to a parameter
		// of its source by its own render, or that render halted; so its keys are the
		// parameters the binding removed from the source.
		var bound []string
		if rn.ValuesMapped {
			bound = rn.Values.Keys()
		}
		nodes = append(nodes, cfncore.NodeFragment{
			NodeID: rn.ID, NameBasis: rn.DerivedNameBasis, Files: files, Paths: paths, SyncWave: rn.SyncWave, Bound: bound,
		})
	}

	stack, err := cfncore.AssembleStack(nodes)
	if err != nil {
		return rendercore.AssembleBoundaryOutput{}, err
	}

	out := rendercore.AssembleBoundaryOutput{
		// The assembled stack template is pure-JSON long-form throughout, so the
		// merged file is JSON. Declaring the format lets the host route it to the
		// JSON parser without inferring from the extension.
		Files: []rendercore.RenderedFile{{Path: cfncore.StackTemplateName, Content: stack, Format: "json"}},
	}
	return out, nil
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

// linkPublishOutput decodes the link_publish input and produces the producer-side
// wiring, returning the error rather than reporting it for the reason
// renderNodeOutput does.
func linkPublishOutput() (rendercore.LinkPublishOutput, error) {
	var in rendercore.LinkPublishInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	return cfncore.LinkPublish(in.Edge, in.ProducerFiles)
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

// linkResolveOutput decodes the link_resolve input and produces the consumer-side
// wiring, returning the error rather than reporting it for the reason
// renderNodeOutput does.
func linkResolveOutput() (rendercore.LinkResolveOutput, error) {
	var in rendercore.LinkResolveInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	return cfncore.LinkResolve(in.Edge, in.Handle, in.ConsumerFiles)
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
