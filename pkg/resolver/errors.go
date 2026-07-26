// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Sentinel errors for errors.Is matching. Every ResolverError wraps one of
// these in its Err field; callers compare via errors.Is rather than string
// equality on the Message text.
var (
	// ErrInvalidParameterRef wraps a Go-template parse or execution error
	// surfaced during parameter interpolation.
	ErrInvalidParameterRef = errors.New("invalid parameter reference: check the {{ .parameters.* }} expression names a declared parameter and parses as a Go template")

	// ErrParametersImportLoad wraps a read or unmarshal failure for the
	// single path supplied at Release.spec.parameters.$import:.
	ErrParametersImportLoad = errors.New("parameters $import: load failed")

	// ErrInvalidInstanceName wraps a user-authored dictionary key (component,
	// feature, secret, subsystem, aggregate name) that does not match the
	// instance-name pattern.
	ErrInvalidInstanceName = errors.New("invalid instance name: names must match ^[a-zA-Z0-9_-]+$ (letters, digits, hyphen, underscore; no dots)")

	// ErrInvalidTargetRef wraps a target reference (e.g., uses.target) whose
	// dot-separated segments do not all match the instance-name pattern.
	ErrInvalidTargetRef = errors.New("invalid target reference: each dot-separated segment must match ^[a-zA-Z0-9_-]+$")

	// ErrYAMLLoad wraps a file-read or YAML-unmarshal failure for any of the
	// three source documents (Aggregate, Release, Manifest).
	ErrYAMLLoad = errors.New("YAML load failed: check the file exists, is readable, and contains well-formed YAML")

	// ErrManifestReservedPrefix wraps a Manifest features.annotations
	// binding key that uses the reserved aggrega.ai/ prefix. The prefix
	// is reserved for the provider well-known annotations documented at
	// aggrega-docs/language-reference/providers/well-known-annotations.md;
	// authors of annotation-driven Feature bindings use a namespaced
	// sub-prefix (feature.aggrega.ai/ for standard-library shortcuts,
	// any custom domain for enterprise shortcuts). The rule is enforced
	// at Manifest load — the YAML parsed successfully but the binding
	// fails a semantic reservation check, distinct from ErrYAMLLoad.
	ErrManifestReservedPrefix = errors.New("manifest annotation binding uses reserved prefix")

	// ErrManifestCrossBindingPillarCollision wraps two Manifest
	// annotation bindings that target the same Feature URI with the
	// same (path, pillar) routing pair. Only one binding may declare
	// a given (path, pillar) pair per target Feature; multiple
	// bindings competing for the same routing slot cannot resolve
	// unambiguously at expansion time.
	ErrManifestCrossBindingPillarCollision = errors.New("manifest annotation bindings collide on same Feature path+pillar")

	// ErrInvalidInjectKey wraps an inject delivery key that does not match
	// the expected per-mechanism format.
	ErrInvalidInjectKey = errors.New("invalid inject key: the from-key must name a declared provides key of the target component type")

	// ErrSchemaConformance wraps a developer-authored component-instance
	// values or infra pillar that does not conform to the catalog
	// Component's declared schema for that pillar (wrong type, out of
	// range, missing required-non-defaulted field, or a property the
	// schema forbids). Validated at Step 1, default-aware: a field the
	// schema supplies a default for is not "missing". Distinct from
	// monotonic-narrowing, which stays signal-only.
	ErrSchemaConformance = errors.New("authored pillar does not conform to its declared schema")

	// ErrAttachmentSlotNearMiss marks an ADVISORY finding, not a halt: an
	// attachment whose name is not a declared slot on its component's type but
	// is within a small edit distance of one — the shape a slot-name typo
	// takes. An attachment name matching no slot is legal (the attachment
	// surface is open), so this sentinel never reaches a build's verdict; it
	// exists so a front end can recognise the advisory class and render it as a
	// warning.
	ErrAttachmentSlotNearMiss = errors.New("attachment name is a near miss for a declared slot")

	// ErrGuardEntityViewInconsistency wraps an internal-consistency failure: a
	// guard needs a catalog entity for a type that resolution resolved AS A
	// CATALOG REFERENCE (a bare-token identity, not a Tier-1 relative-path file),
	// yet the entity is absent from the guard's catalog view. A genuinely
	// unresolvable reference halts earlier at resolution; reaching a guard with
	// the entity missing means the build's resolved set and the guard's view have
	// diverged — a loud halt, never a silent skip. (Tier-1 relative-path types,
	// which legitimately have no catalog entity, are not this error.)
	ErrGuardEntityViewInconsistency = errors.New("guard catalog-entity view inconsistency")

	// ErrMissingRequiredFeature wraps a component whose declared catalog
	// type lists a requiredFeature that no attached Feature instance satisfies.
	ErrMissingRequiredFeature = errors.New("missing required feature: attach a Feature of the required type to the component")

	// ErrFeatureMergeConflict wraps a conflict during Release-side Feature
	// attachment merging (e.g., divergent values on the same Feature key).
	ErrFeatureMergeConflict = errors.New("feature merge conflict: reconcile the Aggregate and Release declarations for the feature")

	// ErrUnknownUsesSocket wraps a uses-block socket name that does not
	// exist in the catalog Component's usesSchema.
	ErrUnknownUsesSocket = errors.New("unknown uses socket: declare the socket in the component type's usesSchema, or use a socket it already declares")

	// ErrSelfInjectForbidden wraps a self-inject declaration on a component
	// whose catalog type does not declare a spec.inject allowlist.
	ErrSelfInjectForbidden = errors.New("self-inject on component without spec.inject allowlist")

	// ErrInjectKeyCollision wraps a delivery key declared on more than one
	// inject source for the same component.
	ErrInjectKeyCollision = errors.New("duplicate delivery key across inject sources")

	// ErrSchemaValidation wraps a pillar validation failure (.values
	// from the Aggregate, .infra from the Release boundary).
	ErrSchemaValidation = errors.New("schema validation failed: correct the flagged field to satisfy its declared schema")

	// ErrMissingRequiredSocket wraps a uses-socket marked required in the
	// Component's usesSchema that no instance populates.
	ErrMissingRequiredSocket = errors.New("required uses socket not populated")

	// ErrParameterMissing wraps a parameter declared required: true in the
	// Aggregate's typed contract that neither the Aggregate's default nor
	// the Release fulfillment supplies. Surfaced at Step 1a.
	ErrParameterMissing = errors.New("required parameter not supplied")

	// ErrParameterContractViolation wraps a parameter value that fails the
	// Aggregate's typed contract — type mismatch, enum / pattern / range
	// violation, validate-predicate failure, or a Release key with no
	// corresponding Aggregate declaration (closed-contract policy).
	// Surfaced at Step 1a.
	ErrParameterContractViolation = errors.New("parameter contract violated")

	// ErrUnnormalizedRelease fires at the resolve entry when a Release arrives
	// with a non-empty top-level components surface AND a synthesized `default`
	// boundary — an impossible state for a Release born through the normalization
	// core (`types.NormalizeRelease` folds the root into the default and clears
	// it), and the authored-both shape is envelope-rejected. It signals a bypass:
	// a hand-built `types.Release` literal or a load path that skipped the parse
	// core. An internal-invariant breach (a Loud Guardrail, Invariant 4)
	// backstopping the `NormalizedRelease` newtype. Asserted at resolveFromInputs
	// entry — the earliest point every boundary consumer (WireBoundaryInfra, the
	// release guards, the renderer via rr.Release) sits behind — so one check
	// protects the whole pipeline, not just the renderer.
	ErrUnnormalizedRelease = errors.New("release reached resolve un-normalized (non-empty root components alongside a synthesized default boundary)")
)

