// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
)

var (
	ErrProvidesSelfContainment      = errors.New("provides block references a root outside the self-contained set (.context.instance_id, .values, .infra, .locals)")
	ErrProvidesEvaluation           = errors.New("provides template evaluation failed")
	ErrSelfInjectForbiddenReference = errors.New("self-inject value: template references cross-node data")
	ErrSelfInjectKeyCollision       = errors.New("duplicate delivery key across self-inject and uses-inject")
	ErrAttachmentInjectKeyCollision = errors.New("duplicate delivery key across attachment-inject and another inject source")
	ErrUnmappedCustomMechanism      = errors.New("custom inject mechanism has no matching key in injectMapping output; add the mechanism's key to the Recipe injectMapping")

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
	ErrProvidesContractUnfulfilled = errors.New("realizer does not publish a provides key its abstract entity's schema.provides contract declares")

	// ErrMappingImportShape fires when a valuesMapping $import: (or a recursive
	// MappingLibrary entry body's field-root $import:) is neither a host-bearing
	// reference nor a well-formed bare-name `<Library>#/mappings/<entry>` identity.
	ErrMappingImportShape = errors.New("valuesMapping $import: is not a bare-name mapping-library reference (<Library>#/mappings/<entry>)")

	// ErrMappingLibraryNotFound fires when a bare-name mapping-library import
	// names a MappingLibrary absent from the resolved set (the local catalog plus
	// any lockfile-pinned libraries bridged in at load).
	ErrMappingLibraryNotFound = errors.New("mapping-library import references a MappingLibrary not present in the catalog")

	// ErrMappingEntryNotFound fires when a bare-name mapping-library import names
	// an entry absent from the resolved MappingLibrary's mappings map.
	ErrMappingEntryNotFound = errors.New("mapping-library import references an entry not present in the MappingLibrary")

	// ErrWorkloadImportShape fires when a workload $import: (or a recursive
	// WorkloadLibrary entry body's field-root $import:) is neither a host-bearing
	// reference nor a well-formed bare-name `<Library>#/workloads/<entry>` identity.
	ErrWorkloadImportShape = errors.New("workload $import: is not a bare-name workload-library reference (<Library>#/workloads/<entry>)")

	// ErrWorkloadLibraryNotFound fires when a bare-name workload-library import
	// names a WorkloadLibrary absent from the resolved set (the local catalog plus
	// any lockfile-pinned libraries bridged in at load).
	ErrWorkloadLibraryNotFound = errors.New("workload-library import references a WorkloadLibrary not present in the catalog")

	// ErrWorkloadEntryNotFound fires when a bare-name workload-library import names
	// an entry absent from the resolved WorkloadLibrary's workloads map.
	ErrWorkloadEntryNotFound = errors.New("workload-library import references an entry not present in the WorkloadLibrary")

	// ErrCircularImport is the $import-surface name for a mapping-library import
	// expansion cycle. It is the shared refcycle.ErrCycle sentinel — the same
	// identity the $ref surface reports via ErrCircularRef — so one detector
	// guards both surfaces; the recursive resolver maintains a stack of in-flight
	// canonical `<Library>#/mappings/<Entry>` keys and surfaces this with the
	// chain named in the message. Match with errors.Is(err, ErrCircularImport)
	// or, equivalently, errors.Is(err, refcycle.ErrCycle).
	ErrCircularImport = refcycle.ErrCycle

	// ErrHostInLibraryImport fires when a library $import: carries a
	// registry host — the `oci://…` form. It is shared by the valuesMapping
	// (`<Library>#/mappings/<entry>`) and workload
	// (`<Library>#/workloads/<entry>`) surfaces: a library import is addressed
	// by bare-name identity, never by physical registry location — the library
	// name resolves against the loaded catalog and the version is supplied by
	// the lockfile. A host in the reference is rejected so physical location
	// cannot leak into authored source. The per-surface wrapping context names
	// which surface carried the host.
	ErrHostInLibraryImport = errors.New("library $import: carries a registry host; reference the library by bare-name identity")

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
	ErrProviderNodeInputInvalid = errors.New("provider rejected node input")

	// ErrUnknownProvider is the compiler-level fatal halt produced by the
	// build-time engineFactory when a Recipe declares a provider name that
	// the CLI does not register. The CLI's engineFactory closure wraps this
	// sentinel with the offending provider name so callers can match via
	// errors.Is(err, ErrUnknownProvider) regardless of which provider
	// triggered the halt.
	ErrUnknownProvider = errors.New("unknown provider")

	// ErrPathCollision fires when two boundaries with the same evaluated
	// dispatch path target different providers AND the operator overrode
	// subDirectory: to defeat the per-provider segregation default. The
	// resulting per-(path, provider) dispatch produces multiple assembler
	// invocations whose on-disk outputs would collide. The error names the
	// conflicting providers, the conflicting boundaries, and the resolution
	// paths (restore the per-provider sub-path default, or change one
	// boundary's directory). Category: TRANSLATION_ERROR.
	ErrPathCollision = errors.New("dispatch path produced multiple provider outputs")

	// ErrInstanceIDCollision fires when two contributors from different
	// boundaries land in the same dispatch group (same evaluated path,
	// same provider) with the same component instance ID. Defensive
	// guardrail — topology flattening's prefix discipline should make
	// collisions impossible — but per the Loud Guardrails invariant the
	// assembler-input validation surfaces structural violations
	// immediately rather than letting them propagate. Category:
	// TRANSLATION_ERROR.
	ErrInstanceIDCollision = errors.New("multiple contributors share an instance ID at the same dispatch path")

	// ErrChartNameMismatch fires when two or more boundaries' contributing
	// components evaluate to the same dispatch path under a provider whose
	// assembler names its output from a boundary annotation and requires that
	// name to be unique, but their effective values for that annotation
	// (computed via the boundary annotation cascade) resolve to different
	// values. The merged assembly has exactly one name; cascade or declare
	// matching values explicitly. Category: TRANSLATION_ERROR.
	ErrChartNameMismatch = errors.New("merged-path assembly has conflicting assembly-name annotation values")

	// ErrPathTemplateInvalidField fires at parse time when a path-template
	// (Release.spec.boundaries[].directory or subDirectory) references a
	// field outside the documented allowlist. Available fields in
	// directory: templates are .capability, .boundary_name,
	// .parameters.<typed_keys>, .parent_boundary.*, .manifest.recipe_name;
	// subDirectory: additionally permits .component_id. Path templates use
	// stricter missing-key handling than value templates because silent
	// path failures produce broken outputs that operators only discover
	// post-build. Category: OPS_ERROR (operator-authored path template).
	ErrPathTemplateInvalidField = errors.New("path template references a field not in the documented allowlist")

	// ErrFRTemplateInvalidField fires at Recipe load time when a
	// FeatureRecipe with spec.executionScope: boundary references a
	// per-component-only field in any of its templates (workload,
	// injectMapping, provides, valuesMapping, artifacts). Boundary-scope
	// FRs use .boundary.* pillars in place of .context.* / .workload /
	// .secret_name; references to per-component fields are flagged at
	// parse time so the runtime never has to defend against shape
	// mismatches. Category: CATALOG_ERROR (catalog-authored FR
	// references invalid context for its scope).
	ErrFRTemplateInvalidField = errors.New("FeatureRecipe template references a per-component-only field with executionScope: boundary")

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
	ErrAssertionFailed = errors.New("assertion failed")

	// ErrPipelineTargetNotEmitted fires when a pipeline PATCH step addresses a
	// document the keyed set does not hold — a scope/key pair absent from the
	// node's emitted documents. It is the renderer's build-halting Loud
	// Guardrail promoting the pipeline walk's local absent-target cause: the
	// walk reports the typed condition; the integration halts the build with
	// this sentinel, naming the offending step and the addressed key.
	ErrPipelineTargetNotEmitted = errors.New("pipeline PATCH targets a document the node did not emit")

	// ErrPipelineHostScopeUnavailable fires when a pipeline step performs a
	// /host operation but the step has no host component — a boundary-scope
	// FeatureRecipe step (which has no single host) or a Recipe step (whose
	// only scope is its own output). The host root is absent for these
	// surfaces, so a /host patch or emit halts loudly rather than silently
	// resolving against an empty set.
	ErrPipelineHostScopeUnavailable = errors.New("pipeline /host operation from a step with no host component")

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
	ErrPipelinePatchPreAssembly = errors.New("pipeline PATCH targets an assembler provider's pre-assembly fragments")

	// ErrPluginProviderIncompatible fires when a pipeline step's plugin declares
	// an enumerated targets set that does not include the provider
	// rendering the scope the step touches — the recipe's own provider for an
	// own-scope step, the host component's provider for a host-scope step. The
	// list is an explicit tested-compatibility claim, not an implicit "any
	// similar provider"; running the plugin against an unlisted provider risks a
	// wrong patch, so the build halts loudly rather than proceeding. An empty or
	// absent set declares no compatibility claim, so it matches no provider and
	// every step that uses the plugin halts here. The check is pre-invoke (read
	// from describe() before any guest runs) and matches provider names exactly.
	// Category: CATALOG_ERROR.
	ErrPluginProviderIncompatible = errors.New("pipeline plugin is not compatible with the rendering provider")

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
	ErrBoundaryAssemblerMissing = errors.New("provider expected to assemble its dispatch group provides no boundary assembler")

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
	ErrArtifactContentMismatch = errors.New("imported artifact content does not match the recipe's recorded digest")

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
	ErrCrossProviderLinkUnsupported = errors.New("cross-boundary link between endpoints rendered by different providers is not supported")

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
	ErrRenderNondeterministic = errors.New("provider render is not deterministic")

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
	ErrOrphanCrossBoundaryLinkSentinel = errors.New("assembled output retains an unresolved cross-boundary link placeholder")
)
