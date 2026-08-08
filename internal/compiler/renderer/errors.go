// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
)

// ErrProvidesSelfContainment names the condition only. The wrapping
// diagnostic spells out which roots the provides surface admits, derived
// from the allowlists themselves, so the permitted set is stated once and
// cannot drift from the check that enforces it.
const ErrProvidesSelfContainment cerrors.Code = "compiler-renderer/provides-self-containment"

var _ = cerrors.Register(ErrProvidesSelfContainment, cerrors.Meta{
	Message: "provides block references a root outside the self-contained set",
})

// ErrTemplateUnknownRoot is the halt raised when a template references a
// top-level root its render context does not define. The wrapping
// diagnostic names the offending path and lists the roots the context does
// define, both derived from the context type so the refused set and the
// reported set are one derivation. It fires identically whether the
// reference is bare or wrapped in a fallback, because a root that does not
// exist is an authoring error at either spelling — a wrapper can supply a
// default for an absent KEY, never for an absent ROOT.
const ErrTemplateUnknownRoot cerrors.Code = "compiler-renderer/template-unknown-root"

var _ = cerrors.Register(ErrTemplateUnknownRoot, cerrors.Meta{
	Message: "template references a root the render context does not define",
})

// ErrLocalsForwardReference is the Loud Guardrail halt raised when a locals
// entry references a sibling the same block declares but has not resolved yet.
// A locals block resolves in author order, and an entry reads only a sibling
// defined above it; the block is never sorted or reordered to make a later
// definition reachable, because emitted output would then follow an inference
// rather than what the author wrote. Without this halt the reference resolves
// through the missing-key policy to an empty string, so a derived hostname or a
// secret name silently loses the segment it was built from. The wrapping
// diagnostic names the referencing entry, the referenced key, the entries
// resolved at that point, and the remedy — move the definition above its
// reader. It is deliberately distinct from ErrLocalsUnknownReference: this one
// is an ORDERING fault whose fix is to reorder the block, and reporting both
// through one condition would tell an author who mistyped a key to reorder
// their block. Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrLocalsForwardReference).
const ErrLocalsForwardReference cerrors.Code = "compiler-renderer/locals-forward-reference"

var _ = cerrors.Register(ErrLocalsForwardReference, cerrors.Meta{
	Message: "locals entry references a sibling defined later in the same block",
})

// ErrLocalsUnknownReference is the Loud Guardrail halt raised when a template
// references a key the locals block does not declare at all — a mistyped key, or
// one that was renamed or removed. Every surface that reads the pillar raises it:
// a locals entry, a provides template, and a Recipe source template. The locals
// pillar is an open keyed map, so an unknown key under it passes the root check
// and resolves through the missing-key policy to an empty string; the halt is
// what stops that empty value from reaching an emitted artifact. The wrapping
// diagnostic names the referencing entry, the key that does not exist, and the
// keys the block does declare, so the correction is readable from the halt. It is deliberately
// distinct from ErrLocalsForwardReference: this one is a NAMING fault whose fix
// is to correct the key, and the two mistakes have different remedies.
// Category: CATALOG_ERROR. Match with errors.Is(err, ErrLocalsUnknownReference).
const ErrLocalsUnknownReference cerrors.Code = "compiler-renderer/locals-unknown-reference"

var _ = cerrors.Register(ErrLocalsUnknownReference, cerrors.Meta{
	Message: "template references a key the locals block does not declare",
})

const ErrProvidesEvaluation cerrors.Code = "compiler-renderer/provides-evaluation"

var _ = cerrors.Register(ErrProvidesEvaluation, cerrors.Meta{
	Message: "provides template evaluation failed",
})

const ErrSelfInjectKeyCollision cerrors.Code = "compiler-renderer/self-inject-key-collision"

var _ = cerrors.Register(ErrSelfInjectKeyCollision, cerrors.Meta{
	Message: "duplicate delivery key across self-inject and uses-inject",
})

const ErrAttachmentInjectKeyCollision cerrors.Code = "compiler-renderer/attachment-inject-key-collision"

var _ = cerrors.Register(ErrAttachmentInjectKeyCollision, cerrors.Meta{
	Message: "duplicate delivery key across attachment-inject and another inject source",
})

