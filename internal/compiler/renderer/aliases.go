// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import rendercore "github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"

// The loader-free render primitives live in package rendercore so provider
// guests and the per-provider render subpackages depend on them without
// linking the host plugin loader. These aliases keep the render orchestration
// package's own references — and its exported surface — pointing at the single
// definition in rendercore rather than a second copy.

// UnsupportedFieldCode re-exports the stable code carried by every
// unsupported-field diagnostic, so the render orchestration package and its
// display consumers match the class by the single definition in rendercore.
const UnsupportedFieldCode = rendercore.UnsupportedFieldCode

// SensitiveByValueCode re-exports the stable code carried by every
// sensitive-by-value advisory, so the render orchestration package and its
// display consumers match the class by the single definition in rendercore.
const SensitiveByValueCode = rendercore.SensitiveByValueCode

// SensitiveByValueDiagnostic re-exports the constructor for the
// sensitive-by-value advisory so the render orchestration package builds it
// from the single definition in rendercore.
var SensitiveByValueDiagnostic = rendercore.SensitiveByValueDiagnostic

// RenderDiagnosticText re-exports the one formatter for a structured render
// diagnostic's user-facing line, so every display surface renders the class
// identically.
var RenderDiagnosticText = rendercore.RenderDiagnosticText

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
	OrderedMapToPlain                 = rendercore.OrderedMapToPlain
	PlainValue                        = rendercore.PlainValue
	ClampYAMLIndent                   = rendercore.ClampYAMLIndent
	MarshalYAMLIndent                 = rendercore.MarshalYAMLIndent
	NamespaceFromAnnotations          = rendercore.NamespaceFromAnnotations
	ProvenanceComponentID             = rendercore.ProvenanceComponentID
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
