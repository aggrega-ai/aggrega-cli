// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Sentinel errors for errors.Is matching. Every ResolverError wraps one of
// these in its Err field; callers compare via errors.Is rather than string
// equality on the Message text.

// ErrInvalidParameterRef wraps a Go-template parse or execution error
// surfaced during parameter interpolation.
func ErrInvalidParameterRef() error { return errInvalidParameterRef }

var errInvalidParameterRef = cerrors.Register("resolver/invalid-parameter-ref", cerrors.Meta{
	ShortCode: "E0158",
	Message:   "invalid parameter reference: check the {{ .parameters.* }} expression names a declared parameter and parses as a Go template",
})

// ErrParametersImportLoad wraps a read or unmarshal failure for the
// single path supplied at Release.spec.parameters.$import:.
func ErrParametersImportLoad() error { return errParametersImportLoad }

var errParametersImportLoad = cerrors.Register("resolver/parameters-import-load", cerrors.Meta{
	ShortCode: "E0166",
	Message:   "parameters $import: load failed",
})

// ErrInvalidInstanceName wraps a user-authored dictionary key (component,
// feature, secret, subsystem, aggregate name) that does not match the
// instance-name pattern.
func ErrInvalidInstanceName() error { return errInvalidInstanceName }

var errInvalidInstanceName = cerrors.Register("resolver/invalid-instance-name", cerrors.Meta{
	ShortCode: "E0157",
	Message:   "invalid instance name: names must match ^[a-zA-Z0-9_-]+$ (letters, digits, hyphen, underscore; no dots)",
})

// ErrYAMLLoad wraps a file-read or YAML-unmarshal failure for any of the
// three source documents (Aggregate, Release, Manifest).
func ErrYAMLLoad() error { return errYAMLLoad }

var errYAMLLoad = cerrors.Register("resolver/yaml-load", cerrors.Meta{
	ShortCode: "E0175",
	Message:   "YAML load failed: check the file exists, is readable, and contains well-formed YAML",
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
	ShortCode: "E0161",
	Message:   "manifest annotation binding uses reserved prefix",
})

// ErrManifestCrossBindingPillarCollision wraps two Manifest
// annotation bindings that target the same Feature URI with the
// same (path, pillar) routing pair. Only one binding may declare
// a given (path, pillar) pair per target Feature; multiple
// bindings competing for the same routing slot cannot resolve
// unambiguously at expansion time.
func ErrManifestCrossBindingPillarCollision() error { return errManifestCrossBindingPillarCollision }

var errManifestCrossBindingPillarCollision = cerrors.Register("resolver/manifest-cross-binding-pillar-collision", cerrors.Meta{
	ShortCode: "E0160",
	Message:   "manifest annotation bindings collide on same Feature path+pillar",
})

// ErrInvalidInjectKey wraps an inject delivery key that does not match
// the expected per-mechanism format.
func ErrInvalidInjectKey() error { return errInvalidInjectKey }

var errInvalidInjectKey = cerrors.Register("resolver/invalid-inject-key", cerrors.Meta{
	ShortCode: "E0156",
	Message:   "invalid inject key: the from-key must name a declared provides key of the target component type",
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
	ShortCode: "E0168",
	Message:   "authored pillar does not conform to its declared schema",
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
	ShortCode: "E0150",
	Message:   "attachment does not conform to its declared slot shape",
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
	ShortCode: "E0149",
	Message:   "attachment name is a near miss for a declared slot",
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
	ShortCode: "E0174",
	Message:   "uses socket name is a near miss for a declared socket",
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
	ShortCode: "E0167",
	Message:   "Recipe publishes a provides key its Component does not declare",
})

// ErrUnreadParameter marks an ADVISORY finding, not a halt: a parameter the
// Aggregate declares that no reference in the Aggregate's own text reads.
// Declaring a parameter nothing reads is legal — the compiler does not police
// which knobs an author exposes — so this sentinel never reaches a build's
// verdict; it exists so a front end can recognise the advisory class and
// render it as a warning.
func ErrUnreadParameter() error { return errUnreadParameter }