const ErrUnmappedCustomMechanism cerrors.Code = "compiler-renderer/unmapped-custom-mechanism"

var _ = cerrors.Register(ErrUnmappedCustomMechanism, cerrors.Meta{
	Message: "custom inject mechanism has no matching key in injectMapping output; add the mechanism's key to the Recipe injectMapping",
})

// ErrProvidesContractUnfulfilled is the Loud Guardrail halt raised at render
// when a bound realizer does not publish every provides key its abstract
// entity declares as the output contract in schema.provides — a Recipe short
// of its Component's provides, or a FeatureRecipe short of its Feature's
// provides (credential and non-credential attachments alike). The contract is
// structural key-presence, subset-required and superset-permitted: every
// declared key must appear in the realizer's produced provides bag, while
// extra produced keys are tolerated. Produced values are never inspected —
// they are opaque runtime pointers piped as strings, so their declared types
// stay a documentary consumer contract, not a build-time check. Without this
// halt a missing key would silently deliver nothing at inject/render time,
// leaving the consumer's reference an unset environment variable, argument, or
// file with no diagnostic. The wrapping chain at each emission site names the
// realizer, the abstract entity, and the first missing key (declared keys are
// checked in sorted order, so the reported key and the halt are byte-identical
// across runs). Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrProvidesContractUnfulfilled).
const ErrProvidesContractUnfulfilled cerrors.Code = "compiler-renderer/provides-contract-unfulfilled"

var _ = cerrors.Register(ErrProvidesContractUnfulfilled, cerrors.Meta{
	Message: "realizer does not publish a provides key its abstract entity's schema.provides contract declares",
})

// ErrMappingImportShape fires when a valuesMapping $import: (or a recursive
// MappingLibrary entry body's field-root $import:) is neither a host-bearing
// reference nor a well-formed bare-name `<Library>#/mappings/<entry>` identity.
const ErrMappingImportShape cerrors.Code = "compiler-renderer/mapping-import-shape"

var _ = cerrors.Register(ErrMappingImportShape, cerrors.Meta{
	Message: "valuesMapping $import: is not a bare-name mapping-library reference (<Library>#/mappings/<entry>)",
})

// ErrMappingLibraryNotFound fires when a bare-name mapping-library import
// names a MappingLibrary absent from the resolved set (the local catalog plus
// any lockfile-pinned libraries bridged in at load).
const ErrMappingLibraryNotFound cerrors.Code = "compiler-renderer/mapping-library-not-found"

var _ = cerrors.Register(ErrMappingLibraryNotFound, cerrors.Meta{
	Message: "mapping-library import references a MappingLibrary not present in the catalog",
})

// ErrMappingEntryNotFound fires when a bare-name mapping-library import names
// an entry absent from the resolved MappingLibrary's mappings map.
const ErrMappingEntryNotFound cerrors.Code = "compiler-renderer/mapping-entry-not-found"

var _ = cerrors.Register(ErrMappingEntryNotFound, cerrors.Meta{
	Message: "mapping-library import references an entry not present in the MappingLibrary",
})

// ErrWorkloadImportShape fires when a workload $import: (or a recursive
// WorkloadLibrary entry body's field-root $import:) is neither a host-bearing
// reference nor a well-formed bare-name `<Library>#/workloads/<entry>` identity.
const ErrWorkloadImportShape cerrors.Code = "compiler-renderer/workload-import-shape"

var _ = cerrors.Register(ErrWorkloadImportShape, cerrors.Meta{
	Message: "workload $import: is not a bare-name workload-library reference (<Library>#/workloads/<entry>)",
})

// ErrWorkloadLibraryNotFound fires when a bare-name workload-library import
// names a WorkloadLibrary absent from the resolved set (the local catalog plus
// any lockfile-pinned libraries bridged in at load).
const ErrWorkloadLibraryNotFound cerrors.Code = "compiler-renderer/workload-library-not-found"

var _ = cerrors.Register(ErrWorkloadLibraryNotFound, cerrors.Meta{
	Message: "workload-library import references a WorkloadLibrary not present in the catalog",
})

