// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "encoding/json"

// API version constant for IR documents validated against ir-v1alpha1.schema.json.
const IRAPIVersion = "aggrega.ai/v1alpha1"

// IRKind is the canonical kind value for IR documents.
const IRKind = "IR"

// AttachmentOrigin is the provenance of a Feature attachment (or of one leaf in
// its values/infra payload): whether it was authored explicitly, contributed by
// the annotation-driven expansion pass, or — at the attachment top level — a mix
// of both. It is a distinct string type so the wire shape is unchanged (a plain
// JSON string) while the value set is single-sourced here rather than re-spelled
// as string literals across the IR builder. The ir-v1alpha1
// IRFeatureEntry.origin enum is the cross-language contract for the same
// value set.
type AttachmentOrigin string

const (
	// OriginExplicit marks a Feature attachment or leaf authored in an explicit
	// features: block.
	OriginExplicit AttachmentOrigin = "explicit"
	// OriginAnnotation marks a Feature attachment or leaf contributed by the
	// annotation-driven expansion pass.
	OriginAnnotation AttachmentOrigin = "annotation"
	// OriginMixed marks an attachment whose leaves carry a mix of explicit and
	// annotation provenance; per-leaf detail then lives in the provenance maps.
	// Reachable only at the attachment top level, never on a single leaf's
	// FieldProvenance.
	OriginMixed AttachmentOrigin = "mixed"
)

const (
	// SocketDeclarationDeclared is the [IREdge.SocketDeclaration] value for an edge
	// wired into a socket the owning catalog entity lists in its usesSchema — the
	// consuming Component, or the originating Feature for a Feature-authored edge.
	// Such a socket carries its usesSchema entry, including the selector that
	// constrains which targets the edge may reach.
	SocketDeclarationDeclared = "declared"
	// SocketDeclarationAdHoc is the [IREdge.SocketDeclaration] value for an edge
	// wired into a socket the Aggregate author named and the catalog did not
	// declare. There is no usesSchema entry, so there is no selector to evaluate
	// and no target constraint; what the edge may carry is bounded by the target's
	// declared provides alone.
	SocketDeclarationAdHoc = "ad-hoc"
)

// AttachmentBindingFree is the value [IRFeatureEntry.Binding] carries when an
// attachment fills no declared attachment slot — a free attachment. It is
// spelled once here so the IR builder, a consumer comparing
// against it, and the ir-v1alpha1 contract that documents it cannot diverge on
// the literal.
//
// A slot whose declared name is this same string is indistinguishable from a
// free attachment on the wire; the value set is a slot name OR this sentinel,
// and nothing reserves the sentinel against the slot namespace.
const AttachmentBindingFree = "free"

// IRGraph is the in-memory, provider-agnostic intermediate representation emitted by the
// topology builder. It strictly mirrors ir-v1alpha1.schema.json.
type IRGraph struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   IRMetadata `json:"metadata"`
	Spec       IRSpec     `json:"spec"`
}

// IRMetadata carries document-level metadata for an IR graph.
type IRMetadata struct {
	AggregateName string `json:"aggregate_name"`
}

// IRSpec is the spec subtree of an IR document, carrying the seven
// data classes that downstream stages consume:
//
//   - Nodes / Edges — the topological graph (nodes keyed by node ID,
//     edges as a slice of directed source/target pairs).
//   - ValuesMapping / InfraMapping — per-node opaque developer payloads
//     keyed by node ID, carried as json.RawMessage so the renderer
//     hands them through to provider engines without re-marshalling.
//   - FeatureValuesMapping — the per-node attachment map (node ID →
//     instance name → IRFeatureEntry) for the per-node attachment
//     cascade. ONE bucket carries every attachment a node holds,
//     whatever it produces: an operational Feature, a credential
//     Feature, an identity Feature. Instance names are unique per node,
//     so a same-named attachment arriving from two cascade tiers with
//     disagreeing types halts rather than merging.
//   - Provenance — compiler / aggregate / release / manifest version
//     traceability for the IR build.
//
// Schema: ir-v1alpha1.schema.json#/properties/spec
type IRSpec struct {
	Nodes                OrderedMap[IRNode]                     `json:"nodes"`
	Edges                []IREdge                               `json:"edges"`
	ValuesMapping        OrderedMap[json.RawMessage]            `json:"values_mapping"`
	InfraMapping         OrderedMap[json.RawMessage]            `json:"infra_mapping"`
	FeatureValuesMapping OrderedMap[OrderedMap[IRFeatureEntry]] `json:"feature_values_mapping"`
	// BoundaryFeatures is the un-folded projection of each Release boundary's
	// Feature attachments, keyed boundary-path -> instance-name. It mirrors
	// release.Spec.Boundaries[*].Features as the single source both lock-time
	// source-descent and the render-time boundary-FR dispatch read, so neither
	// re-walks the Release. omitempty: only graphs with boundary-level Feature
	// attachments carry it, so non-boundary-FR fixtures do not churn IR goldens.
	BoundaryFeatures OrderedMap[OrderedMap[IRBoundaryFeature]] `json:"boundary_features,omitempty,omitzero"`
	Provenance       IRProvenance                              `json:"provenance"`
}