var errUnreadParameter = cerrors.Register("resolver/unread-parameter", cerrors.Meta{
	ShortCode: "E0173",
	Message:   "declared parameter is read by no reference in the Aggregate",
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
	ShortCode: "E0154",
	Message:   "guard catalog-entity view inconsistency",
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
	ShortCode: "E0162",
	Message:   "missing required feature: attach a Feature of the required type to the component",
})

// ErrFeatureMergeConflict wraps a conflict during Release-side Feature
// attachment merging (e.g., divergent values on the same Feature key).
func ErrFeatureMergeConflict() error { return errFeatureMergeConflict }

var errFeatureMergeConflict = cerrors.Register("resolver/feature-merge-conflict", cerrors.Meta{
	ShortCode: "E0153",
	Message:   "feature merge conflict: reconcile the Aggregate and Release declarations for the feature",
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
	ShortCode: "E0171",
	Message:   "unknown uses socket: name a socket the entity declares in its usesSchema — only a component's own uses block may name one the catalog did not declare",
})

// ErrSelfInjectForbidden wraps a self-inject declaration on a component
// whose catalog type does not declare a spec.inject allowlist.
func ErrSelfInjectForbidden() error { return errSelfInjectForbidden }

var errSelfInjectForbidden = cerrors.Register("resolver/self-inject-forbidden", cerrors.Meta{
	ShortCode: "E0169",
	Message:   "self-inject on component without spec.inject allowlist",
})

// ErrInjectKeyCollision wraps a delivery key declared on more than one
// inject source for the same component.
func ErrInjectKeyCollision() error { return errInjectKeyCollision }

var errInjectKeyCollision = cerrors.Register("resolver/inject-key-collision", cerrors.Meta{
	ShortCode: "E0155",
	Message:   "duplicate delivery key across inject sources",
})

// ErrMissingRequiredSocket wraps a uses-socket marked required in the
// Component's usesSchema that no instance populates.
func ErrMissingRequiredSocket() error { return errMissingRequiredSocket }

var errMissingRequiredSocket = cerrors.Register("resolver/missing-required-socket", cerrors.Meta{
	ShortCode: "E0163",
	Message:   "required uses socket not populated",
})

// ErrParameterMissing wraps a parameter declared required: true in the
// Aggregate's typed contract that neither the Aggregate's default nor
// the Release fulfillment supplies. Surfaced by parameter-contract
// validation, which checks every supplied parameter against the declared
// typed contract before interpolation — so a missing required parameter
// halts the build instead of interpolating an empty value.
func ErrParameterMissing() error { return errParameterMissing }

var errParameterMissing = cerrors.Register("resolver/parameter-missing", cerrors.Meta{
	ShortCode: "E0165",
	Message:   "required parameter not supplied",
})

// ErrParameterContractViolation wraps a parameter value that fails the
// Aggregate's typed contract — type mismatch, enum / pattern / range
// violation, validate-predicate failure, or a Release key with no
// corresponding Aggregate declaration (closed-contract policy).
// Surfaced by parameter-contract validation, which runs after the Release
// parameter load and before parameter interpolation.
func ErrParameterContractViolation() error { return errParameterContractViolation }

var errParameterContractViolation = cerrors.Register("resolver/parameter-contract-violation", cerrors.Meta{
	ShortCode: "E0164",
	Message:   "parameter contract violated",
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
	ShortCode: "E0151",
	Message:   "component declared in more than one boundary: declare each component in exactly one boundary",
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
	ShortCode: "E0172",
	Message:   "release reached resolve un-normalized (non-empty root components alongside a synthesized default boundary)",
})

