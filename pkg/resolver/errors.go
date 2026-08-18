// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Sentinel errors for errors.Is matching. Every ResolverError wraps one of
// these in its Err field; callers compare via errors.Is rather than string
// equality on the Message text.

// ErrInvalidParameterRef wraps a Go-template parse or execution error
// surfaced during parameter interpolation.
func ErrInvalidParameterRef() error { return errInvalidParameterRef }

var errInvalidParameterRef = cerrors.Register("resolver/invalid-parameter-ref", cerrors.Meta{
	Message: "invalid parameter reference: check the {{ .parameters.* }} expression names a declared parameter and parses as a Go template",
})

// ErrParametersImportLoad wraps a read or unmarshal failure for the
// single path supplied at Release.spec.parameters.$import:.
func ErrParametersImportLoad() error { return errParametersImportLoad }

var errParametersImportLoad = cerrors.Register("resolver/parameters-import-load", cerrors.Meta{
	Message: "parameters $import: load failed",
})

// ErrInvalidInstanceName wraps a user-authored dictionary key (component,
// feature, secret, subsystem, aggregate name) that does not match the
// instance-name pattern.
func ErrInvalidInstanceName() error { return errInvalidInstanceName }

var errInvalidInstanceName = cerrors.Register("resolver/invalid-instance-name", cerrors.Meta{
	Message: "invalid instance name: names must match ^[a-zA-Z0-9_-]+$ (letters, digits, hyphen, underscore; no dots)",
})

// ErrInvalidTargetRef wraps a target reference (e.g., uses.target) whose
// dot-separated segments do not all match the instance-name pattern.
func ErrInvalidTargetRef() error { return errInvalidTargetRef }

var errInvalidTargetRef = cerrors.Register("resolver/invalid-target-ref", cerrors.Meta{
	Message: "invalid target reference: each dot-separated segment must match ^[a-zA-Z0-9_-]+$",
})

// ErrYAMLLoad wraps a file-read or YAML-unmarshal failure for any of the
// three source documents (Aggregate, Release, Manifest).
func ErrYAMLLoad() error { return errYAMLLoad }

var errYAMLLoad = cerrors.Register("resolver/yaml-load", cerrors.Meta{
	Message: "YAML load failed: check the file exists, is readable, and contains well-formed YAML",
})

// ErrManifestReservedPrefix wraps a Manifest features.annotations
// binding key that uses the reserved aggrega.ai/ prefix. The prefix
// is reserved for the provider well-known annotations documented at
// aggrega-docs/language-reference/providers/well-known-annotations.md;
// authors of annotation-driven Feature bindings use a namespaced
// sub-prefix (feature.aggrega.ai/ for standard-library shortcuts,
// any custom domain for enterprise shortcuts). The rule is enforced
// at Manifest load — the YAML parsed successfully but the binding
// fails a semantic reservation check, distinct from ErrYAMLLoad.
func ErrManifestReservedPrefix() error { return errManifestReservedPrefix }

var errManifestReservedPrefix = cerrors.Register("resolver/manifest-reserved-prefix", cerrors.Meta{
	Message: "manifest annotation binding uses reserved prefix",
})

// ErrManifestCrossBindingPillarCollision wraps two Manifest
// annotation bindings that target the same Feature URI with the
// same (path, pillar) routing pair. Only one binding may declare
// a given (path, pillar) pair per target Feature; multiple
// bindings competing for the same routing slot cannot resolve
// unambiguously at expansion time.
func ErrManifestCrossBindingPillarCollision() error { return errManifestCrossBindingPillarCollision }

var errManifestCrossBindingPillarCollision = cerrors.Register("resolver/manifest-cross-binding-pillar-collision", cerrors.Meta{
	Message: "manifest annotation bindings collide on same Feature path+pillar",
})

// ErrInvalidInjectKey wraps an inject delivery key that does not match
// the expected per-mechanism format.
func ErrInvalidInjectKey() error { return errInvalidInjectKey }

var errInvalidInjectKey = cerrors.Register("resolver/invalid-inject-key", cerrors.Meta{
	Message: "invalid inject key: the from-key must name a declared provides key of the target component type",
})