// ErrWorkloadEntryNotFound fires when a bare-name workload-library import names
// an entry absent from the resolved WorkloadLibrary's workloads map.
const ErrWorkloadEntryNotFound cerrors.Code = "compiler-renderer/workload-entry-not-found"

var _ = cerrors.Register(ErrWorkloadEntryNotFound, cerrors.Meta{
	Message: "workload-library import references an entry not present in the WorkloadLibrary",
})

// ErrArtifactImportShape fires when an artifacts entry `$import:` value is
// neither a host-bearing reference nor a well-formed bare-name
// `<Library>#/artifacts/<entry>` identity — including a field-root `$import:`
// nested inside a bundle file body, which is not supported (a single bundle
// file cannot expand into another bundle; compose at the consumption site).
const ErrArtifactImportShape cerrors.Code = "compiler-renderer/artifact-import-shape"

var _ = cerrors.Register(ErrArtifactImportShape, cerrors.Meta{
	Message: "artifacts $import: is not a bare-name artifacts-library reference (<Library>#/artifacts/<entry>)",
})

// ErrArtifactsLibraryNotFound fires when a bare-name artifacts-library import
// names an ArtifactsLibrary absent from the resolved set (the local catalog
// plus any lockfile-pinned libraries bridged in at load).
const ErrArtifactsLibraryNotFound cerrors.Code = "compiler-renderer/artifacts-library-not-found"

var _ = cerrors.Register(ErrArtifactsLibraryNotFound, cerrors.Meta{
	Message: "artifacts-library import references an ArtifactsLibrary not present in the catalog",
})

// ErrArtifactsLibraryEntryNotFound fires when a bare-name artifacts-library
// import names a bundle absent from the resolved ArtifactsLibrary's artifacts map.
const ErrArtifactsLibraryEntryNotFound cerrors.Code = "compiler-renderer/artifacts-library-entry-not-found"

var _ = cerrors.Register(ErrArtifactsLibraryEntryNotFound, cerrors.Meta{
	Message: "artifacts-library import references a bundle not present in the ArtifactsLibrary",
})

// ErrArtifactOutputCollision fires when two artifact outputs resolve to the
// same output filename — two bundle files, or a bundle file and another
// artifacts entry. A collision halts loudly rather than one output silently
// overwriting another (a Loud Guardrail, per the No Ghost Infrastructure
// invariant).
const ErrArtifactOutputCollision cerrors.Code = "compiler-renderer/artifact-output-collision"

var _ = cerrors.Register(ErrArtifactOutputCollision, cerrors.Meta{
	Message: "artifacts output filename collision: two artifact outputs resolve to the same filename",
})

// ErrAssembledOutputCollision fires when two contributors to one assembly
// write a file at the same output path — two components, a component and a
// boundary-attached feature, or a contributor and the assembled output
// itself. The colliding writes halt loudly rather than one silently
// overwriting another (a Loud Guardrail, per the No Ghost Infrastructure
// invariant).
//
// It is a sibling of ErrArtifactOutputCollision, not a widening of it, and the
// two must not be merged. They differ in surface and in remedy, not merely in
// scope: ErrArtifactOutputCollision says one Recipe's own artifacts map names
// one output filename twice, which its author fixes by renaming an entry in
// that map. This sentinel says two contributors that know nothing about each
// other landed on one path once the assembly merged their output, which is
// fixed by changing what one of them emits or by separating their output
// paths. A caller matching one is not asking about the other. A third,
// likewise separate, guard covers two resources colliding inside a single
// component's own render, before any of them reach an assembly.
const ErrAssembledOutputCollision cerrors.Code = "compiler-renderer/assembled-output-collision"

var _ = cerrors.Register(ErrAssembledOutputCollision, cerrors.Meta{
	Message: "assembled output collision: two contributors write the same output path",
})

// ErrCircularImport is the $import-surface name for a mapping-library import
// expansion cycle. It is the shared refcycle.ErrCycle sentinel — the same
// identity the $ref surface reports via ErrCircularRef — so one detector
// guards both surfaces; the recursive resolver maintains a stack of in-flight
// canonical `<Library>#/mappings/<Entry>` keys and surfaces this with the
// chain named in the message. Match with errors.Is(err, ErrCircularImport)
// or, equivalently, errors.Is(err, refcycle.ErrCycle).
const ErrCircularImport = refcycle.ErrCycle

