// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
)

var (
	ErrProvidesSelfContainment               = errors.New("provides block references a root outside the self-contained set (.context.instance_id, .values, .infra, .locals)")
	ErrProvidesEvaluation                    = errors.New("provides template evaluation failed")
	ErrCredentialProvidesContractUnfulfilled = errors.New("credential FeatureRecipe does not publish a provides key its Feature schema.provides contract declares")
	ErrSelfInjectForbiddenReference          = errors.New("self-inject value: template references cross-node data")
	ErrSelfInjectKeyCollision                = errors.New("duplicate delivery key across self-inject and uses-inject")
	ErrAttachmentInjectKeyCollision          = errors.New("duplicate delivery key across attachment-inject and another inject source")
	ErrUnmappedCustomMechanism               = errors.New("custom inject mechanism has no matching key in injectMapping output")

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

	// ErrCircularImport is the $import-surface name for a mapping-library import
	// expansion cycle. It is the shared refcycle.ErrCycle sentinel — the same
	// identity the $ref surface reports via ErrCircularRef — so one detector
	// guards both surfaces; the recursive resolver maintains a stack of in-flight
	// canonical `<Library>#/mappings/<Entry>` keys and surfaces this with the
	// chain named in the message. Match with errors.Is(err, ErrCircularImport)
	// or, equivalently, errors.Is(err, refcycle.ErrCycle).
	ErrCircularImport = refcycle.ErrCycle

	// ErrHostInLibraryImport fires when a valuesMapping $import: carries a
	// registry host — the `oci://…` form. A mapping-library import is
	// addressed by bare-name identity (`<Library>#/mappings/<entry>`),
	// never by physical registry location: the library name resolves
	// against the loaded closure and the version is supplied by the
	// lockfile. A host in the reference is rejected so physical location
	// cannot leak into authored source.
	ErrHostInLibraryImport = errors.New("valuesMapping $import: carries a registry host; reference the mapping library by identity (valuesMapping: { $import: <Library>#/mappings/<entry> })")

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
	// components evaluate to the same dispatch path under the helm-chart
	// provider but their effective aggrega.ai/chart-name annotations
	// (computed via the boundary annotation cascade) resolve to different
	// values. The merged umbrella chart has exactly one name; cascade or
	// declare matching values explicitly. Category: TRANSLATION_ERROR.
	ErrChartNameMismatch = errors.New("merged-path helm-chart umbrella has conflicting aggrega.ai/chart-name values")

	// ErrPathTemplateInvalidField fires at parse time when a path-template
	// (Release.spec.boundaries[].directory or subDirectory) references a
	// field outside the documented allowlist. Available fields in
	// directory: templates are .provider, .boundary_name,
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

	// ErrPipelinePatchTargetNotStructured marks an emitted file that a
	// pipeline PATCH step addresses but which is not parseable structured
	// YAML/JSON, so it has no addressable document tree. The segmenter
	// surfaces this typed error to the pipeline caller, which halts with a
	// Loud Guardrail rather than silently skipping the operation. An emitted
	// file that is only ever an EMIT side effect — never a PATCH target — is
	// not segmented, so a non-structured file is an error only when a step
	// tries to address a document inside it.
	ErrPipelinePatchTargetNotStructured = errors.New("pipeline PATCH target is not parseable structured YAML/JSON")

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
	// an enumerated compatibleProviders set that does not include the provider
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
)
