// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	rendercore "github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
)

// The loader-free render primitives live in package rendercore so provider
// guests and the per-provider render subpackages depend on them without
// linking the host plugin loader. These aliases keep the render orchestration
// package's own references — and its exported surface — pointing at the single
// definition in rendercore rather than a second copy.

// UnsupportedFieldCode re-exports the stable code carried by every
// unsupported-field diagnostic, so the render orchestration package and its
// display consumers match the class by the single definition in rendercore.
const UnsupportedFieldCode = rendercore.UnsupportedFieldCode

// UnhonorableAnnotationDiagnostic re-exports the constructor for the advisory
// raised when a target's support claim does not cover an authored platform
// annotation, so the render orchestration package builds it from the single
// definition in rendercore and it carries the class code by construction.
var UnhonorableAnnotationDiagnostic = rendercore.UnhonorableAnnotationDiagnostic

// SensitiveByValueCode re-exports the stable code carried by every
// sensitive-by-value advisory, so the render orchestration package and its
// display consumers match the class by the single definition in rendercore.
const SensitiveByValueCode = rendercore.SensitiveByValueCode

// SensitiveByValueDiagnostic re-exports the constructor for the
// sensitive-by-value advisory so the render orchestration package builds it
// from the single definition in rendercore.
var SensitiveByValueDiagnostic = rendercore.SensitiveByValueDiagnostic

// SensitivityNotConveyedCode re-exports the stable code carried by every
// sensitivity-not-conveyed advisory, so the render orchestration package and its
// display consumers match the class by the single definition in rendercore.
const SensitivityNotConveyedCode = rendercore.SensitivityNotConveyedCode

// SensitivityNotConveyedDiagnostic re-exports the constructor for the
// sensitivity-not-conveyed advisory so the render orchestration package builds
// it from the single definition in rendercore.
var SensitivityNotConveyedDiagnostic = rendercore.SensitivityNotConveyedDiagnostic

// UnconveyedValueLeafDeclarations re-exports the one reading of which authored
// sensitivity declarations a node's render cannot convey, so the advisory and
// the marking it is the complement of are decided from one definition.
var UnconveyedValueLeafDeclarations = rendercore.UnconveyedValueLeafDeclarations

// RenderDiagnosticText re-exports the one formatter for a structured render
// diagnostic's user-facing line, so every display surface renders the class
// identically.
var RenderDiagnosticText = rendercore.RenderDiagnosticText

// NodeProjection re-exports the one builder of a render dispatch's node
// projection, so the render orchestration package and the provider engines build
// it from the single definition in rendercore.
var NodeProjection = rendercore.NodeProjection

// SensitiveRenderedKeys re-exports the one join from a node's sensitivity
// declarations to the rendered identities its dispatch produces, so nothing
// outside rendercore recovers a rendered key a second way.
var SensitiveRenderedKeys = rendercore.SensitiveRenderedKeys

// Wire types of the provider-render socket.
type (
	ArtifactProjectionEntry    = rendercore.ArtifactProjectionEntry
	ArtifactsProjection        = rendercore.ArtifactsProjection
	AssembleBoundaryInput      = rendercore.AssembleBoundaryInput
	AssembleBoundaryOutput     = rendercore.AssembleBoundaryOutput
	BoundaryEnvOverridesInput  = rendercore.BoundaryEnvOverridesInput
	BoundaryEnvOverridesOutput = rendercore.BoundaryEnvOverridesOutput
	CarveInput                 = rendercore.CarveInput
	CarveOutput                = rendercore.CarveOutput
	DerivedNameVerdict         = rendercore.DerivedNameVerdict
	NameBoundInput             = rendercore.NameBoundInput
	NameBoundOutput            = rendercore.NameBoundOutput
	IRNodeProjection           = rendercore.IRNodeProjection
	LinkEndpoint               = rendercore.LinkEndpoint
	LinkEdge                   = rendercore.LinkEdge
	LinkPatch                  = rendercore.LinkPatch
	LinkPublishInput           = rendercore.LinkPublishInput
	LinkPublishOutput          = rendercore.LinkPublishOutput
	LinkResolveInput           = rendercore.LinkResolveInput
	LinkResolveOutput          = rendercore.LinkResolveOutput
	NodeProvenance             = rendercore.NodeProvenance
	ReassembleInput            = rendercore.ReassembleInput
	ReassembleOutput           = rendercore.ReassembleOutput
	RenderEdge                 = rendercore.RenderEdge
	RenderNodeInput            = rendercore.RenderNodeInput
	RenderNodeOutput           = rendercore.RenderNodeOutput
	RenderedFile               = rendercore.RenderedFile
	RenderedNameInput          = rendercore.RenderedNameInput
	RenderedNameOutput         = rendercore.RenderedNameOutput
	RenderedNode               = rendercore.RenderedNode
	SourceEntry                = rendercore.SourceEntry
	SourceFile                 = rendercore.SourceFile
	SourceFileRequest          = rendercore.SourceFileRequest
	SourceFileResponse         = rendercore.SourceFileResponse
	SourceListing              = rendercore.SourceListing
	SourcePackRequest          = rendercore.SourcePackRequest
	SourcePackResponse         = rendercore.SourcePackResponse
	SourceProjection           = rendercore.SourceProjection
	SubChart                   = rendercore.SubChart
	ValidateNodeOutput         = rendercore.ValidateNodeOutput
	WorkloadProjection         = rendercore.WorkloadProjection
	EmitOrigin                 = rendercore.EmitOrigin
	EmittedFile                = rendercore.EmittedFile
	Segment                    = rendercore.Segment
	IREdgeInfo                 = rendercore.IREdgeInfo
	NodeRenderContext          = rendercore.NodeRenderContext
	ProvenanceContext          = rendercore.ProvenanceContext
	DeclaredSensitivity        = rendercore.DeclaredSensitivity
)