// IRBoundaryFeature is one Release boundary-level Feature attachment projected
// into the IR — the type URI plus the boundary infra. Boundary-level attachments
// carry no values (the Release pillar-split forbids values: at boundary level), so
// only Type and Infra are projected; Type resolves to the FeatureRecipe (source /
// locals / provides) and Infra is the boundary infra a boundary-scope FR renders
// against.
//
// The absent values field is deliberate and is not an asymmetry to complete: the
// Release schema bans a values: block, and the attachment cascade honours that by
// construction — a Release-side tier contributes Infra with an empty values map,
// an Aggregate-side tier contributes Values with an empty infra map. Adding a
// Values field here would create the very surface the ban exists to deny. Where
// both pillars DO belong on one attachment shape is the per-node render
// projection (TemplateAttachment), which carries values, infra and provides
// together because an Aggregate-side and a Release-side contribution merge into
// one entry there.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/boundary_features
type IRBoundaryFeature struct {
	Type  string          `json:"type"`
	Infra OrderedMap[any] `json:"infra,omitempty,omitzero"`
	// SensitivePayloadLeaves names the leaves this boundary attachment's
	// infra payload carries that the attached Feature declares sensitive,
	// each as the pillar-prefixed dotted path the datum occupies. Only
	// "infra.<path>" coordinates arise here, because the shape carries no
	// values payload — which is the ban above expressed in the marking
	// rather than a narrowing of it.
	//
	// The subject is the payload Infra ends up carrying rather than the
	// block it was built from, so a leaf standing at a value the Feature's
	// schema declares as a default is named alongside one a boundary wrote.
	// A filled default is a datum the payload carries, and a marking read
	// off the written block alone would leave it unnamed.
	//
	// A leaf is named where a declaration COVERS it, on the same reading
	// every consumer of the axis holds: the Feature's schema declares that
	// leaf, or declares an object containing it. Matching is segment-wise
	// over the dotted path, nothing is read out of the path's own text, and
	// no value is inspected.
	//
	// Ordered as Infra holds the leaves, so the wire bytes are identical
	// across runs of identical input.
	//
	// Empty when the attachment carries no leaf its Feature declares
	// sensitive, and equally when the Feature resolves to no schema this
	// reader could read. Empty is not permission: it says the marking found
	// nothing at this unit, never that the attachment's infra carries
	// nothing sensitive.
	SensitivePayloadLeaves []string `json:"sensitive_payload_leaves,omitempty"`

	// Realization names the FeatureRecipe the Manifest binds to this boundary
	// attachment, as "<name>:<version>", by the attachment's own name and then
	// by its Feature type. It is [IRFeatureEntry.Realization] at boundary
	// scope: one field, recorded from the same lookup, read the same way.
	// Empty when the Manifest binds the attachment to no FeatureRecipe the
	// catalog holds.
	Realization string `json:"realization,omitempty"`
}

