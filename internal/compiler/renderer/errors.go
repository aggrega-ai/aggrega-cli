// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
)

// ErrProvidesSelfContainment names the condition only. The diagnostic that
// carries it names the expression the entry has to change, and is positioned at
// the Recipe the entry was written in and the authored key inside it — so the
// finding is something an author navigates to rather than a message to read.
//
// The reference is refused before anything is evaluated, which is why the halt
// exists at all: the value such a template would produce is never produced, so
// without the refusal the entry would render empty rather than fail. An entry that
// slips past the refusal still does not render empty — the evaluator checks the
// same references against the same declared scope, and the two share one walk.
func ErrProvidesSelfContainment() error { return errProvidesSelfContainment }

var errProvidesSelfContainment = cerrors.Register("compiler-renderer/provides-self-containment", cerrors.Meta{
	ShortCode: "E0057",
	Message:   "provides block references a root outside the self-contained set",
})

// ErrTemplateUnknownRoot is the halt raised when a template references a
// top-level root its render context does not define. The wrapping
// diagnostic names the offending path and lists the roots the context does
// define, both derived from the context type so the refused set and the
// reported set are one derivation. It fires identically whether the
// reference is bare or wrapped in a fallback, because a root that does not
// exist is an authoring error at either spelling — a wrapper can supply a
// default for an absent KEY, never for an absent ROOT.
func ErrTemplateUnknownRoot() error { return errTemplateUnknownRoot }