// ErrHostInLibraryImport fires when a library $import: carries a
// registry host — the `oci://…` form. It is shared by the valuesMapping
// (`<Library>#/mappings/<entry>`) and workload
// (`<Library>#/workloads/<entry>`) surfaces: a library import is addressed
// by bare-name identity, never by physical registry location — the library
// name resolves against the loaded catalog and the version is supplied by
// the lockfile. A host in the reference is rejected so physical location
// cannot leak into authored source. The per-surface wrapping context names
// which surface carried the host.
const ErrHostInLibraryImport cerrors.Code = "compiler-renderer/host-in-library-import"

var _ = cerrors.Register(ErrHostInLibraryImport, cerrors.Meta{
	Message: "library $import: carries a registry host; reference the library by bare-name identity",
})

// ErrProviderNodeInputInvalid is the Loud Guardrail halt a provider
// engine raises when its optional input-validation export reports an
// error-severity diagnostic for a node's resolved input before render
// dispatch. The wrapping chain at the emission site names the offending
// node, the provider, and the guest's diagnostic message, so the operator
// sees which node failed the provider's structural check and why. Warning-
// and info-severity diagnostics from the same export do not produce this
// halt — they surface to the build result's warnings. Category:
// TRANSLATION_ERROR (a provider-reported structural defect in the node's
// rendered input). Match with errors.Is(err, ErrProviderNodeInputInvalid).
const ErrProviderNodeInputInvalid cerrors.Code = "compiler-renderer/provider-node-input-invalid"

var _ = cerrors.Register(ErrProviderNodeInputInvalid, cerrors.Meta{
	Message: "provider rejected node input",
})

// ErrUnknownProvider is the compiler-level fatal halt produced by the
// build-time engineFactory when a Recipe declares a provider name that
// the CLI does not register. The CLI's engineFactory closure wraps this
// sentinel with the offending provider name so callers can match via
// errors.Is(err, ErrUnknownProvider) regardless of which provider
// triggered the halt.
const ErrUnknownProvider cerrors.Code = "compiler-renderer/unknown-provider"

var _ = cerrors.Register(ErrUnknownProvider, cerrors.Meta{
	Message: "unknown provider",
})

// ErrPathCollision fires when two boundaries with the same evaluated
// dispatch path target different providers AND the operator overrode
// subDirectory: to defeat the per-provider segregation default. The
// resulting per-(path, provider) dispatch produces multiple assembler
// invocations whose on-disk outputs would collide. The error names the
// conflicting providers, the conflicting boundaries, and the resolution
// paths (restore the per-provider sub-path default, or change one
// boundary's directory). Category: OPS_ERROR — the collision exists
// because of how the boundaries were configured, so it is the operator
// who has the change to make.
const ErrPathCollision cerrors.Code = "compiler-renderer/path-collision"

var _ = cerrors.Register(ErrPathCollision, cerrors.Meta{
	Message: "dispatch path produced multiple provider outputs",
})

// ErrInstanceIDCollision fires when two contributors from different
// boundaries land in the same dispatch group (same evaluated path,
// same provider) with the same component instance ID. Defensive
// guardrail — topology flattening's prefix discipline should make
// collisions impossible — but per the Loud Guardrails invariant the
// assembler-input validation surfaces structural violations
// immediately rather than letting them propagate. Category:
// TRANSLATION_ERROR.
const ErrInstanceIDCollision cerrors.Code = "compiler-renderer/instance-id-collision"

var _ = cerrors.Register(ErrInstanceIDCollision, cerrors.Meta{
	Message: "multiple contributors share an instance ID at the same dispatch path",
})

// ErrChartNameMismatch fires when two or more boundaries' contributing
// components evaluate to the same dispatch path under a provider whose
// assembler names its output from a boundary annotation and requires that
// name to be unique, but their effective values for that annotation
// (computed via the boundary annotation cascade) resolve to different
// values. The merged assembly has exactly one name; cascade or declare
// matching values explicitly. Category: TRANSLATION_ERROR.
const ErrChartNameMismatch cerrors.Code = "compiler-renderer/chart-name-mismatch"