// IRNode is a single node in the IR graph. Purely topological — no opaque payload.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/nodes
type IRNode struct {
	Description     string             `json:"description,omitempty"`
	Labels          OrderedMap[string] `json:"labels"`
	Component       string             `json:"component"`
	ResolvedVersion string             `json:"resolved_version"`
	SyncWave        int                `json:"sync_wave,omitempty"`
	BoundaryPath    string             `json:"boundary_path,omitempty"`
	SubsystemPath   string             `json:"subsystem_path,omitempty"`
	// SynthesizedSubsystem is true when SubsystemPath names the compiler-
	// synthesized `default` subsystem — created for an Aggregate that declares
	// top-level components rather than named subsystems. Derived provenance,
	// never authored; distinguishes the synthesized default from a subsystem a
	// user explicitly named "default". Independent of SynthesizedBoundary.
	// Omitted (false) for authored subsystems.
	SynthesizedSubsystem bool `json:"synthesized_subsystem,omitempty"`
	// SynthesizedBoundary is true when BoundaryPath names the compiler-
	// synthesized `default` boundary — created for a Release that declares
	// top-level components rather than named boundaries. Derived provenance,
	// never authored; distinguishes the synthesized default from a boundary a
	// user explicitly named "default". Omitted (false) for authored boundaries.
	SynthesizedBoundary bool             `json:"synthesized_boundary,omitempty"`
	CatalogMetadata     *CatalogMetadata `json:"catalog_metadata,omitempty"`
	// CatalogAnnotations, ReleaseAnnotations, and AggregateAnnotations
	// carry the renderer-facing Scalar projection of the post-cascade
	// annotation set for this IR node, partitioned by authoring side.
	// Only scalar annotation values surface in these maps; structured
	// annotation values participate in the cascade but flow through
	// Feature expansion rather than template substitution.
	//
	// Render-context template substitution sites (`.context.annotations.*`,
	// `.parent_boundary.annotations`) consume these maps directly.
	// Consumers needing the full AnnotationValue shape — per-subkey
	// routing, structured-value attribution, future programmatic
	// access to the structured payload — read fresh from the
	// authoring types (Release.Spec.Boundaries.<...>.Annotations,
	// Aggregate.Spec.Subsystems.<...>.Annotations, etc.) rather than
	// from these fields. The annotation-expansion phase does exactly
	// that.
	CatalogAnnotations   OrderedMap[string] `json:"catalog_annotations,omitempty,omitzero"`
	ReleaseAnnotations   OrderedMap[string] `json:"release_annotations,omitempty,omitzero"`
	AggregateAnnotations OrderedMap[string] `json:"aggregate_annotations,omitempty,omitzero"`

	SelfInject *InjectBlock `json:"self_inject,omitempty"` // Component-level self-inject data for self-inject resolution

	// SourceFile is the workspace-relative path of the Aggregate file
	// where this node was authored. Root-tier nodes carry the root
	// Aggregate's relative path (the caller renders it via
	// catalog.RelativeToWorkspace before constructing pipeline.Inputs);
	// sub-aggregate nodes carry the relative path the catalog accessor
	// returned (the catalog applies the same normalization using the
	// workspace root it was constructed with, the first argument to
	// Load/LoadAll). The single coordinate system
	// keeps the IR wire shape byte-identical across developer machines
	// whose workspaces live at different absolute paths. Empty when no
	// workspace was wired or topology cannot resolve a source file
	// (synthetic test IR, loaders returning empty paths). Consumers
	// that filter diagnostics per file (the daemon's
	// analyzer-error-to-diagnostic conversion) render their comparison
	// path through the same catalog.RelativeToWorkspace helper and drop
	// entries whose SourceFile points to a different file from the
	// validated one.
	SourceFile string `json:"source_file,omitempty"`

	// Uses is the node's `uses` block exactly as authored, in the order the
	// author declared each socket and each entry within it.
	//
	// It is a DIFFERENT fact from [IRSpec.Edges], not a second copy of one, and
	// the difference is the reason it is carried: an edge exists only where an
	// entry resolved to a target, so a socket the author populated but that
	// produced no edge is invisible in Edges and visible here. A reader
	// distinguishing "the author filled this socket" from "this socket reaches a
	// target" needs both, and can derive neither from the other.
	//
	// Empty for a node that authored no `uses` block, and omitted from the wire
	// in that case.
	//
	// One part of the block is not carried as authored: an attachment a
	// connection carries keeps its type and description here and leaves its
	// values out. The attachment itself is one of the node's attachments, under
	// [IRSpec.FeatureValuesMapping], where its values are carried with the
	// per-leaf sensitivity marking a surface displaying the graph withholds by.
	// A copy of the same values here would sit outside that marking, so a
	// declared-sensitive value would reach every reader of the graph in the
	// clear.
	Uses OrderedMap[UsesSocketValue] `json:"uses,omitempty,omitzero"`
	// SensitivePayloadLeaves names the leaves this node's values and infra
	// payload carries that its Component declares sensitive, each as the
	// pillar-prefixed dotted path the datum occupies — "values.<path>" or
	// "infra.<path>". Its subject is the node's OWN data: the payloads
	// [IRSpec.ValuesMapping] and [IRSpec.InfraMapping] carry under this
	// node's id.
	//
	// It is a different fact from the same-named field on
	// [IRFeatureEntry], not a finer or coarser view of one. That field
	// reads what an attachment carries against its Feature's schema; this
	// one reads what the node carries against its Component's. The two
	// schemas are declared by different entities, so neither answer is
	// recoverable from the other, and a node that attaches nothing still
	// carries values.
	//
	// The subject is the payload those two mappings carry rather than the
	// blocks they were built from, so a leaf standing at a value the
	// Component's schema declares as a default is named alongside one a
	// Release wrote. A filled default is a datum the graph carries, and
	// a marking read off the written blocks alone would leave it unnamed.
	//
	// A leaf is named where a declaration COVERS it, on the same reading
	// every consumer of the axis holds: the Component's schema declares
	// that leaf, or declares an object containing it. Matching is
	// segment-wise over the dotted path, nothing is read out of the path's
	// own text, and no value is inspected.
	//
	// Ordered as the payloads hold the leaves, the values pillar's before
	// the infra pillar's — the same order the payloads themselves carry,
	// and what makes the wire bytes identical across runs of identical
	// input.
	//
	// Empty when the node carries no leaf its Component declares sensitive,
	// and equally when the Component resolves to no schema this reader
	// could read. Empty is not permission: it says the marking found
	// nothing at this unit, never that the node's payloads carry nothing
	// sensitive, and a consumer treating it as licence to display or emit a
	// value in the clear has read it backwards.
	SensitivePayloadLeaves []string `json:"sensitive_payload_leaves,omitempty"`
}