// ErrSchemaConformance wraps a developer-authored component-instance
// values or infra pillar that does not conform to the catalog
// Component's declared schema for that pillar (wrong type, out of
// range, missing required-non-defaulted field, or a property the
// schema forbids). Validated during resolution, default-aware: a field the
// schema supplies a default for is not "missing". Distinct from
// monotonic-narrowing, which stays signal-only.
func ErrSchemaConformance() error { return errSchemaConformance }

var errSchemaConformance = cerrors.Register("resolver/schema-conformance", cerrors.Meta{
	Message: "authored pillar does not conform to its declared schema",
})

// ErrAttachmentSlotShape wraps an attachment that binds a declared slot
// but does not match that slot's declared shape: a key the slot does not
// permit, or an inject delivery mechanism outside the slot's allowlist
// (including any mechanism at all on a Black-Box slot, whose empty
// allowlist reserves delivery to the Recipe). In both cases the shape
// checked is the one the host Component's featuresSchema projects.
//
// It reaches an attachment by either route it can arrive on, and the two
// are checked at different points because the slot becomes known at
// different points:
//
//   - Declared on the component instance itself — checked during
//     resolution, against the authored block.
//   - Contributed by a broader scope and cascaded onto the component —
//     checked once the component's attachment set is complete, because
//     which slot closes the fill is a property of the component it
//     reached rather than of the scope it was written at. The same
//     contributed fill can be legal on one component in the scope and
//     out of shape on another; the diagnostic names the component and
//     the slot, and locates the attachment at the scope it was authored
//     at.
//
// It is a SHAPE sentinel only. Whether a slot declared `required: true`
// ends up bound is a different question, asked of the attachment set a
// node holds after the whole cascade, and it is never reported here.
func ErrAttachmentSlotShape() error { return errAttachmentSlotShape }

var errAttachmentSlotShape = cerrors.Register("resolver/attachment-slot-shape", cerrors.Meta{
	Message: "attachment does not conform to its declared slot shape",
})

// ErrAttachmentSlotNearMiss marks an ADVISORY finding, not a halt: an
// attachment whose name is not a declared slot on its component's type but
// is within a small edit distance of one — the shape a slot-name typo
// takes. An attachment name matching no slot is legal (the attachment
// surface is open), so this sentinel never reaches a build's verdict; it
// exists so a front end can recognise the advisory class and render it as a
// warning.
func ErrAttachmentSlotNearMiss() error { return errAttachmentSlotNearMiss }

var errAttachmentSlotNearMiss = cerrors.Register("resolver/attachment-slot-near-miss", cerrors.Meta{
	Message: "attachment name is a near miss for a declared slot",
})

// ErrUsesSocketNearMiss marks an ADVISORY finding, not a halt: a socket
// named on a component's own uses block that its Component type does not
// declare in usesSchema but is within a small edit distance of one — the
// shape a socket-name typo takes. A socket name matching no declared socket
// is legal on that surface (the Aggregate author owns the topology and may
// name a socket the catalog did not anticipate), so this sentinel never
// reaches a build's verdict; it exists so a front end can recognise the
// advisory class and render it as a warning.
func ErrUsesSocketNearMiss() error { return errUsesSocketNearMiss }

var errUsesSocketNearMiss = cerrors.Register("resolver/uses-socket-near-miss", cerrors.Meta{
	Message: "uses socket name is a near miss for a declared socket",
})

// ErrProvidesKeyNotDeclared marks an ADVISORY finding, not a halt: a bound
// Recipe publishes a provides key its Component does not declare in
// spec.schema. Publishing more than the Component declares is legal — the
// provides contract is subset-required and superset-permitted — so this
// sentinel never reaches a build's verdict; it exists so a front end can
// recognise the advisory class and render it as a warning. The key is not
// delivered across a uses edge, which reads the Component's declared contract,
// and the advisory names that consequence where it is caused.
func ErrProvidesKeyNotDeclared() error { return errProvidesKeyNotDeclared }

var errProvidesKeyNotDeclared = cerrors.Register("resolver/provides-key-not-declared", cerrors.Meta{
	Message: "Recipe publishes a provides key its Component does not declare",
})