// ErrVerdictPluginTrustMismatch reports that the digest recorded as trusted for a
// plugin returning an accept-or-reject verdict disagrees with the digest that
// plugin's cached manifest reports for the same identity. The two are compared
// before the executable is read, and a disagreement halts the resolution.
//
// The comparison is what makes the recorded digest an authority rather than a
// restatement. Without a recorded digest, a verdict plugin's bytes are checked
// against a pin derived from the very manifest whose bytes it then validates —
// self-consistent by construction, and therefore unable to notice that the whole
// artifact has been replaced. Comparing the recorded digest against the
// manifest-derived one is what turns that into a check with an outside opinion,
// and this sentinel names the case where the two opinions differ.
//
// It is distinct from a content-digest mismatch (oci.ErrFetchIntegrity), which
// reports resolved BYTES disagreeing with the digest claimed for them: here no
// bytes have been read, and they may well agree with the manifest — what
// disagrees is which artifact is being resolved at all. Keeping the two apart is
// the point, because the remediations are opposite: one is a corrupted or
// tampered artifact, the other is a legitimately changed plugin whose new
// identity has not been reviewed and recorded yet.
//
// The recorded digest reaches the comparison as an ordinary per-plugin pin, so
// this package neither reads nor names the file the record lives in; the
// remediation is to review what changed and re-record the trusted identity, or
// to restore the artifact the record names.
func ErrVerdictPluginTrustMismatch() error { return errVerdictPluginTrustMismatch }

var errVerdictPluginTrustMismatch = cerrors.Register("resolver/verdict-plugin-trust-mismatch", cerrors.Meta{
	ShortCode: "E0246",
	Message:   "the digest recorded as trusted for this plugin does not match the digest its cached manifest reports",
})

// ResolverError carries entity and field context alongside a sentinel error.
// Entity follows the convention "<Kind> <metadata.name>" (e.g.
// "Aggregate my-app", "Release prod"); Field is a JSON-path-style locator
// from the entity root (e.g. ".spec.components[\"web\"].features[\"audit\"]")
// or empty for whole-entity errors. Err carries one of the sentinel errors
// above so callers can match via errors.Is; Unwrap returns it.
//
// Message is what THIS occurrence supplied and nothing more — the key at fault,
// the value that was read, what the check found. It is not a description of the
// condition: that sentence is registered beside the condition's identity, and
// Error reads it off Err rather than from anything a raise site writes. A raise
// site that spells the condition out here would be writing a second copy of a
// wording that already has a home, and the two would agree until one was
// edited.
//
// A raise site that has nothing of its own to add leaves Message empty. That is
// the shape for wrapping an error that is already a composed diagnostic for the
// same condition: see Error for what such a finding renders.
type ResolverError struct {
	Entity  string // e.g. "Aggregate my-app"
	Field   string // e.g. ".spec.components.bad name"
	Message string // what this occurrence supplied, not a description of the condition
	Err     error  // sentinel for errors.Is
}

// subjectValues renders the subject a finding is attributed to as the values a
// composed message carries: the entity reference first, the locator after it,
// each omitted when the finding carries none.
//
// It is one function rather than the same two appends in three methods because
// the order those two values read in is part of the composed shape, and three
// copies of it would agree until one was edited.
func subjectValues(entity, field string) []string {
	values := make([]string, 0, 3)
	if entity != "" {
		values = append(values, entity)
	}
	if field != "" {
		values = append(values, "at "+field)
	}
	return values
}

// Error renders the finding in the one composition every diagnostic naming a
// condition follows: the condition's registered sentence first, then what this
// occurrence supplied, read through [cerrors.ComposeMessage].
//
// The subject — the entity reference and the locator — is one of those values
// rather than a prefix ahead of the sentence. Putting it in front would demote
// the sentence to the middle of the line, and the sentence is the only part
// that says which condition was met: a reader scanning a wall of diagnostics
// reads the leftmost words of each, and an entity name is the part they already
// know.
//
// A finding that supplied no Message contributed nothing of its own, and the
// error it wraps is then the whole diagnostic — a report already composed for
// this same condition by the surface that raised it, opening with the same
// registered sentence and naming its own subject. Such a finding renders that
// report as it stands rather than composing a second time, which would either
// restate the sentence the report already opens with or drop the report's body
// to avoid restating it. Which of the two shapes a finding takes follows from
// the field the raise site populated and never from reading the text, so a
// message that happens to begin with its own condition's sentence cannot be
// mistaken for either. The subject still travels — structurally, on the finding
// and into the source pointer ToCompilerError builds — which is where it
// travels at every other boundary that composes this way.
func (e *ResolverError) Error() string {
	return e.compose(subjectValues(e.Entity, e.Field))
}