// CatalogMetadata is the Enterprise Metadata Suite extracted from a catalog entity.
// Schema: ir-v1alpha1.schema.json node catalog_metadata
type CatalogMetadata struct {
	Maintainers []Maintainer `json:"maintainers,omitempty"`
	License     string       `json:"license,omitempty"`
	Repository  string       `json:"repository,omitempty"`
	Home        string       `json:"home,omitempty"`
	Keywords    []string     `json:"keywords,omitempty"`
}

// IREdge is a directed logical relationship between IR nodes.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/edges/items
type IREdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Socket string `json:"socket,omitempty"`
	Alias  string `json:"alias,omitempty"`
	// SocketDeclaration records where the socket came from:
	// [SocketDeclarationDeclared] when the catalog entity that owns the socket
	// lists it in usesSchema, [SocketDeclarationAdHoc] when the Aggregate author
	// named a socket the catalog did not declare.
	//
	// It is a THIRD axis, independent of the two authorship fields below. Those
	// say who drew the edge; this says where the socket came from. A
	// Release-authored edge into a declared socket and an Aggregate-authored edge
	// into an ad-hoc socket differ on the two axes independently, so this fact is
	// never derivable from OriginFeature or OriginRelease — nor from Socket being
	// empty, since an ad-hoc edge has a socket NAME, it just has no usesSchema
	// entry behind it.
	//
	// The value is always serialised, never omitted. A consumer must never have to
	// read an absent value as "declared": that would make a construction path that
	// forgets to populate the field indistinguishable from a constrained edge, and
	// the whole point of the field is to make an unconstrained edge legible. The
	// schema declares it required with a two-value enum, so an unpopulated field
	// fails conformance rather than passing as declared.
	//
	// Not the same mechanic as an attachment's binding marker. Same shape of
	// question — did the catalog declare this? — on a different subject, with a
	// different construction site. They are deliberately separate fields and must
	// not be collapsed into a shared enum.
	SocketDeclaration string `json:"socket_declaration"`
	// OriginFeature is the catalog Feature type URI when the edge originated
	// from a Feature's own uses block (empty for Component-own edges).
	OriginFeature string `json:"origin_feature,omitempty"`
	// OriginFeatureInstance is the per-attachment instance name of that
	// Feature. It locates a boundary-scope FeatureRecipe's resolved provides
	// by instance when a consumer's uses edge references them, disambiguating
	// two attachments of the same Feature type on one boundary.
	OriginFeatureInstance string `json:"origin_feature_instance,omitempty"`
	// OriginRelease is true when this edge was created by a Release wiring a
	// declared-but-unbound socket to an existing component. It marks edge
	// AUTHORSHIP, not configuration: a developer- or Feature-declared edge that
	// a Release merely supplied infra for keeps its origin (OriginRelease false)
	// and carries the Release infra in Infra. OriginFeature set => Feature;
	// OriginRelease true => Release; neither => Aggregate.
	OriginRelease bool `json:"origin_release,omitempty"`
	// Values and Infra carry the resolved per-connection config for this edge,
	// one per authoring surface: Values comes from the Aggregate's own uses
	// entry, Infra from a Release edge entry. Both are validated against the
	// per-edge contract the CONSUMING component — the edge's source — declares
	// on the socket (valueSchema for Values, infraSchema for Infra), and both
	// carry that contract's declared defaults for keys the author omitted (an
	// authored value wins). Both project into the consuming node's render
	// context at .context.uses.<alias>.values / .context.uses.<alias>.infra
	// (the uses map is alias-keyed). Opaque to the core.
	Values OrderedMap[any]                `json:"values,omitempty,omitzero"`
	Infra  OrderedMap[any]                `json:"infra,omitempty,omitzero"`
	Inject OrderedMap[OrderedMap[string]] `json:"inject,omitempty,omitzero"`
}