// ErrUnreadParameter marks an ADVISORY finding, not a halt: a parameter the
// Aggregate declares that no reference in the Aggregate's own text reads.
// Declaring a parameter nothing reads is legal — the compiler does not police
// which knobs an author exposes — so this sentinel never reaches a build's
// verdict; it exists so a front end can recognise the advisory class and
// render it as a warning.
func ErrUnreadParameter() error { return errUnreadParameter }

var errUnreadParameter = cerrors.Register("resolver/unread-parameter", cerrors.Meta{
	Message: "declared parameter is read by no reference in the Aggregate",
})

// ErrGuardEntityViewInconsistency wraps an internal-consistency failure: a
// guard needs a catalog entity for a type that resolution resolved AS A
// CATALOG REFERENCE (a bare-token identity, not a Tier-1 relative-path file),
// yet the entity is absent from the guard's catalog view. A genuinely
// unresolvable reference halts earlier at resolution; reaching a guard with
// the entity missing means the build's resolved set and the guard's view have
// diverged — a loud halt, never a silent skip. (Tier-1 relative-path types,
// which legitimately have no catalog entity, are not this error.)
func ErrGuardEntityViewInconsistency() error { return errGuardEntityViewInconsistency }

var errGuardEntityViewInconsistency = cerrors.Register("resolver/guard-entity-view-inconsistency", cerrors.Meta{
	Message: "guard catalog-entity view inconsistency",
})

// ErrMissingRequiredFeature wraps a component whose declared catalog type
// carries a presence mandate — a features.aggrega.ai/requiredType.<label>
// annotation — that no attachment in the node's final set satisfies.
//
// It is declared here and raised from the package that holds the rule, which is
// the one point in the compilation where both halves of the question are in
// hand: the mandate the catalog entity declares, and the attachment set the node
// ends up with once every scope has folded. The finding's detail carries the
// component type declaring the mandate, the type it demands and the label it
// carries, so a finding says which declaration made the demand as well as what
// would satisfy it.
func ErrMissingRequiredFeature() error { return errMissingRequiredFeature }

var errMissingRequiredFeature = cerrors.Register("resolver/missing-required-feature", cerrors.Meta{
	Message: "missing required feature: attach a Feature of the required type to the component",
})

// ErrFeatureMergeConflict wraps a conflict during Release-side Feature
// attachment merging (e.g., divergent values on the same Feature key).
func ErrFeatureMergeConflict() error { return errFeatureMergeConflict }

var errFeatureMergeConflict = cerrors.Register("resolver/feature-merge-conflict", cerrors.Meta{
	Message: "feature merge conflict: reconcile the Aggregate and Release declarations for the feature",
})

// ErrUnknownUsesSocket wraps a socket name that the entity owning the surface
// does not declare in its usesSchema, on a surface that may only name a
// declared socket: a Feature attachment's uses (the socket must be on the
// attached Feature) and a Release per-edge uses (the socket must be on the
// consuming component). A component's own uses block is NOT one of those
// surfaces — an Aggregate author may name a socket the catalog did not
// declare — so this sentinel no longer fires for it.
func ErrUnknownUsesSocket() error { return errUnknownUsesSocket }

var errUnknownUsesSocket = cerrors.Register("resolver/unknown-uses-socket", cerrors.Meta{
	Message: "unknown uses socket: name a socket the entity declares in its usesSchema — only a component's own uses block may name one the catalog did not declare",
})

// ErrSelfInjectForbidden wraps a self-inject declaration on a component
// whose catalog type does not declare a spec.inject allowlist.
func ErrSelfInjectForbidden() error { return errSelfInjectForbidden }

var errSelfInjectForbidden = cerrors.Register("resolver/self-inject-forbidden", cerrors.Meta{
	Message: "self-inject on component without spec.inject allowlist",
})

// ErrInjectKeyCollision wraps a delivery key declared on more than one
// inject source for the same component.
func ErrInjectKeyCollision() error { return errInjectKeyCollision }

var errInjectKeyCollision = cerrors.Register("resolver/inject-key-collision", cerrors.Meta{
	Message: "duplicate delivery key across inject sources",
})