// Unexported alias so in-package type assertions name the concrete stock codec.
type stockYAMLCodec = rendercore.StockYAMLCodec

// Well-known constants and emit-origin tags.
const (
	EmitOriginSource          = rendercore.EmitOriginSource
	EmitOriginWorkload        = rendercore.EmitOriginWorkload
	EmitOriginArtifactsInline = rendercore.EmitOriginArtifactsInline
	EmitOriginArtifactsImport = rendercore.EmitOriginArtifactsImport
	EmitOriginPipeline        = rendercore.EmitOriginPipeline
	AnnotationNamespace       = rendercore.AnnotationNamespace
)

// Pure placement, provenance, value-flattening, and codec constructors.
var (
	OrderedMapToPlain        = rendercore.OrderedMapToPlain
	PlainValue               = rendercore.PlainValue
	ClampYAMLIndent          = rendercore.ClampYAMLIndent
	MarshalYAMLIndent        = rendercore.MarshalYAMLIndent
	NamespaceFromAnnotations = rendercore.NamespaceFromAnnotations
	ProvenanceComponentID    = rendercore.ProvenanceComponentID
	TargetNameBasis          = rendercore.TargetNameBasis
	// encodeNodeIdentity writes a node identity into the alphabet every authored
	// component name satisfies. Every surface here that publishes a node's identity
	// as .context.instance_id — the pillar a recipe author spends to predict the
	// name a target will give a resource — routes through it, so the identity a
	// template reads and the identity a provider stamps are one string. It is
	// aliased here rather than imported in each of those files so that one spelling
	// reaches all of them and a new publisher has an obvious thing to call.
	//
	// It takes the identity a graph is keyed by, so a caller already holding a
	// published identity must NOT call it again: this encoding is injective, not
	// idempotent, and a second pass writes out the escape the first one emitted.
	encodeNodeIdentity                = labels.EncodeNodeIdentity
	BuildAggregaProvenanceLabels      = rendercore.BuildAggregaProvenanceLabels
	BuildAggregaProvenanceAnnotations = rendercore.BuildAggregaProvenanceAnnotations
	FormatTerraformProvenanceComment  = rendercore.FormatTerraformProvenanceComment
	ProvenanceRecipeURIFromNodeCtx    = rendercore.ProvenanceRecipeURIFromNodeCtx
	SpliceArtifactProvenance          = rendercore.SpliceArtifactProvenance
	NewStockYAMLCodec                 = rendercore.NewStockYAMLCodec
	Split                             = rendercore.Split
	ApplyBack                         = rendercore.ApplyBack

	// newStockYAMLCodec and provenanceSkipWarning forward to the render-core
	// definitions so existing callers keep their unexported spellings.
	newStockYAMLCodec     = rendercore.NewStockYAMLCodec
	provenanceSkipWarning = rendercore.ProvenanceSkipWarning

	// Pure yaml.Node navigation helpers shared with the provenance splice.
	unwrapAlias       = rendercore.UnwrapAlias
	mappingValueByKey = rendercore.MappingValueByKey
	nodeKindName      = rendercore.NodeKindName
)

// ErrPipelinePatchTargetNotStructured keeps its identity for errors.Is
// while its definition lives with the segmenter in rendercore. It restates no
// message: the message is registered once, beside the canonical declaration,
// and a second registration of one identity is refused.
func ErrPipelinePatchTargetNotStructured() error {
	return rendercore.ErrPipelinePatchTargetNotStructured()
}