// ResolverError carries entity and field context alongside a sentinel error.
// Entity follows the convention "<Kind> <metadata.name>" (e.g.
// "Aggregate my-app", "Release prod"); Field is a JSON-path-style locator
// from the entity root (e.g. ".spec.components[\"web\"].features[\"audit\"]")
// or empty for whole-entity errors. Message is a human-readable description.
// Err carries one of the sentinel errors above so callers can match via
// errors.Is. Error() formats Entity, Field (if non-empty), and Message;
// Unwrap returns Err.
type ResolverError struct {
	Entity  string // e.g. "Aggregate my-app"
	Field   string // e.g. ".spec.components.bad name"
	Message string // human-readable description
	Err     error  // sentinel for errors.Is
}

func (e *ResolverError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s at %s: %s", e.Entity, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Entity, e.Message)
}

func (e *ResolverError) Unwrap() error {
	return e.Err
}

// MultiResolverError carries the per-violation slice when a resolver
// pass produces multiple ResolverError instances at once. Returned by
// aggregateParameterViolations (and any future multi-violation
// aggregator) in place of the previous single-summary ResolverError
// when len(violations) > 1.
//
// Implements the error interface with the same multi-line summary
// format the previous single-aggregate ResolverError produced so
// existing callers reading err.Error() see no change in human-
// readable output. Consumers needing the structured per-violation
// slice (e.g., the daemon's Tier-4 validate path mapping each
// violation to source positions) extract it via:
//
//	var multi *MultiResolverError
//	if errors.As(err, &multi) {
//	    for _, ve := range multi.Violations { ... }
//	}
//
// errors.Is matching against domain sentinels (e.g.,
// ErrParameterContractViolation) succeeds when ANY of the embedded
// violations wraps that sentinel — Unwrap returns the slice as a
// []error per Go 1.20+ multi-error convention.
type MultiResolverError struct {
	Entity     string           // e.g. "Aggregate my-app"
	Field      string           // typically a parent JSON-path like ".spec.parameters"
	Summary    string           // multi-line human-readable description
	Sentinel   error            // domain sentinel for errors.Is on the parent (e.g., ErrParameterContractViolation)
	Violations []*ResolverError // per-violation slice with structured Field locators preserved
}