var _ = cerrors.Register(ErrChartNameMismatch, cerrors.Meta{
	Message: "merged-path assembly has conflicting assembly-name annotation values",
})

// ErrPathTemplateInvalidField fires before a path template
// (Release.spec.boundaries[].directory or subDirectory) executes, when it
// references a variable outside the documented set. Available fields in
// directory: templates are .capability, .boundary_name,
// .parameters.<typed_keys>, .parent_boundary.*, .manifest.recipe_name;
// subDirectory: additionally permits .component_id. That set is derived from
// the path-template context type rather than listed beside the check, and
// each of the two surfaces states only which variables it refuses, so a
// variable added to or retired from the context reaches both surfaces
// without an edit at either. A mistyped key UNDER a defined variable is a
// different halt: the variable itself is admitted, so the reference reports
// ErrTemplateUnknownRoot instead. Path templates use stricter missing-key
// handling than value templates because silent path failures produce broken
// outputs that operators only discover post-build. Category: OPS_ERROR
// (operator-authored path template).
const ErrPathTemplateInvalidField cerrors.Code = "compiler-renderer/path-template-invalid-field"

var _ = cerrors.Register(ErrPathTemplateInvalidField, cerrors.Meta{
	Message: "path template references a field not in the documented allowlist",
})

// ErrFRTemplateInvalidField fires when the boundary-scope FeatureRecipe
// dispatch validates an FR's templates, before any of them renders — not
// at Recipe load, which never parses them. It fires when a
// FeatureRecipe with spec.executionScope: boundary references a
// per-component-only field in any of its templates (workload,
// injectMapping, provides, valuesMapping, artifacts). Boundary-scope
// FRs use .boundary.* pillars in place of .context.* / .workload /
// .attachment_name; references to per-component fields are flagged at
// parse time so the runtime never has to defend against shape
// mismatches. Category: CATALOG_ERROR (catalog-authored FR
// references invalid context for its scope).
const ErrFRTemplateInvalidField cerrors.Code = "compiler-renderer/fr-template-invalid-field"

var _ = cerrors.Register(ErrFRTemplateInvalidField, cerrors.Meta{
	Message: "FeatureRecipe template references a per-component-only field with executionScope: boundary",
})

// ErrAssertionFailed wraps a build halt triggered by {{ fail "..." }}
// invocation inside a Component / Feature / Recipe / FeatureRecipe
// assertion template. Step 4b (Component/Feature, post-resolution,
// pre-render) and Step 5b (Recipe/FeatureRecipe, post-binding,
// pre-render) both produce this sentinel. The wrapping chain at
// the wire-in sites adds the failing entity kind/type, the failing
// assertion name, and the per-instance node ID; the inner error
// message carries the operator-supplied fail-call argument. The
// evaluator uses missingkey=error so any reference to an undefined
// field also halts here.
const ErrAssertionFailed cerrors.Code = "compiler-renderer/assertion-failed"

var _ = cerrors.Register(ErrAssertionFailed, cerrors.Meta{
	Message: "assertion failed",
})

// ErrPipelineTargetNotEmitted fires when a pipeline PATCH step addresses a
// document the keyed set does not hold — a scope/key pair absent from the
// node's emitted documents. It is the renderer's build-halting Loud
// Guardrail promoting the pipeline walk's local absent-target cause: the
// walk reports the typed condition; the integration halts the build with
// this sentinel, naming the offending step and the addressed key.
const ErrPipelineTargetNotEmitted cerrors.Code = "compiler-renderer/pipeline-target-not-emitted"

var _ = cerrors.Register(ErrPipelineTargetNotEmitted, cerrors.Meta{
	Message: "pipeline PATCH targets a document the node did not emit",
})

// ErrPipelineHostScopeUnavailable fires when a pipeline step performs a
// /host operation but the step has no host component — a boundary-scope
// FeatureRecipe step (which has no single host) or a Recipe step (whose
// only scope is its own output). The host root is absent for these
// surfaces, so a /host patch or emit halts loudly rather than silently
// resolving against an empty set.
const ErrPipelineHostScopeUnavailable cerrors.Code = "compiler-renderer/pipeline-host-scope-unavailable"