// IRFeatureEntry is one attachment on a node in the feature_values_mapping.
// Schema: ir-v1alpha1.schema.json feature_values_mapping entry
//
// Every attachment family carries this shape, so no family gets a lossier
// projection than another: a credential Feature reads .infra and per-leaf
// provenance exactly as an operational Feature does. Type and ResolvedVersion
// are always serialised; ResolvedVersion is the ATTACHMENT's own resolved
// catalog version, not the host component's. What a credential attachment's
// Values carries is the author's choice, not a property of this shape: a
// by-reference credential carries a pointer into a secret store, and a
// by-value one carries the material itself. The compiler resolves neither —
// it carries whichever the author wrote, verbatim.
//
// Labels, CatalogAnnotations and Binding are what let a consumer judge an
// attachment for itself rather than reading a judgement the compiler made.
// Labels and CatalogAnnotations are the attached Feature's own catalog labels
// and annotations, both carried verbatim; Binding says whether the attachment
// fills a declared slot. None of the three is interpreted here: the compiler
// evaluates no label, reads no annotation and branches on no binding value.
//
// Origin distinguishes Feature attachments authored in an explicit
// features: block on the Aggregate, Subsystem, Boundary, or
// BoundaryComponent (Origin = "explicit") from synthetic attachments
// produced by the annotation-driven expansion pass (Origin =
// "annotation"). When values-pillar contributions and infra-pillar
// contributions on the same (Component, Feature) pair come from
// different authoring sides — or when explicit and annotation
// contributions land on the same attachment at different leaves —
// Origin widens to "mixed" and per-leaf detail lives in the two
// provenance maps below. For annotation-driven attachments,
// AnnotationKey carries the annotation name that triggered the
// expansion and AnnotationSource locates the source declaration as
// <file-path>:<dotted-yaml-key-path>, or as the key path alone where the
// compilation named no file for that document. The top-level provenance
// fields populate to the singular value when every leaf shares the
// same FieldProvenance shape; when leaves diverge, the top-level
// fields stay empty and consumers must read ValuesProvenance /
// InfraProvenance per leaf. JSON tags `omitempty` keep the wire
// shape backwards-compatible — pre-provenance fixtures and golden
// files re-marshal without spurious empty-field entries.
//
// ValuesProvenance and InfraProvenance carry per-leaf FieldProvenance
// keyed by the flat-dotted leaf path from the pillar root — the whole
// chain, so a nested leaf is keyed by its full path and not by any one
// segment of it. That key set is NOT the key set of the Values / Infra
// maps themselves: a pillar is a tree, so its own keys name only the
// shallowest leaves. A consumer joins the two by flattening the pillar
// first, which is what the IR builder and the inspect renderer both do
// through FlattenLeaves, and what makes the two sequences align by
// construction. The maps populate at IR-build time during the per-
// pillar per-leaf merge; downstream consumers (inspect, lint) read
// them to surface per-leaf origin markers and to warn when an explicit
// contribution suppressed an annotation one.
//
// Sensitive, SensitiveInject and SensitivePayloadLeaves are derived,
// not authored. All three are computed at IR-build time from
// declarations alone — the attached Feature's schema and the
// attachment-slot declaration the attachment fills, on the host
// Component or on the socket of the connection it was written on — over
// the merged attachment, after the whole attachment cascade has folded.
// None of them inspects a value, and none infers meaning from a name: the
// slot name locates the slot through an exact lookup against the
// declared slot map, and nothing is read out of the name itself. All
// three are absent from the wire when the declarations
// that would populate them are absent, so an attachment whose Feature
// and slot declare nothing carries the same bytes it carried before
// the axis existed.
//
// The three sit at three different units — one whole attachment, one
// inject delivery key, one payload leaf — and each answers its own
// question rather than sharpening the answer above it. None is
// recoverable from either of the others, so a consumer reads the one
// whose unit matches the question it is asking.
type IRFeatureEntry struct {
	Type            string `json:"type"`
	ResolvedVersion string `json:"resolved_version"`
	// Labels carries the attached Feature's catalog labels verbatim, in the
	// order its catalog entity declares them. Verbatim is the contract: the
	// labels are neither filtered, normalised nor prefix-stripped on the way
	// here, so a consumer reads exactly what the catalog author wrote rather
	// than a compiler-shaped view of it. The compiler reads none of them.
	//
	// Empty when the attached Feature declares no labels, and when its type
	// resolves to no catalog entity — the same condition that leaves
	// ResolvedVersion empty.
	Labels OrderedMap[string] `json:"labels,omitempty,omitzero"`
	// CatalogAnnotations carries the attached Feature's catalog annotations
	// verbatim, in the order its catalog entity declares them. Verbatim is the
	// contract, on the same terms as Labels: the annotations are neither
	// filtered, normalised nor prefix-stripped on the way here, so a consumer
	// reads exactly what the catalog author wrote rather than a compiler-shaped
	// view of it. The compiler reads none of them.
	//
	// The subject is the attached Feature entity's own metadata.annotations.
	// That is a different subject from AnnotationKey and AnnotationSource
	// below, which name and locate the annotation that caused this attachment
	// to exist at all: those two describe how the attachment came to be, this
	// one describes the entity the attachment names. The two are independent —
	// an attachment written in a features: block carries no AnnotationKey and
	// still carries every annotation its Feature declares, and an
	// annotation-expanded attachment carries both, about different
	// declarations.
	//
	// The name deliberately matches IRNode.CatalogAnnotations, which carries the
	// same concept — a catalog entity's own metadata.annotations, verbatim — for
	// the node's Component. One concept keeps one name and is disambiguated by
	// which entry it sits on, rather than gaining a second spelling per scope.
	//
	// Empty when the attached Feature declares no annotations, and when its
	// type resolves to no catalog entity — the same condition that leaves
	// ResolvedVersion empty.
	CatalogAnnotations OrderedMap[string] `json:"catalog_annotations,omitempty,omitzero"`
	// Binding names the attachment slot this attachment fills, or
	// AttachmentBindingFree when it fills none. The slot pairs with the
	// attachment by NAME — an attachment fills a slot when the Feature is
	// attached under the slot's name — so the value is an exact key from a
	// declared slot set, never anything read out of the name itself.
	//
	// Which slot set depends on where the attachment was written. An attachment
	// of the node's own block fills a slot the host Component declares in its
	// own featuresSchema, paired by the name it is attached under. An attachment
	// a connection carries (Connection is set) fills a slot the connection's
	// socket declares, paired by the slot name Connection records — never by its
	// composed name, which no declared slot carries. A socket's slot set is
	// closed, so such an attachment never reports AttachmentBindingFree in a
	// graph whose build succeeded.
	//
	// The slot name is carried rather than a bare boolean because which slot
	// was filled is the more useful half of the answer: a consumer that wants
	// only the yes/no compares against AttachmentBindingFree.
	//
	// Empty when the host Component's declared slot set could not be resolved.
	// That is distinct from AttachmentBindingFree: free is the positive
	// finding that the resolved Component declares no slot of this name, and
	// empty is the absence of a finding.
	//
	// Binding answers the question for an ATTACHMENT only. A similarly shaped
	// question — whether the socket an edge is wired into was declared by the
	// catalog — has a different subject, a different construction site and a
	// different consumer, so it does not belong on this field. One field
	// answering for both subjects would report on two unrelated declarations
	// under one name.
	Binding          string                         `json:"binding,omitempty"`
	Values           OrderedMap[any]                `json:"values,omitempty,omitzero"`
	Infra            OrderedMap[any]                `json:"infra,omitempty,omitzero"`
	Inject           OrderedMap[OrderedMap[string]] `json:"inject,omitempty,omitzero"`
	Origin           AttachmentOrigin               `json:"origin,omitempty"`
	AnnotationKey    string                         `json:"annotation_key,omitempty"`
	AnnotationSource string                         `json:"annotation_source,omitempty"`
	ValuesProvenance OrderedMap[FieldProvenance]    `json:"values_provenance,omitempty,omitzero"`
	InfraProvenance  OrderedMap[FieldProvenance]    `json:"infra_provenance,omitempty,omitzero"`
	// Sensitive is true when the attachment handles material an author
	// declared sensitive: either the attached Feature declares a
	// sensitive leaf in its values, infra or provides pillar, or the
	// slot the attachment fills declares the material it carries
	// sensitive — the host Component's slot of the same name, or for an
	// attachment a connection carries, the slot of that name the
	// connection's socket declares. The two declarations carry
	// different claims — a leaf says this datum is secret material, a
	// slot says whatever binds here handles secret material — and
	// either one alone makes the attachment sensitive.
	Sensitive bool `json:"sensitive,omitempty"`
	// SensitiveInject names the delivery keys of Inject whose
	// referenced provides key the attached Feature declares sensitive,
	// in Inject's own shape: mechanism -> delivery key -> the
	// referenced provides key. It is a subset of Inject, and the value
	// is retained so a consumer can name the declaration a delivery key
	// inherited its sensitivity from. An attachment declaring no
	// delivery mechanism has no delivery key to mark, so the map is
	// empty for it.
	SensitiveInject OrderedMap[OrderedMap[string]] `json:"sensitive_inject,omitempty,omitzero"`
	// SensitivePayloadLeaves names the leaves this attachment's values
	// and infra payload carries that the attached Feature declares
	// sensitive, each as the pillar-prefixed dotted path the datum
	// occupies — "values.<path>" or "infra.<path>". Ordered as the
	// payload holds the leaves, the values pillar's before the infra
	// pillar's — the same order Values and Infra themselves carry, and
	// what makes the wire bytes identical across runs of identical input.
	// The encoding carries the order positionally and sorts nothing of
	// its own, so the order this field is built in is the order a
	// consumer reads.
	//
	// The subject is the payload the attachment ends up carrying rather
	// than the block it was built from, so a leaf standing at a value the
	// Feature's schema declares as a default is named alongside one a
	// Release wrote. A filled default is a datum the payload carries,
	// and a marking read off the written block alone would leave it
	// unnamed.
	//
	// It intersects the declarations with what the attachment actually
	// carries, and that is a deliberate difference from the leaf half of
	// Sensitive, which does not intersect. The two answer different
	// questions, so neither behaviour is the other's bug. Sensitive
	// reports a property of the catalog entity plus the slot it fills, so
	// a reader can reason about the entity in isolation and gets the same
	// answer in every Release. This field reports which data one
	// Release put where, which is Release-specific by construction:
	// a leaf appears here only because this attachment's payload carries a
	// value at it. Intersecting inside Sensitive would make one entity
	// sensitive in one Release and not another; declining to intersect
	// here would name leaves that carry nothing.
	//
	// A leaf is named where a declaration COVERS it: the Feature's schema
	// declares that leaf, or declares an object containing it. A leaf
	// beneath an object a declaration covers is named by that declaration,
	// so covering it claims nothing the declaration did not reach.
	// Matching is segment-wise over the dotted path, so a declaration at
	// "db" covers "db.password" and does not reach a sibling "dbPassword".
	// Nothing is read out of the path's own text, no value is inspected,
	// and the per-attachment roll-up is never spread across individual
	// leaves, which would assert something false about a leaf left
	// readable.
	//
	// Empty when the attachment carries no leaf its Feature declares
	// sensitive. Empty is not permission: it says that the marking named
	// no leaf at this unit, never that the attachment carries nothing
	// sensitive, and a consumer treating it as licence to display or emit
	// a value in the clear has read it backwards. The marking is carried
	// as part of the payload's contract rather than in response to any one
	// consumer, so a policy that needs it can be written whenever the need
	// arises instead of waiting for the payload to be widened first — a
	// reader finding nothing that consumes it has therefore learnt a fact
	// about today's consumers, not a reason to remove it.
	SensitivePayloadLeaves []string `json:"sensitive_payload_leaves,omitempty"`

	// Connection names the connection this attachment was written on, when it
	// is one a connection carries rather than one written on the node's own
	// attachment block: the socket, the alias and the slot name it was
	// authored under. Nil for an attachment of the node's own block.
	//
	// It is what tells the two apart, and it is read rather than the
	// attachment's name: the name is composed from the alias and the slot,
	// and nothing reads the connection back out of it. An attachment a
	// connection carries is one of the node's attachments for its Manifest
	// mapping, its FeatureRecipe, its conformance and its sensitivity marking,
	// and is left out of the node's own `.features` in a template context. Its
	// Binding and the slot half of its Sensitive are read from the slot the
	// connection's socket declares rather than from the Component's own slots.
	//
	// The graph's wire form carries the socket and the alias, so a consumer of
	// an emitted graph joins the attachment to the edge whose source is this
	// node and whose socket and alias are these. The slot name stays off the
	// wire, where Binding reports it; see [AttachmentConnection].
	Connection *AttachmentConnection `json:"connection,omitempty"`

	// Realization names the FeatureRecipe the Manifest binds to this
	// attachment, as "<name>:<version>": by the attachment's own name where the
	// Manifest's name tier maps it — for an attachment a connection carries,
	// its qualified name — otherwise by its Feature type.
	//
	// It is recorded when the graph is finalised, from the same lookup the
	// render binds the attachment by, so the FeatureRecipe it names is the one a
	// build of the same documents renders the attachment with. The graph
	// records the binding; it renders nothing and fetches nothing to do so.
	//
	// The value is the "<name>:<version>" form the documents that FeatureRecipe
	// renders carry as their recipe-uri provenance, where the target stamps
	// one, composed by the one composition of that form, so the attachment
	// joins to what it renders without a translation between spellings.
	//
	// Empty when the Manifest binds the attachment to no FeatureRecipe the
	// catalog holds.
	Realization string `json:"realization,omitempty"`
}