// Error returns the multi-line summary so callers reading err.Error()
// see the same format aggregateParameterViolations produced before
// the structured-violations preservation refactor.
func (e *MultiResolverError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s at %s: %s", e.Entity, e.Field, e.Summary)
	}
	return fmt.Sprintf("%s: %s", e.Entity, e.Summary)
}

// Unwrap returns the per-violation slice as []error so
// errors.Is / errors.As match against any individual violation's
// wrapped sentinel. Tier-4 callers reading Violations directly bypass
// the unwrap chain entirely.
func (e *MultiResolverError) Unwrap() []error {
	out := make([]error, 0, len(e.Violations)+1)
	if e.Sentinel != nil {
		out = append(out, e.Sentinel)
	}
	for _, v := range e.Violations {
		out = append(out, v)
	}
	return out
}

// ToCompilerError converts the structured ResolverError into a
// *cerrors.CompilerError carrying the appropriate Category. The category
// is classified by the Entity prefix per cerrors.ClassifyEntityCategory:
// Aggregate → DeveloperError, Release → OpsError, catalog entities →
// CatalogError, unknown → DeveloperError (conservative).
//
// The Source location maps the JSON-path Field into the CompilerError's
// SourceLocation.File. Field is a JSON-path-style locator (e.g.,
// ".spec.components[\"web\"]"), not a real file path; the dual semantics
// is documented in pkg/errors's SourceLocation godoc as "the most
// specific authoring locator available, file path or JSON path."
//
// The inner error chain is preserved via cerrors.Wrap, so errors.Is
// against the resolver's sentinel errors (ErrYAMLLoad, ErrSchemaValidation,
// etc.) continues to match through the converted CompilerError.
func (e *ResolverError) ToCompilerError() *cerrors.CompilerError {
	cat := cerrors.ClassifyEntityCategory(e.Entity)
	source := cerrors.SourceLocation{File: e.Field}
	msg := fmt.Sprintf("%s: %s", e.Entity, e.Message)
	return cerrors.Wrap(e, cat, source, msg)
}