// compose renders the finding over the subject values the calling surface wants
// carried in the text, and holds the arms both renderings share so neither can
// drift from the other: which values follow the sentence, and what a finding
// that supplied no Message of its own renders.
func (e *ResolverError) compose(values []string) string {
	if e.Message == "" && e.Err != nil {
		return e.Err.Error()
	}
	if e.Message != "" {
		values = append(values, e.Message)
	}
	return cerrors.ComposeMessage(e.Err, values, "")
}

// MessageWithoutSubject renders the finding for a surface that shows the subject
// itself: the condition's registered sentence and what this occurrence supplied,
// with the entity reference and the locator left out.
//
// An editor placing a diagnostic on the offending key already draws the file and
// the position, and highlights the token; spelling the same path again inside the
// sentence would say it twice in the one place a reader cannot scroll away from.
// A terminal has nowhere else to put the subject, which is why Error carries it
// and this does not.
//
// It is Error's composition minus the subject and not a second composition: both
// read the wording off Err through the one composer, so a condition keeps one
// wording however many surfaces report it, and the empty-Message arm behaves the
// same way in both.
func (e *ResolverError) MessageWithoutSubject() string {
	return e.compose(nil)
}

func (e *ResolverError) Unwrap() error {
	return e.Err
}

// MultiResolverError carries the per-violation slice when a resolver
// pass produces several ResolverError instances at once, so a document
// offending in several places is reported once for all of them rather
// than once for whichever was found first.
//
// It renders as a header and the findings beneath it — see Error for the
// two shapes a group takes and why. Summary is the findings half only:
// the lines that go beneath the header, never a description of the
// condition, which Error composes from Sentinel.
//
// Consumers needing the structured per-violation
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
	Summary    string           // the per-finding lines that go beneath the composed header
	Sentinel   error            // domain sentinel for errors.Is on the parent (e.g., ErrParameterContractViolation)
	Violations []*ResolverError // per-violation slice with structured Field locators preserved
}

// Error renders the group as a header and the findings beneath it, which is the
// shape [cerrors.ComposeMessage] is built for when one report carries many
// findings: the header is composed — the condition's registered sentence, then
// this occurrence's subject — and Summary's per-finding lines follow it.
//
// A group carrying no Sentinel is a roll-up rather than a condition: it names a
// stage or a conformance pass, counts what that pass found, and leaves the
// conditions to the individual findings in Violations. There is no registered
// sentence for such a group to open with, so it opens with its subject. That is
// not an exception to the composition — it is what the composition renders when
// the chain carries no identity, and it is the reason Sentinel is read here
// rather than a sentence being invented for the roll-up.
func (e *MultiResolverError) Error() string {
	values := subjectValues(e.Entity, e.Field)
	if e.Sentinel == nil {
		subject := strings.Join(values, " ")
		if subject == "" {
			return e.Summary
		}
		return subject + ": " + e.Summary
	}
	header := cerrors.ComposeMessage(e.Sentinel, values, "")
	switch {
	case e.Summary == "":
		return header
	case header == "":
		return e.Summary
	default:
		return header + ": " + e.Summary
	}
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
// The Message is exactly what Error renders — one composition feeding both
// surfaces, so the two cannot diverge, and a chain walk that suppresses a frame
// restating its cause is not defeated by two strings that say the same thing
// differently.
//
// The inner error chain is preserved via cerrors.Wrap, so errors.Is
// against the resolver's sentinel errors (ErrYAMLLoad, ErrSchemaConformance,
// etc.) continues to match through the converted CompilerError.
func (e *ResolverError) ToCompilerError() *cerrors.CompilerError {
	cat := cerrors.ClassifyEntityCategory(e.Entity)
	source := cerrors.SourceLocation{File: e.Field}
	return cerrors.Wrap(e, cat, source, e.Error())
}
