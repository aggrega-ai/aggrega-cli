// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "errors"

var (
	ErrProvidesSelfContainment      = errors.New("provides block references cross-node data")
	ErrProvidesEvaluation           = errors.New("provides template evaluation failed")
	ErrSelfInjectForbiddenReference = errors.New("self-inject value: template references cross-node data")
	ErrSelfInjectKeyCollision       = errors.New("duplicate delivery key across self-inject and uses-inject")
	ErrAttachmentInjectKeyCollision = errors.New("duplicate delivery key across attachment-inject and another inject source")
	ErrUnmappedCustomMechanism      = errors.New("custom inject mechanism has no matching key in injectMapping output")

	// ErrLibraryImportNotYetSupported fires when a Recipe / FeatureRecipe
	// valuesMapping carries a mapping-library import (mode 3) — an
	// $import: of the bare-name identity form
	// `<Library>#/mappings/<entry>` into a MappingLibrary entry. The
	// reference is recognized; its render-time resolution is not yet
	// implemented. Until it ships, authors use a local file path import
	// (mode 2) as the workaround. The error message names the workaround
	// inline so authors aren't blocked.
	ErrLibraryImportNotYetSupported = errors.New("mapping-library import is recognized but not yet supported at render. Use a local file path import as a workaround (valuesMapping: { $import: ./relative/path.tmpl }) until library-import resolution lands.")

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
)