var _ = cerrors.Register(ErrPipelineHostScopeUnavailable, cerrors.Meta{
	Message: "pipeline /host operation from a step with no host component",
})

// ErrPipelinePatchPreAssembly fires when a pipeline PATCH addresses a
// document rendered by an assembler provider before assembly produces the
// final documents — the target is a pre-assembly fragment, not a discrete
// document. The discreteness rule is per-provider and scope-symmetric, and it
// is the defensive floor rather than the normal path: a pipeline against an
// assembler node is deferred whole to the post-assembly phase and replayed
// against the merged documents — a component Recipe (or component-scope
// FeatureRecipe) /own pipeline whose own provider is an assembler, and a
// component-scope FeatureRecipe /host pipeline whose host provider is an
// assembler. This guardrail therefore catches an assembler-fragment PATCH
// only when one reaches the per-node executor through a path that did not
// defer (a FeatureRecipe's own output is emitted as discrete fragment files,
// never assembled, so a /own PATCH against an FR-rendered assembler fragment
// has no merged target to defer to and halts here). EMIT is additive and runs
// per-node for every provider, so it is never gated by this guardrail.
//
// It also fires from the post-assembly replay's pre-walk compatibility gate
// for an own-scope step in a deferred host-only record: a component-scope
// FeatureRecipe whose host is an assembler defers its whole pipeline, but the
// FeatureRecipe's own output is never assembled, so the deferral records no
// own provider. An own-scope step in that record (a mixed own + host pipeline)
// has no merged own document to patch after assembly; the gate halts here with
// a cause-naming message rather than reporting an empty-provider compatibility
// claim. The remedy is to target a non-assembler provider for the
// FeatureRecipe's own output, move the step to /host, or remove it.
const ErrPipelinePatchPreAssembly cerrors.Code = "compiler-renderer/pipeline-patch-pre-assembly"

var _ = cerrors.Register(ErrPipelinePatchPreAssembly, cerrors.Meta{
	Message: "pipeline PATCH targets an assembler provider's pre-assembly fragments",
})

// ErrPluginProviderIncompatible fires when a pipeline step's plugin declares
// an enumerated targets set that does not include the provider
// rendering the scope the step touches — the recipe's own provider for an
// own-scope step, the host component's provider for a host-scope step. The
// list is an explicit tested-compatibility claim, not an implicit "any
// similar provider"; running the plugin against an unlisted provider risks a
// wrong patch, so the build halts loudly rather than proceeding. An empty or
// absent set declares no compatibility claim, so it matches no provider and
// every step that uses the plugin halts here. The check is pre-invoke (read
// from describe(), which is itself a guest call, before the walk invokes any
// step) and matches provider names exactly.
// Category: CATALOG_ERROR.
const ErrPluginProviderIncompatible cerrors.Code = "compiler-renderer/plugin-provider-incompatible"

var _ = cerrors.Register(ErrPluginProviderIncompatible, cerrors.Meta{
	Message: "pipeline plugin is not compatible with the rendering provider",
})

// ErrBoundaryAssemblerMissing fires when a provider whose in-tree engine
// assembles a dispatch group into a single artifact (a merged
// docker-compose.yaml, an umbrella chart) is served by a rendering plugin
// that advertises per-node rendering but not the matching boundary-assembly
// export. The dispatched engine can render each node but cannot merge them,
// so assembly would be silently skipped and the build would emit unmerged
// per-node fragments. Per the No Ghost Infrastructure / Loud Guardrails
// invariant the build halts here instead, naming the provider, the dispatch
// path, and the missing assemble_boundary export, rather than producing an
// unassembled result the operator only discovers at debug time. The remedy
// is to serve the provider through a plugin that advertises both render_node
// and assemble_boundary, or to render the provider through its in-tree
// engine. Category: TRANSLATION_ERROR.
const ErrBoundaryAssemblerMissing cerrors.Code = "compiler-renderer/boundary-assembler-missing"

var _ = cerrors.Register(ErrBoundaryAssemblerMissing, cerrors.Meta{
	Message: "provider expected to assemble its dispatch group provides no boundary assembler",
})