// ErrMissingRequiredSocket wraps a uses-socket marked required in the
// Component's usesSchema that no instance populates.
func ErrMissingRequiredSocket() error { return errMissingRequiredSocket }

var errMissingRequiredSocket = cerrors.Register("resolver/missing-required-socket", cerrors.Meta{
	Message: "required uses socket not populated",
})

// ErrParameterMissing wraps a parameter declared required: true in the
// Aggregate's typed contract that neither the Aggregate's default nor
// the Release fulfillment supplies. Surfaced by parameter-contract
// validation, which checks every supplied parameter against the declared
// typed contract before interpolation — so a missing required parameter
// halts the build instead of interpolating an empty value.
func ErrParameterMissing() error { return errParameterMissing }

var errParameterMissing = cerrors.Register("resolver/parameter-missing", cerrors.Meta{
	Message: "required parameter not supplied",
})

// ErrParameterContractViolation wraps a parameter value that fails the
// Aggregate's typed contract — type mismatch, enum / pattern / range
// violation, validate-predicate failure, or a Release key with no
// corresponding Aggregate declaration (closed-contract policy).
// Surfaced by parameter-contract validation, which runs after the Release
// parameter load and before parameter interpolation.
func ErrParameterContractViolation() error { return errParameterContractViolation }

var errParameterContractViolation = cerrors.Register("resolver/parameter-contract-violation", cerrors.Meta{
	Message: "parameter contract violated",
})

// ErrDuplicateBoundaryComponent wraps a Release that places one component in
// more than one boundary of its boundary tree. The two placements are not merged
// and not ranked: a component's boundary context (output directory,
// sub-directory, sync wave, annotations, boundary infra) and its per-edge `uses`
// surface are read by separate walks that resolve a duplicated name to different
// declarations, so one node would take its output location from one and its
// per-edge configuration from another. Rejected at Release load rather than
// resolved silently — the diagnostic names both sites so the author can delete
// the one they did not mean.
//
// A boundary places components two ways, and both count: naming a component
// directly, and routing in a whole sub-module, which places every component that
// module contains. So this also covers the same sub-module routed into two
// boundaries, a module routed into one boundary with a module nested inside it
// routed into another, and a component named at one boundary while the module
// containing it is routed into a different one. Routing a module and naming one
// of its components under the same boundary is not a conflict: both agree on
// where the component belongs.
func ErrDuplicateBoundaryComponent() error { return errDuplicateBoundaryComponent }

var errDuplicateBoundaryComponent = cerrors.Register("resolver/duplicate-boundary-component", cerrors.Meta{
	Message: "component declared in more than one boundary: declare each component in exactly one boundary",
})

// ErrUnnormalizedRelease fires at the resolve entry when a Release arrives
// with a non-empty top-level components surface AND a synthesized `default`
// boundary — an impossible state for a Release born through the normalization
// core (`types.NormalizeRelease` folds the root into the default and clears
// it), and the authored-both shape is envelope-rejected. It signals a bypass:
// a hand-built `types.Release` literal or a load path that skipped the parse
// core. An internal-invariant breach (a Loud Guardrail, Invariant 4)
// backstopping the `NormalizedRelease` type. Asserted at resolveFromInputs
// entry — the earliest point every boundary consumer (WireBoundaryInfra, the
// release guards, the renderer via rr.Release) sits behind — so one check
// protects the whole pipeline, not just the renderer.
func ErrUnnormalizedRelease() error { return errUnnormalizedRelease }

var errUnnormalizedRelease = cerrors.Register("resolver/unnormalized-release", cerrors.Meta{
	Message: "release reached resolve un-normalized (non-empty root components alongside a synthesized default boundary)",
})

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
// against the resolver's sentinel errors (ErrYAMLLoad, ErrSchemaConformance,
// etc.) continues to match through the converted CompilerError.
func (e *ResolverError) ToCompilerError() *cerrors.CompilerError {
	cat := cerrors.ClassifyEntityCategory(e.Entity)
	source := cerrors.SourceLocation{File: e.Field}
	msg := fmt.Sprintf("%s: %s", e.Entity, e.Message)
	return cerrors.Wrap(e, cat, source, msg)
}