// OnConnection reports whether the attachment was written on one of the
// node's connections rather than on the node's own attachment block. See
// [IRFeatureEntry.Connection].
func (e IRFeatureEntry) OnConnection() bool { return e.Connection != nil }

// FieldProvenance records the origin of one leaf in an IR feature
// attachment's values or infra pillar.
//
// Origin is "explicit" for leaves declared by an author-written
// features: block; "annotation" for leaves contributed by the
// annotation-driven expansion pass.
//
// Side is "aggregate" or "release" when Origin is "annotation",
// naming the authoring surface the annotation was declared on; empty
// when Origin is "explicit".
//
// AnnotationKey and AnnotationSource carry the triggering annotation
// for "annotation"-origin leaves: the key as declared in the manifest
// binding registry, and the source location as
// <file-path>:<dotted-yaml-key-path> (the key path alone where the
// compilation named no file for that document). Both fields are empty for
// "explicit"-origin leaves.
//
// OverriddenAnnotation holds the annotation contribution that lost this
// leaf: it is populated when an explicit contribution overwrites an
// annotation contribution at the same leaf, retaining the suppressed
// FieldProvenance so inspect rendering can surface the override and the
// advisory lint can name what was replaced. Nil when no override occurred
// at this leaf. Singular, because exactly one suppressed contribution is
// ever retained — a same-origin replacement replaces the provenance
// wholesale rather than chaining.
type FieldProvenance struct {
	// Origin is OriginExplicit or OriginAnnotation; a single leaf's provenance
	// never widens to OriginMixed (mixing is an attachment-top-level notion).
	Origin               AttachmentOrigin `json:"origin"`
	Side                 string           `json:"side,omitempty"`
	AnnotationKey        string           `json:"annotation_key,omitempty"`
	AnnotationSource     string           `json:"annotation_source,omitempty"`
	OverriddenAnnotation *FieldProvenance `json:"overridden_annotation,omitempty"`
}

// IRProvenance records compiler and source traceability for an IR build.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/provenance
type IRProvenance struct {
	SchemaVersion    string `json:"schema_version"`
	CompilerVersion  string `json:"compiler_version"`
	AggregateVersion string `json:"aggregate_version"`
	ManifestVersion  string `json:"manifest_version"`
	ReleaseVersion   string `json:"release_version"`
}