// ErrArtifactContentMismatch is the Loud Guardrail halt raised at build when
// an artifact's imported file ($import) has drifted from the content digest
// the recipe recorded for it at resolution. The recipe carries an
// artifactDigests pin per $import artifact, frozen from the file's bytes; the
// build re-digests the file and halts here when the two disagree, so an
// imported artifact silently edited after resolution never renders unverified.
// The wrapping chain names the recipe, the artifact key, and the imported
// path; the remedy is to re-resolve (re-freeze the pin) if the change was
// intended, or restore the file. Category: CATALOG_ERROR (a catalog-authored
// imported artifact drifted from its recorded pin). Match with
// errors.Is(err, ErrArtifactContentMismatch).
const ErrArtifactContentMismatch cerrors.Code = "compiler-renderer/artifact-content-mismatch"

var _ = cerrors.Register(ErrArtifactContentMismatch, cerrors.Meta{
	Message: "imported artifact content does not match the recipe's recorded digest",
})

// ErrCrossProviderLinkUnsupported marks a cross-boundary uses edge whose two
// ends assemble into dispatch groups rendered by different providers, where at
// least one of those providers advertises cross-boundary linking. Native
// cross-reference wiring between two different providers' assembled artifacts
// is not supported: a provider's link exports produce wiring in that
// provider's own artifact vocabulary, and one provider cannot resolve a
// reference published in another's. Rather than emit a wrong reference, the
// link pass leaves the existing per-node scalar value-pipe in place and
// surfaces this as a warning that names both ends (source and target node
// ids), both provider names, and the socket. It is a warning today, not a
// build halt — the edge still carries its scalar value across the boundary —
// so the build completes with the value-pipe fallback rather than failing.
// Match with errors.Is(err, ErrCrossProviderLinkUnsupported).
const ErrCrossProviderLinkUnsupported cerrors.Code = "compiler-renderer/cross-provider-link-unsupported"

var _ = cerrors.Register(ErrCrossProviderLinkUnsupported, cerrors.Meta{
	Message: "cross-boundary link between endpoints rendered by different providers is not supported",
})

// ErrRenderNondeterministic is the Loud Guardrail halt the verify-determinism
// build mode raises when a provider guest renders a node twice from identical
// input and produces different output — a guest that carries state across
// crossings, or is otherwise non-deterministic. The default build renders each
// node once; the opt-in verify-determinism mode renders twice and compares the
// emitted bytes, so a divergence that would break the byte-identical-output
// contract is caught at build time rather than surfacing as a GitOps diff. The
// wrapping chain at the emission site names the offending node and provider.
// The comparison is over the guest's raw emitted output, the same surface the
// rendered artifacts are carved from. Category: TRANSLATION_ERROR (a
// provider-side non-determinism in synthesised output). Match with
// errors.Is(err, ErrRenderNondeterministic).
const ErrRenderNondeterministic cerrors.Code = "compiler-renderer/render-nondeterministic"

var _ = cerrors.Register(ErrRenderNondeterministic, cerrors.Meta{
	Message: "provider render is not deterministic",
})

// ErrOrphanCrossBoundaryLinkSentinel is the Loud Guardrail halt raised when
// an assembled file retains a cross-boundary link placeholder after the link
// pass. The host stamps a deterministic placeholder into a consuming node's
// injected input for a cross-boundary edge, and the consuming provider's link
// resolution replaces it in the assembled output with the provider-native
// cross-reference. A placeholder that survives means resolution never reached
// it — the injected value landed at a position the resolver could not address
// (for example spliced into a larger string rather than a whole scalar), or
// an edge was stamped with no matching resolve. A build-internal placeholder
// is never valid shipped output, so the build halts here rather than emitting
// it. The wrapping chain names the offending file and its dispatch path.
// Category: TRANSLATION_ERROR. Match with
// errors.Is(err, ErrOrphanCrossBoundaryLinkSentinel).
const ErrOrphanCrossBoundaryLinkSentinel cerrors.Code = "compiler-renderer/orphan-cross-boundary-link-sentinel"

var _ = cerrors.Register(ErrOrphanCrossBoundaryLinkSentinel, cerrors.Meta{
	Message: "assembled output retains an unresolved cross-boundary link placeholder",
})