var errTemplateUnknownRoot = cerrors.Register("compiler-renderer/template-unknown-root", cerrors.Meta{
	ShortCode: "E0060",
	Message:   "template references a root the render context does not define",
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
// errors.Is(err, ErrLocalsForwardReference()).
func ErrLocalsForwardReference() error { return errLocalsForwardReference }

var errLocalsForwardReference = cerrors.Register("compiler-renderer/locals-forward-reference", cerrors.Meta{
	ShortCode: "E0042",
	Message:   "locals entry references a sibling defined later in the same block",
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
// Category: CATALOG_ERROR. Match with errors.Is(err, ErrLocalsUnknownReference()).
func ErrLocalsUnknownReference() error { return errLocalsUnknownReference }

var errLocalsUnknownReference = cerrors.Register("compiler-renderer/locals-unknown-reference", cerrors.Meta{
	ShortCode: "E0043",
	Message:   "template references a key the locals block does not declare",
})

func ErrProvidesEvaluation() error { return errProvidesEvaluation }

var errProvidesEvaluation = cerrors.Register("compiler-renderer/provides-evaluation", cerrors.Meta{
	ShortCode: "E0056",
	Message:   "provides template evaluation failed",
})

func ErrSelfInjectKeyCollision() error { return errSelfInjectKeyCollision }

var errSelfInjectKeyCollision = cerrors.Register("compiler-renderer/self-inject-key-collision", cerrors.Meta{
	ShortCode: "E0059",
	Message:   "duplicate delivery key across self-inject and uses-inject",
})

func ErrAttachmentInjectKeyCollision() error { return errAttachmentInjectKeyCollision }

var errAttachmentInjectKeyCollision = cerrors.Register("compiler-renderer/attachment-inject-key-collision", cerrors.Meta{
	ShortCode: "E0034",
	Message:   "duplicate delivery key across attachment-inject and another inject source",
})

func ErrUnmappedCustomMechanism() error { return errUnmappedCustomMechanism }

var errUnmappedCustomMechanism = cerrors.Register("compiler-renderer/unmapped-custom-mechanism", cerrors.Meta{
	ShortCode: "E0062",
	Message:   "custom inject mechanism has no matching key in injectMapping output; add the mechanism's key to the Recipe injectMapping",
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
// errors.Is(err, ErrProvidesContractUnfulfilled()).
func ErrProvidesContractUnfulfilled() error { return errProvidesContractUnfulfilled }

var errProvidesContractUnfulfilled = cerrors.Register("compiler-renderer/provides-contract-unfulfilled", cerrors.Meta{
	ShortCode: "E0055",
	Message:   "realizer does not publish a provides key its abstract entity's schema.provides contract declares",
})

// ErrMappingImportShape fires when a valuesMapping $import: (or a recursive
// MappingLibrary entry body's field-root $import:) is neither a host-bearing
// reference nor a well-formed bare-name `<Library>#/mappings/<entry>` identity.
func ErrMappingImportShape() error { return errMappingImportShape }

var errMappingImportShape = cerrors.Register("compiler-renderer/mapping-import-shape", cerrors.Meta{
	ShortCode: "E0045",
	Message:   "valuesMapping $import: is not a bare-name mapping-library reference (<Library>#/mappings/<entry>)",
})

// ErrMappingLibraryNotFound fires when a bare-name mapping-library import
// names a MappingLibrary absent from the resolved set (the local catalog plus
// any lockfile-pinned libraries bridged in at load).
func ErrMappingLibraryNotFound() error { return errMappingLibraryNotFound }

var errMappingLibraryNotFound = cerrors.Register("compiler-renderer/mapping-library-not-found", cerrors.Meta{
	ShortCode: "E0046",
	Message:   "mapping-library import references a MappingLibrary not present in the catalog",
})

// ErrMappingEntryNotFound fires when a bare-name mapping-library import names
// an entry absent from the resolved MappingLibrary's mappings map.
func ErrMappingEntryNotFound() error { return errMappingEntryNotFound }

var errMappingEntryNotFound = cerrors.Register("compiler-renderer/mapping-entry-not-found", cerrors.Meta{
	ShortCode: "E0044",
	Message:   "mapping-library import references an entry not present in the MappingLibrary",
})

// ErrWorkloadImportShape fires when a workload $import: (or a recursive
// WorkloadLibrary entry body's field-root $import:) is neither a host-bearing
// reference nor a well-formed bare-name `<Library>#/workloads/<entry>` identity.
func ErrWorkloadImportShape() error { return errWorkloadImportShape }

var errWorkloadImportShape = cerrors.Register("compiler-renderer/workload-import-shape", cerrors.Meta{
	ShortCode: "E0064",
	Message:   "workload $import: is not a bare-name workload-library reference (<Library>#/workloads/<entry>)",
})

// ErrWorkloadLibraryNotFound fires when a bare-name workload-library import
// names a WorkloadLibrary absent from the resolved set (the local catalog plus
// any lockfile-pinned libraries bridged in at load).
func ErrWorkloadLibraryNotFound() error { return errWorkloadLibraryNotFound }

var errWorkloadLibraryNotFound = cerrors.Register("compiler-renderer/workload-library-not-found", cerrors.Meta{
	ShortCode: "E0065",
	Message:   "workload-library import references a WorkloadLibrary not present in the catalog",
})

// ErrWorkloadEntryNotFound fires when a bare-name workload-library import names
// an entry absent from the resolved WorkloadLibrary's workloads map.
func ErrWorkloadEntryNotFound() error { return errWorkloadEntryNotFound }

var errWorkloadEntryNotFound = cerrors.Register("compiler-renderer/workload-entry-not-found", cerrors.Meta{
	ShortCode: "E0063",
	Message:   "workload-library import references an entry not present in the WorkloadLibrary",
})

// ErrArtifactImportShape fires when an artifacts entry `$import:` value is
// neither a host-bearing reference nor a well-formed bare-name
// `<Library>#/artifacts/<entry>` identity — including a field-root `$import:`
// nested inside a bundle file body, which is not supported (a single bundle
// file cannot expand into another bundle; compose at the consumption site).
func ErrArtifactImportShape() error { return errArtifactImportShape }

var errArtifactImportShape = cerrors.Register("compiler-renderer/artifact-import-shape", cerrors.Meta{
	ShortCode: "E0027",
	Message:   "artifacts $import: is not a bare-name artifacts-library reference (<Library>#/artifacts/<entry>)",
})

// ErrArtifactsLibraryNotFound fires when a bare-name artifacts-library import
// names an ArtifactsLibrary absent from the resolved set (the local catalog
// plus any lockfile-pinned libraries bridged in at load).
func ErrArtifactsLibraryNotFound() error { return errArtifactsLibraryNotFound }

var errArtifactsLibraryNotFound = cerrors.Register("compiler-renderer/artifacts-library-not-found", cerrors.Meta{
	ShortCode: "E0031",
	Message:   "artifacts-library import references an ArtifactsLibrary not present in the catalog",
})

// ErrArtifactsLibraryEntryNotFound fires when a bare-name artifacts-library
// import names a bundle absent from the resolved ArtifactsLibrary's artifacts map.
func ErrArtifactsLibraryEntryNotFound() error { return errArtifactsLibraryEntryNotFound }

var errArtifactsLibraryEntryNotFound = cerrors.Register("compiler-renderer/artifacts-library-entry-not-found", cerrors.Meta{
	ShortCode: "E0030",
	Message:   "artifacts-library import references a bundle not present in the ArtifactsLibrary",
})

// ErrArtifactOutputCollision fires when one Recipe's artifacts map emits two
// outputs under the same filename. A collision halts loudly rather than one output
// silently overwriting another (a Loud Guardrail, per the No Ghost Infrastructure
// invariant).
//
// What it covers is a residue, and saying so is part of the contract. An artifact
// output is named by the key it was authored under — a bundle's files nested under
// that key — and a mapping key is unique within one document, so a collision
// between two entries that each name their own output cannot arise. The authoring
// surface bounds a key to a single filename, so a key cannot spell the nested name
// another key's bundle produces either — that name is no longer writable. What
// remains is content that reached the projection without the envelope validation
// and the duplicate-key refusal a decoded document gets: state assembled in
// process rather than read from an authored document. The guard is retained for
// exactly that, since a name arriving by that route has passed nothing.
//
// It is raised per Recipe over that Recipe's own artifacts, which is why it is
// distinct from the collision between two contributors to one assembled output.
// Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrArtifactOutputCollision()).
func ErrArtifactOutputCollision() error { return errArtifactOutputCollision }

var errArtifactOutputCollision = cerrors.Register("compiler-renderer/artifact-output-collision", cerrors.Meta{
	ShortCode: "E0028",
	Message:   "artifacts output filename collision: two artifact outputs resolve to the same filename",
})

// ErrArtifactPillarNotDeclared fires when an artifacts entry's template reads a
// pillar the Recipe carrying it does not declare at all: `.workload` or a key
// beneath it on a Recipe with no workload:, or `.source` or a key beneath it on a
// Recipe with no source:. Both arms are one condition — a template consuming a
// pillar that was never authored — so they carry one identity, and the wrapping
// diagnostic names which pillar and the exact expression that reached for it.
//
// It is checked before the entry is evaluated, which is what makes the pillar the
// subject of the report. Left to evaluation, an absent pillar reaches the template
// as a nil value and the failure surfaces as whatever the template engine says
// about dereferencing one — a message naming neither the pillar nor the
// declaration that is missing. The expression the diagnostic names is the literal
// dot-path the parser captured, so it is the text to edit rather than a
// reconstruction of it.
//
// It is deliberately distinct from ErrTemplateUnknownRoot, and the two describe
// different mistakes on the same surface. That one refuses a root the render
// context does not define, or a mistyped key beneath a pillar whose shape is
// declared; `.workload` and `.source` are both defined roots on the artifacts
// render context, so a reference to one is well-formed and reaches this condition
// instead. The remedies differ accordingly: that one is corrected by fixing the
// reference, this one by declaring the pillar or by dropping the reference.
// Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrArtifactPillarNotDeclared()).
func ErrArtifactPillarNotDeclared() error { return errArtifactPillarNotDeclared }

var errArtifactPillarNotDeclared = cerrors.Register("compiler-renderer/artifact-pillar-not-declared", cerrors.Meta{
	ShortCode: "E0029",
	Message:   "artifact template references a pillar the Recipe does not declare",
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
func ErrAssembledOutputCollision() error { return errAssembledOutputCollision }

var errAssembledOutputCollision = cerrors.Register("compiler-renderer/assembled-output-collision", cerrors.Meta{
	ShortCode: "E0032",
	Message:   "assembled output collision: two contributors write the same output path",
})

// ErrDerivedKeyCollision fires when one derived name is declared more than once
// in one key space of one assembled output — whether two contributors each
// derived it, or one contributor's single declaration carried it twice. A
// provider guest derives the names its output addresses resources by, and those
// names live inside the file bytes the host writes verbatim — so the host cannot
// see them. The guest declares them instead, and this sentinel is the halt when
// the declarations are not injective within a space: the colliding contributors
// halt loudly rather than one silently displacing the other in the merged output
// (a Loud Guardrail, per the No Ghost Infrastructure invariant).
//
// One contributor restating a name across its several render crossings is not a
// collision and does not fire this: the name describes one resource and the
// output carries it once. What fires is one declaration carrying the name twice,
// which is that contributor's own derivation failing to be injective.
//
// The check is blind to what a key means. It compares declared strings for
// equality within one declared space, so it covers every key space any guest
// declares — a second provider needs no second guard — and it interprets no
// provider format, learns no key's meaning, and derives no key itself.
//
// It is a sibling of ErrAssembledOutputCollision, not a widening of it, and the
// two must not be merged. They differ in what collides and in who can see it:
// ErrAssembledOutputCollision says two contributors write one output path, which
// the host observes directly because it assembles the files. This sentinel says
// two contributors chose one name inside their content, which the host observes
// only because the guests declared it. A caller matching one is not asking about
// the other.
func ErrDerivedKeyCollision() error { return errDerivedKeyCollision }

var errDerivedKeyCollision = cerrors.Register("compiler-renderer/derived-key-collision", cerrors.Meta{
	ShortCode: "E0038",
	Message:   "derived name collision: one name is derived more than once in one key space",
})

// ErrCircularImport is the $import-surface name for a mapping-library import
// expansion cycle. It is the shared refcycle.ErrCycle sentinel — the same
// identity the $ref surface reports via ErrCircularRef — so one detector
// guards both surfaces; the recursive resolver maintains a stack of in-flight
// canonical `<Library>#/mappings/<Entry>` keys and surfaces this with the
// chain named in the message. Match with errors.Is(err, ErrCircularImport())
// or, equivalently, errors.Is(err, refcycle.ErrCycle()).
func ErrCircularImport() error { return refcycle.ErrCycle() }

// ErrHostInLibraryImport fires when a library $import: carries a
// registry host — the `oci://…` form. It is shared by the valuesMapping
// (`<Library>#/mappings/<entry>`) and workload
// (`<Library>#/workloads/<entry>`) surfaces: a library import is addressed
// by bare-name identity, never by physical registry location — the library
// name resolves against the loaded catalog and the version is supplied by
// the lockfile. A host in the reference is rejected so physical location
// cannot leak into authored source. The per-surface wrapping context names
// which surface carried the host.
func ErrHostInLibraryImport() error { return errHostInLibraryImport }

var errHostInLibraryImport = cerrors.Register("compiler-renderer/host-in-library-import", cerrors.Meta{
	ShortCode: "E0040",
	Message:   "library $import: carries a registry host; reference the library by bare-name identity",
})

// ErrProviderNodeInputInvalid is the Loud Guardrail halt a provider
// engine raises when its optional input-validation export reports an
// halt-severity diagnostic for a node's resolved input before render
// dispatch. The wrapping chain at the emission site names the offending
// node, the provider, and the guest's diagnostic message, so the operator
// sees which node failed the provider's structural check and why. Warning-
// and info-severity diagnostics from the same export do not produce this
// halt — they surface to the build result's warnings. Category:
// TRANSLATION_ERROR (a provider-reported structural defect in the node's
// rendered input). Match with errors.Is(err, ErrProviderNodeInputInvalid()).
func ErrProviderNodeInputInvalid() error { return errProviderNodeInputInvalid }

var errProviderNodeInputInvalid = cerrors.Register("compiler-renderer/provider-node-input-invalid", cerrors.Meta{
	ShortCode: "E0054",
	Message:   "provider rejected node input",
})

// ErrUnknownProvider is the compiler-level fatal halt produced by the
// build-time engineFactory when a Recipe declares a provider name that
// the CLI does not register. The CLI's engineFactory closure wraps this
// sentinel with the offending provider name so callers can match via
// errors.Is(err, ErrUnknownProvider()) regardless of which provider
// triggered the halt.
func ErrUnknownProvider() error { return errUnknownProvider }

var errUnknownProvider = cerrors.Register("compiler-renderer/unknown-provider", cerrors.Meta{
	ShortCode: "E0061",
	Message:   "unknown provider",
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
func ErrPathCollision() error { return errPathCollision }

var errPathCollision = cerrors.Register("compiler-renderer/path-collision", cerrors.Meta{
	ShortCode: "E0048",
	Message:   "dispatch path produced multiple provider outputs",
})

// ErrInstanceIDCollision fires when two contributors from different
// boundaries land in the same dispatch group (same evaluated path,
// same provider) with the same component instance ID. Defensive
// guardrail — topology flattening's prefix discipline should make
// collisions impossible — but per the Loud Guardrails invariant the
// assembler-input validation surfaces structural violations
// immediately rather than letting them propagate. Category:
// TRANSLATION_ERROR.
func ErrInstanceIDCollision() error { return errInstanceIDCollision }

var errInstanceIDCollision = cerrors.Register("compiler-renderer/instance-id-collision", cerrors.Meta{
	ShortCode: "E0041",
	Message:   "multiple contributors share an instance ID at the same dispatch path",
})

// ErrChartNameMismatch fires when two or more boundaries' contributing
// components evaluate to the same dispatch path under a provider whose
// assembler names its output from a boundary annotation and requires that
// name to be unique, but their effective values for that annotation
// (computed via the boundary annotation cascade) resolve to different
// values. The merged assembly has exactly one name; cascade or declare
// matching values explicitly. Category: TRANSLATION_ERROR.
func ErrChartNameMismatch() error { return errChartNameMismatch }

var errChartNameMismatch = cerrors.Register("compiler-renderer/chart-name-mismatch", cerrors.Meta{
	ShortCode: "E0036",
	Message:   "merged-path assembly has conflicting assembly-name annotation values",
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
func ErrPathTemplateInvalidField() error { return errPathTemplateInvalidField }

var errPathTemplateInvalidField = cerrors.Register("compiler-renderer/path-template-invalid-field", cerrors.Meta{
	ShortCode: "E0049",
	Message:   "path template references a field not in the documented allowlist",
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
func ErrFRTemplateInvalidField() error { return errFRTemplateInvalidField }

var errFRTemplateInvalidField = cerrors.Register("compiler-renderer/fr-template-invalid-field", cerrors.Meta{
	ShortCode: "E0039",
	Message:   "FeatureRecipe template references a per-component-only field with executionScope: boundary",
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
func ErrAssertionFailed() error { return errAssertionFailed }

var errAssertionFailed = cerrors.Register("compiler-renderer/assertion-failed", cerrors.Meta{
	ShortCode: "E0033",
	Message:   "assertion failed",
})

// ErrPipelineTargetNotEmitted fires when a pipeline PATCH step addresses a
// document the keyed set does not hold — a scope/key pair absent from the
// node's emitted documents. It is the renderer's build-halting Loud
// Guardrail promoting the pipeline walk's local absent-target cause: the
// walk reports the typed condition; the integration halts the build with
// this sentinel, naming the offending step and the addressed key.
func ErrPipelineTargetNotEmitted() error { return errPipelineTargetNotEmitted }

var errPipelineTargetNotEmitted = cerrors.Register("compiler-renderer/pipeline-target-not-emitted", cerrors.Meta{
	ShortCode: "E0052",
	Message:   "pipeline PATCH targets a document the node did not emit",
})

// ErrPipelineHostScopeUnavailable fires when a pipeline step performs a
// /host operation but the step has no host component — a boundary-scope
// FeatureRecipe step (which has no single host) or a Recipe step (whose
// only scope is its own output). The host root is absent for these
// surfaces, so a /host patch or emit halts loudly rather than silently
// resolving against an empty set.
func ErrPipelineHostScopeUnavailable() error { return errPipelineHostScopeUnavailable }

var errPipelineHostScopeUnavailable = cerrors.Register("compiler-renderer/pipeline-host-scope-unavailable", cerrors.Meta{
	ShortCode: "E0050",
	Message:   "pipeline /host operation from a step with no host component",
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
// It also carries the finding a rule over an attachment's own-scope pipeline
// steps reports: an attachment's own output is written under its own position as
// discrete files whichever provider the component uses, so a step addressing that
// output against a component whose provider folds its dispatch group into one
// document has nothing to address by the time it runs. That is reported once the
// build knows what every plugin and provider declares, ahead of any node's walk.
// The remedy is to target a non-assembler provider for the FeatureRecipe's own
// output, move the step to /host, or remove it.
func ErrPipelinePatchPreAssembly() error { return errPipelinePatchPreAssembly }

var errPipelinePatchPreAssembly = cerrors.Register("compiler-renderer/pipeline-patch-pre-assembly", cerrors.Meta{
	ShortCode: "E0051",
	Message:   "pipeline PATCH targets an assembler provider's pre-assembly fragments",
})

// ErrPluginProviderIncompatible fires when a pipeline step's plugin declares
// an enumerated targets set that does not include the provider
// rendering the scope the step touches — the recipe's own provider for an
// own-scope step, the host component's provider for a host-scope step. The
// list is an explicit tested-compatibility claim, not an implicit "any
// similar provider"; running the plugin against an unlisted provider risks a
// wrong patch, so the build halts loudly rather than proceeding. An empty or
// absent set declares no compatibility claim, so it matches no provider and
// every step that uses the plugin halts here. A plugin the host could not ask
// does NOT halt here: an unobtainable `describe()` self-report is the absence of
// a claim rather than an empty one, and such a step halts with the resolution,
// load or `describe()` failure itself, which is the thing to repair. It is
// reported once the build knows what every plugin declares about itself — reading
// a self-report is itself a guest call, so what the report precedes is the walk
// that invokes a step — and provider names are matched exactly. A step is
// reported once per authored step rather than once per build, so an author sees
// every offending step of a pipeline in one build.
// Category: CATALOG_ERROR.
func ErrPluginProviderIncompatible() error { return errPluginProviderIncompatible }

var errPluginProviderIncompatible = cerrors.Register("compiler-renderer/plugin-provider-incompatible", cerrors.Meta{
	ShortCode: "E0053",
	Message:   "pipeline plugin is not compatible with the rendering provider",
})

// ErrBoundaryAssemblerMissing fires when a provider that self-reports assembling
// a dispatch group into a single artifact (a merged docker-compose.yaml, an
// umbrella chart) is served by a rendering plugin that advertises per-node
// rendering but not the matching boundary-assembly export. The dispatched engine
// can render each node but cannot merge them, so assembly would be silently
// skipped and the build would emit unmerged per-node fragments. Per the No Ghost
// Infrastructure / Loud Guardrails invariant the build halts here instead, naming
// the provider, the dispatch path, and the missing assemble_boundary export,
// rather than producing an unassembled result the operator only discovers at
// debug time. The remedy is to serve the provider through a plugin that
// advertises both render_node and assemble_boundary.
// Category: TRANSLATION_ERROR.
func ErrBoundaryAssemblerMissing() error { return errBoundaryAssemblerMissing }

var errBoundaryAssemblerMissing = cerrors.Register("compiler-renderer/boundary-assembler-missing", cerrors.Meta{
	ShortCode: "E0035",
	Message:   "provider expected to assemble its dispatch group provides no boundary assembler",
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
// errors.Is(err, ErrArtifactContentMismatch()).
func ErrArtifactContentMismatch() error { return errArtifactContentMismatch }

var errArtifactContentMismatch = cerrors.Register("compiler-renderer/artifact-content-mismatch", cerrors.Meta{
	ShortCode: "E0026",
	Message:   "imported artifact content does not match the recipe's recorded digest",
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
// Match with errors.Is(err, ErrCrossProviderLinkUnsupported()).
func ErrCrossProviderLinkUnsupported() error { return errCrossProviderLinkUnsupported }

var errCrossProviderLinkUnsupported = cerrors.Register("compiler-renderer/cross-provider-link-unsupported", cerrors.Meta{
	ShortCode: "E0037",
	Message:   "cross-boundary link between endpoints rendered by different providers is not supported",
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
// errors.Is(err, ErrRenderNondeterministic()).
func ErrRenderNondeterministic() error { return errRenderNondeterministic }

var errRenderNondeterministic = cerrors.Register("compiler-renderer/render-nondeterministic", cerrors.Meta{
	ShortCode: "E0058",
	Message:   "provider render is not deterministic",
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
// it. The diagnostic names the provider that wrote the file and the output path
// it stands at, and is positioned at the boundary the output was assembled for —
// a path is unique only within a boundary and a provider together, so all three
// are what identify one file. Every offending file is reported rather than the
// first. Category: TRANSLATION_ERROR, because a placeholder the host stamped and
// a provider failed to resolve is a fault on the translation side with nothing in
// any catalog document to correct. Match with
// errors.Is(err, ErrOrphanCrossBoundaryLinkSentinel()).
func ErrOrphanCrossBoundaryLinkSentinel() error { return errOrphanCrossBoundaryLinkSentinel }

var errOrphanCrossBoundaryLinkSentinel = cerrors.Register("compiler-renderer/orphan-cross-boundary-link-sentinel", cerrors.Meta{
	ShortCode: "E0047",
	Message:   "assembled output retains an unresolved cross-boundary link placeholder",
})

// ErrNestSegmentNotOneDirectory is the Loud Guardrail halt raised when a
// boundary assembler names a per-node sub-directory that is not a single
// directory name. A nesting assembler names the directory its contributors' own
// sub-directories sit under, and the orchestrator composes each node's identity
// into it — so a value carrying a path separator, or naming the current or the
// parent directory, would place a node's files somewhere other than beneath the
// node's own position in the dispatch group. That is either an escape out of the
// dispatch position or a silent re-layout of the group, so the build halts here
// instead of composing the path. The diagnostic names the provider whose
// assembler returned the value and the value itself, so an author sees which
// assembler to change and what it returned. The remedy is to name one directory.
// An assembler that names nothing is not affected: the orchestrator then applies
// its own default and the layout is unchanged. Category: TRANSLATION_ERROR,
// because the value came from a provider's assembler with nothing in any catalog
// document to correct. Match with
// errors.Is(err, ErrNestSegmentNotOneDirectory()).
func ErrNestSegmentNotOneDirectory() error { return errNestSegmentNotOneDirectory }

var errNestSegmentNotOneDirectory = cerrors.Register("compiler-renderer/nest-segment-not-one-directory", cerrors.Meta{
	ShortCode: "E0250",
	Message:   "boundary assembler named a per-node sub-directory that is not a single directory name",
})

// ErrEmptySource is the Loud Guardrail halt raised when a Recipe declares
// `source:` and gives it an empty string. The two authoring modes a Recipe's
// source field selects between are sourceless — no external artifact, output
// built entirely from the Recipe's other input kinds — and sourced, where the
// source names a chart, module, or file to render from. An absent source selects
// the first; a non-empty value selects the second; an empty string selects
// neither, so it is an authoring ambiguity rather than a value with a meaning.
//
// Admitting it as sourceless is the failure this halt exists to stop: the
// commonest way to write it is to delete a URI and leave the key behind, where
// the author meant sourced mode and the build would have shipped without the
// artifact, never saying which of the two readings it took. Silently resolving
// two authoring intents onto one behaviour is the silent reinterpretation the
// no-ghost-infrastructure invariant forbids, and its answer is a halt naming
// what to write instead.
//
// One condition, host-side and provider-agnostic: the ambiguity is a property of
// the authored Recipe and is identical whichever target renders it, so a reader
// comparing two providers meets one identity for one mistake rather than one per
// provider. The diagnostic is positioned at the Recipe document and names both
// valid authoring paths — omit the field, or give it a real path — so the
// correction is readable from the halt. Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrEmptySource()).
func ErrEmptySource() error { return errEmptySource }

var errEmptySource = cerrors.Register("compiler-renderer/empty-source", cerrors.Meta{
	ShortCode: "E0251",
	Message:   "recipe source is an empty string, which names neither sourceless mode nor sourced mode",
})

// ErrArtifactsLibraryFileEscapesKey is the Loud Guardrail halt raised when a
// file inside an ArtifactsLibrary bundle carries a name that leaves the
// artifacts key the bundle was consumed at. One artifacts entry importing a
// bundle expands into one output per bundle file, each nested under that key —
// so the key names a directory and every file the bundle contributes belongs
// beneath it. A file name climbing out of that directory, naming an absolute
// location, or naming nothing at all does not belong beneath it, and the build
// halts here rather than placing the file somewhere the consuming Recipe never
// named.
//
// What it prevents is a silent re-layout rather than a write outside the output
// directory: the output directory is guarded again where the files are written,
// so a name climbing far enough is refused there in any case. A name climbing
// exactly one level is the one that survives — it resolves back inside the
// output directory, at a position the consuming Recipe did not choose. That is
// worse than a refused write, because every collision guard on this path reads
// the name as it was composed rather than as it resolves: two such files can
// resolve onto one output while nothing reports a collision, and which of them
// survives is then decided by nothing the author wrote. Refusing the name is
// what keeps the composed name and the written position the same thing.
//
// A file name that stays inside the key's directory is untouched, including one
// naming a subdirectory of its own: a bundle is free to carry a nested layout,
// and nesting that layout under the consumption key is exactly what the key is
// for.
//
// The consuming Recipe and the published bundle are usually written by different
// authors, so the diagnostic names both halves — the artifacts key the bundle
// was consumed at and the file name the bundle carries — together with the
// remedy, which is to give the file a name that stays under that key.
// Category: CATALOG_ERROR, because the offending value came from an authored
// catalog document, unlike the provider-supplied sub-directory name one layer up
// that ErrNestSegmentNotOneDirectory refuses as a translation fault. Match with
// errors.Is(err, ErrArtifactsLibraryFileEscapesKey()).
func ErrArtifactsLibraryFileEscapesKey() error { return errArtifactsLibraryFileEscapesKey }

var errArtifactsLibraryFileEscapesKey = cerrors.Register("compiler-renderer/artifacts-library-file-escapes-key", cerrors.Meta{
	ShortCode: "E0252",
	Message:   "artifacts-library bundle file name leaves the artifacts key its output is nested under",
})
