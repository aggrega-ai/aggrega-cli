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

var errInvalidParameterRef = cerrors.Register("invalid-parameter-ref", cerrors.Meta{
	ShortCode: "E0158",
	Message:   "invalid parameter reference: check the {{ .parameters.* }} expression names a declared parameter and parses as a Go template",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrYAMLLoad wraps a failure to turn a file into the content a surface asked
// for: the read did not produce bytes, or the bytes did not parse.
//
// Its subject is any file a build has to read, and not only the three documents
// a build starts from. Beside the Aggregate, the Release and the Manifest it
// covers the parameter contract read out of an Aggregate, the file a Release's
// parameters import names, the file a schema import names, and the file a
// Recipe's or FeatureRecipe's valuesMapping, workload or artifacts entry imports
// — each of them a path an author wrote, and a file that yielded nothing to use.
//
// One identity covers them all, and the reason is that neither the failure nor
// the remedy varies with which key named the path: the file is absent,
// unreadable or malformed, and what the author does is correct the path or
// correct the file. Which surface named it is what the OCCURRENCE carries — the
// entity reference and the locator travel on the finding and into the source
// pointer — so a second identity per naming key would hand a reader another code
// to look up for one failure while telling them nothing the diagnostic does not
// already say.
//
// The registered sentence names no format, and that is a requirement rather than
// a simplification. Some of the files it covers are parsed as YAML and some are
// read as Go template source and never parsed at all, so a sentence promising
// YAML would lead a diagnostic about a template file with a claim about that
// file's format, and would offer advice its reader cannot act on. What each
// occurrence supplies is the clause saying which surface named the path and the
// reason the read or the parse gave.
//
// It is not a fault in what a file holds once it has parsed. Content that parsed
// and is the wrong shape for the surface that asked for it is a rule over what
// the author wrote, and the surface that read it names its own condition for
// that; this one reports only that there was nothing to read.
func ErrYAMLLoad() error { return errYAMLLoad }

var errYAMLLoad = cerrors.Register("document-not-loadable", cerrors.Meta{
	ShortCode: "E0175",
	Message:   "file load failed: the file could not be read, or its bytes did not parse",
	RulePlace: cerrors.NotARule("A file a build had to read could not be read, or its bytes could not be parsed, so the " +
		"surface that named it was left with nothing to use. The file is opened when the load runs, " +
		"and what it reports is a load that did not yield content."),
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

var errManifestReservedPrefix = cerrors.Register("manifest-reserved-prefix", cerrors.Meta{
	ShortCode: "E0161",
	Message:   "manifest annotation binding uses reserved prefix",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrManifestCrossBindingPillarCollision wraps two Manifest
// annotation bindings that target the same Feature URI with the
// same (path, pillar) routing pair. Only one binding may declare
// a given (path, pillar) pair per target Feature; multiple
// bindings competing for the same routing slot cannot resolve
// unambiguously at expansion time.
func ErrManifestCrossBindingPillarCollision() error { return errManifestCrossBindingPillarCollision }

var errManifestCrossBindingPillarCollision = cerrors.Register("manifest-cross-binding-pillar-collision", cerrors.Meta{
	ShortCode: "E0160",
	Message:   "manifest annotation bindings collide on same Feature path+pillar",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMalformedRecipeReference fires when a Manifest mapping value carries more
// slash-separated segments than a Recipe / FeatureRecipe reference accepts. The
// verdict comes from the reference string alone, so the diagnostic names the
// shape the author typed and the shapes a reference accepts, instead of
// reporting the entity as absent: the recipe may be present in the catalog and
// correctly named, and only unreachable through the shape written. That is why
// it is distinct from the condition raised for a reference the catalog does not
// hold, whose remedy — add a Manifest mapping for the type — is a step the author
// of a mis-shaped mapping has already taken.
//
// The accepted shape itself has a single home; see [RecipeReferenceSegments].
func ErrMalformedRecipeReference() error { return errMalformedRecipeReference }

var errMalformedRecipeReference = cerrors.Register("malformed-recipe-reference", cerrors.Meta{
	ShortCode: "E0095",
	Message:   "malformed recipe reference in a Manifest mapping value",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrAnnotationBindingTargetUnresolved wraps a Manifest annotation binding
// whose declared route cannot be resolved against the Feature it targets: the
// Feature is absent from the loaded catalog, its spec.schema does not compile,
// or a path the binding declares under maps.infra or maps.values names no
// property the Feature's schema declares at that pillar.
//
// All four are one condition because they are one rule — every path a binding
// declares resolves to a declared property — and which of the four ways it was
// broken is what the occurrence names. The condition is checked over the
// Manifest before any annotation is expanded, so a binding that would route a
// value into a path nothing declares is refused before it synthesizes an
// attachment.
//
// Distinct from ErrAnnotationSubkeyNotConforming, which asks a different
// question of a different authoring surface: this one reads what the Manifest
// binding declares, that one reads the value an Aggregate or Release author
// wrote through the binding.
func ErrAnnotationBindingTargetUnresolved() error { return errAnnotationBindingTargetUnresolved }

var errAnnotationBindingTargetUnresolved = cerrors.Register("annotation-binding-target-unresolved", cerrors.Meta{
	ShortCode: "E0253",
	Message:   "annotation binding does not resolve against its target Feature",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrAnnotationSubkeyNotConforming wraps an authored annotation subkey whose
// routed value does not conform to the target Feature's spec.schema at the
// pillar the binding routes it through — a subkey the schema declares no
// property for, or a value that fails the sub-schema located at that path.
//
// It is raised during annotation expansion, over the value an Aggregate or
// Release author supplied, which is why it is a separate condition from
// ErrAnnotationBindingTargetUnresolved: that one refuses what the Manifest
// binding declares, this one refuses what was written through it.
//
// The undeclared-subkey case is usually pre-empted by the binding-target check
// at catalog load, so reaching it here means the binding declares the path and
// the schema does not carry it — the two reads disagree, and the value is
// refused rather than routed.
func ErrAnnotationSubkeyNotConforming() error { return errAnnotationSubkeyNotConforming }

var errAnnotationSubkeyNotConforming = cerrors.Register("annotation-subkey-not-conforming", cerrors.Meta{
	ShortCode: "E0254",
	Message:   "annotation subkey does not conform to the target Feature's schema at the routed pillar",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrAnnotationSidePillarMismatch wraps an annotation whose binding routes it
// through a pillar the authoring side it was written on may not use: an
// Aggregate-side annotation routes through values, a Release-side annotation
// through infra, and any other pairing is refused.
//
// The partition is the Aggregate-as-authoring-surface split expressed on the
// annotation shortcut: a developer's annotation contributes application values,
// an operator's contributes infrastructure sizing, and an annotation that
// crossed would let one surface author the other's pillar through a shortcut it
// could not write directly.
func ErrAnnotationSidePillarMismatch() error { return errAnnotationSidePillarMismatch }

var errAnnotationSidePillarMismatch = cerrors.Register("annotation-side-pillar-mismatch", cerrors.Meta{
	ShortCode: "E0255",
	Message:   "annotation routes through a pillar its authoring side may not use",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrAnnotationBindingPillarOverlap marks an ADVISORY finding, not a halt: a
// Manifest annotation binding declares one path in both its maps.infra and
// maps.values lists.
//
// Declaring the overlap is legal and the compiler does not police it — the
// routing tie-break is deterministic and sends the path through infra — so this
// sentinel never reaches a build's verdict; it exists so a front end can
// recognise the advisory class and render it as a warning. What it names is the
// consequence: the values side of that path becomes unreachable, so an
// Aggregate-side annotation routing to it is refused at expansion time by
// ErrAnnotationSidePillarMismatch. An author who wants both pillars routed
// declares two bindings under two annotation keys.
func ErrAnnotationBindingPillarOverlap() error { return errAnnotationBindingPillarOverlap }

var errAnnotationBindingPillarOverlap = cerrors.Register("annotation-binding-pillar-overlap", cerrors.Meta{
	ShortCode: "E0256",
	Message:   "annotation binding declares one path in both pillar lists",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrInvalidInjectKey wraps an inject delivery key that does not match
// the expected per-mechanism format.
func ErrInvalidInjectKey() error { return errInvalidInjectKey }

var errInvalidInjectKey = cerrors.Register("invalid-inject-key", cerrors.Meta{
	ShortCode: "E0156",
	Message:   "invalid inject key: the from-key must name a declared provides key of the target component type",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSchemaConformance wraps a developer-authored component-instance
// values or infra pillar that does not conform to the catalog
// Component's declared schema for that pillar (wrong type, out of
// range, missing required-non-defaulted field, or a property the
// schema forbids). Validated during resolution, default-aware: a field the
// schema supplies a default for is not "missing". Distinct from
// monotonic-narrowing, which stays signal-only.
func ErrSchemaConformance() error { return errSchemaConformance }

var errSchemaConformance = cerrors.Register("schema-conformance", cerrors.Meta{
	ShortCode: "E0168",
	Message:   "authored pillar does not conform to its declared schema",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSchemaContractCompileFailure wraps a schema contract that was declared and
// yielded nothing a payload can be measured against: a Component or Feature's
// own spec.schema, or the per-edge valueSchema / infraSchema one of its
// dependency sockets declares. The conformance rules measure a payload against a
// compiled contract, so a contract that yields none leaves its subject with
// nothing to measure against — which is why this halts rather than passing the
// payload through unchecked.
//
// Two faults reach it, and they are one condition because the remedy is the same
// and the consequence of passing either over is identical. A contract may fail
// to compile at all, and a contract may compile and then build no validator — a
// body readable as compact form that is not a schema anything can validate
// against. Splitting them would give one authoring mistake two identities to
// match on while telling the author the same thing.
//
// It is the counterpart of [ErrSchemaConformance] one step earlier: that
// condition reports a payload failing a contract, this one reports that there is
// no readable contract to fail. The pair is what keeps the two absences apart. A
// subject declaring no contract is skipped, deliberately and silently, because
// there is nothing to check; a subject declaring one that cannot be read is
// refused, because skipping it would retire a check with nothing to show that it
// had gone.
//
// It is raised as the catalog loads, where the entity that declared the contract,
// the pillar, the authored field and the source line are all known, and at
// resolution for a contract that reached it without that reading.
func ErrSchemaContractCompileFailure() error { return errSchemaContractCompileFailure }

var errSchemaContractCompileFailure = cerrors.Register(
	"schema-contract-compile-failure", cerrors.Meta{
		ShortCode: "E0257",
		Message: "declared schema contract could not be compiled into a usable schema, so nothing " +
			"was validated against it: correct the contract where it is declared, or remove it " +
			"if the configuration it covers needs no validation",
		RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errAttachmentSlotShape = cerrors.Register("attachment-slot-shape", cerrors.Meta{
	ShortCode: "E0150",
	Message:   "attachment does not conform to its declared slot shape",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrAttachmentSlotNearMiss marks an ADVISORY finding, not a halt: an
// attachment whose name is not a declared slot on its component's type but
// is within a small edit distance of one — the shape a slot-name typo
// takes. An attachment name matching no slot is legal (the attachment
// surface is open), so this sentinel never reaches a build's verdict; it
// exists so a front end can recognise the advisory class and render it as a
// warning. It is not raised for a declared slot whose label constraint the
// attachment's Feature satisfies: that pairing is a fill written under another
// name rather than a mistyped free attachment, and it is judged, once every
// scope has folded, by the rule reporting ErrMisnamedSlotFill.
func ErrAttachmentSlotNearMiss() error { return errAttachmentSlotNearMiss }

var errAttachmentSlotNearMiss = cerrors.Register("attachment-slot-near-miss", cerrors.Meta{
	ShortCode: "E0149",
	Message:   "attachment name is a near miss for a declared slot",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMisnamedSlotFill wraps an attachment written on a component instance under a
// name its component type declares no slot for, whose Feature satisfies the label
// constraint of a slot the type declares that nothing binds on that instance once
// every scope has folded — a fill meant for the slot, written under another
// name. It halts.
//
// It is narrower than the openness of the attachment surface, and deliberately:
// an attachment fitting no declared slot, an extra attachment once every slot it
// fits is bound, and an attachment the instance inherits from a subsystem or a
// Release boundary all stay free. The finding names the attachment, the
// component instance, the Feature attached and each empty slot that Feature
// fits, and the remedy is to attach it under the slot's name.
//
// It is declared here and raised from the package that holds the rule, which is
// the one point in the compilation where every fact it reads is in hand: which
// slots a node ends up binding, and which scope each attachment was written at.
// It is distinct from ErrAttachmentSlotNearMiss, which is an advisory about a
// name one edit from a declared slot whose Feature does not fit it; where both
// would describe one attachment, this is the one reported.
func ErrMisnamedSlotFill() error { return errMisnamedSlotFill }

var errMisnamedSlotFill = cerrors.Register("misnamed-slot-fill", cerrors.Meta{
	ShortCode: "E0328",
	Message:   "attachment written under a name no slot declares fits a slot its component leaves empty",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errUsesSocketNearMiss = cerrors.Register("uses-socket-near-miss", cerrors.Meta{
	ShortCode: "E0174",
	Message:   "uses socket name is a near miss for a declared socket",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errProvidesKeyNotDeclared = cerrors.Register("provides-key-not-declared", cerrors.Meta{
	ShortCode: "E0167",
	Message:   "Recipe publishes a provides key its Component does not declare",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnreadParameter marks an ADVISORY finding, not a halt: a parameter the
// Aggregate declares that no reference in the Aggregate's own text reads.
// Declaring a parameter nothing reads is legal — the compiler does not police
// which knobs an author exposes — so this sentinel never reaches a build's
// verdict; it exists so a front end can recognise the advisory class and
// render it as a warning.
func ErrUnreadParameter() error { return errUnreadParameter }

var errUnreadParameter = cerrors.Register("unread-parameter", cerrors.Meta{
	ShortCode: "E0173",
	Message:   "declared parameter is read by no reference in the Aggregate",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errGuardEntityViewInconsistency = cerrors.Register("internal-catalog-view-disagreement", cerrors.Meta{
	ShortCode: "E0154",
	Message:   "the compiler's own views of the catalog disagree",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errMissingRequiredFeature = cerrors.Register("missing-required-feature", cerrors.Meta{
	ShortCode: "E0162",
	Message:   "missing required feature: attach a Feature of the required type to the component",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrFeatureMergeConflict wraps a conflict during Release-side Feature
// attachment merging (e.g., divergent values on the same Feature key).
func ErrFeatureMergeConflict() error { return errFeatureMergeConflict }

var errFeatureMergeConflict = cerrors.Register("feature-merge-conflict", cerrors.Meta{
	ShortCode: "E0153",
	Message:   "feature merge conflict: reconcile the Aggregate and Release declarations for the feature",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnknownUsesSocket wraps a socket name that the entity owning the surface
// does not declare in its usesSchema, on a surface that may only name a
// declared socket: a Feature attachment's uses (the socket must be on the
// attached Feature) and a Release per-edge uses (the socket must be on the
// consuming component). A component's own uses block is NOT one of those
// surfaces — an Aggregate author may name a socket the catalog did not
// declare — so this sentinel no longer fires for it.
func ErrUnknownUsesSocket() error { return errUnknownUsesSocket }

var errUnknownUsesSocket = cerrors.Register("unknown-uses-socket", cerrors.Meta{
	ShortCode: "E0171",
	Message:   "unknown uses socket: name a socket the entity declares in its usesSchema — only a component's own uses block may name one the catalog did not declare",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrConnectionAttachmentSlotUndeclared wraps an attachment a connection carries
// under a name the connection's socket declares no slot for. A connection's
// attachment surface is closed: the socket on the consuming Component declares,
// in its featuresSchema, every name a connection on it may carry an attachment
// under, so any other name — and any attachment at all on a socket declaring no
// slots, which is every socket an Aggregate names without the catalog declaring
// it — halts when the build loads. It is raised for the Aggregate's connection
// entries and for the Release's alike, positioned at the entry in the document the
// name was written in, naming the attachment, each declared slot within one edit
// of it, the alias, the socket and the component, and the slots the socket does
// declare.
func ErrConnectionAttachmentSlotUndeclared() error { return errConnectionAttachmentSlotUndeclared }

var errConnectionAttachmentSlotUndeclared = cerrors.Register("connection-attachment-slot-undeclared", cerrors.Meta{
	ShortCode: "E0315",
	Message:   "a connection carries an attachment under a name its socket declares no slot for",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSelfInjectForbidden wraps a self-inject declaration on a component
// whose catalog type does not declare a spec.inject allowlist.
func ErrSelfInjectForbidden() error { return errSelfInjectForbidden }

var errSelfInjectForbidden = cerrors.Register("self-inject-forbidden", cerrors.Meta{
	ShortCode: "E0169",
	Message:   "self-inject on component without spec.inject allowlist",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrInjectKeyCollision wraps a delivery key declared on more than one
// inject source for the same component.
func ErrInjectKeyCollision() error { return errInjectKeyCollision }

var errInjectKeyCollision = cerrors.Register("inject-key-collision", cerrors.Meta{
	ShortCode: "E0155",
	Message:   "duplicate delivery key across inject sources",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMissingRequiredSocket wraps a uses-socket marked required in the
// Component's usesSchema that no instance populates.
func ErrMissingRequiredSocket() error { return errMissingRequiredSocket }

var errMissingRequiredSocket = cerrors.Register("missing-required-socket", cerrors.Meta{
	ShortCode: "E0163",
	Message:   "required uses socket not populated",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrParameterMissing wraps a parameter declared required: true in the
// Aggregate's typed contract that neither the Aggregate's default nor
// the Release fulfillment supplies. Surfaced by parameter-contract
// validation, which checks every supplied parameter against the declared
// typed contract before interpolation — so a missing required parameter
// halts the build instead of interpolating an empty value.
func ErrParameterMissing() error { return errParameterMissing }

var errParameterMissing = cerrors.Register("parameter-missing", cerrors.Meta{
	ShortCode: "E0165",
	Message:   "required parameter not supplied",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrParameterContractViolation wraps a parameter value that fails the
// Aggregate's typed contract — type mismatch, enum / pattern / range
// violation, validate-predicate failure, or a Release key with no
// corresponding Aggregate declaration (closed-contract policy).
// Surfaced by parameter-contract validation, which runs after the Release
// parameter load and before parameter interpolation.
//
// It also wraps a contract that cannot constrain anything as written: a
// declaration that does not decode, and a pattern: that does not compile. The
// pattern is refused when the contract loads, before any Release value is read,
// so a broken one halts the build whether or not a Release supplies the
// parameter.
func ErrParameterContractViolation() error { return errParameterContractViolation }

var errParameterContractViolation = cerrors.Register("parameter-contract-violation", cerrors.Meta{
	ShortCode: "E0164",
	Message:   "parameter contract violated",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errDuplicateBoundaryComponent = cerrors.Register("duplicate-boundary-component", cerrors.Meta{
	ShortCode: "E0151",
	Message:   "component declared in more than one boundary: declare each component in exactly one boundary",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errUnnormalizedRelease = cerrors.Register("release-not-in-canonical-form", cerrors.Meta{
	ShortCode: "E0172",
	Message:   "release reached resolve un-normalized (non-empty root components alongside a synthesized default boundary)",
	RulePlace: cerrors.NotARule("A Release reached resolution in a state the normalization core cannot produce, so a caller " +
		"built one by hand or took a load path that skipped that core. It reports an invariant of " +
		"this program's own construction; the authored shape it would correspond to is refused by the " +
		"envelope."),
})

// ErrReleaseChainComposesBothRoutingKeys fires when folding a Release extends
// chain would produce a Release carrying both named boundaries and top-level
// components — the two routing keys that are mutually exclusive in any one
// Release.
//
// Every document along such a chain is well-formed on its own, which is what
// makes this a condition of its own rather than the envelope rule reported
// later. The envelope holds each authored document to the exclusivity as it is
// read; neither the Release declaring boundaries nor the Release declaring
// components breaks it, and the shape that no author may write appears only once
// the two are composed. So the subject here is a combination rather than a
// document, and the diagnostic names both files and both keys: neither is at
// fault alone, and wording that named only the Release the build was pointed at
// would send its author to the file with nothing wrong in it.
//
// It is not folded into the chain-structure conditions — a cycle, a chain past
// the depth bound, a parent that does not resolve. Those describe the shape of
// the chain itself, this describes the shape of what the chain composes to, and
// a consumer handed one identity for both could not tell which it had caught.
func ErrReleaseChainComposesBothRoutingKeys() error {
	return errReleaseChainComposesBothRoutingKeys
}

var errReleaseChainComposesBothRoutingKeys = cerrors.Register(
	"release-chain-composes-both-routing-keys", cerrors.Meta{
		ShortCode: "E0296",
		Message: "release extends chain composes both named boundaries and top-level components: " +
			"declare one routing shape across the chain, never both",
		RulePlace: cerrors.RuleOverAuthoredInput(),
	})

// ErrInheritedBoundaryDirectoryIsLiteral marks an ADVISORY finding, not a halt:
// a boundary whose output directory a Release inherited from the Release it
// extends, where that directory carries no template variable and so resolves to
// the same path for both.
//
// Inheriting the directory is the supported shape rather than a mistake. A
// directory is a template, so a parent writes it once and a child that fulfils a
// parameter resolves it somewhere else with nothing duplicated — which is the
// whole reason a child need not restate its parent's boundary topology. The
// residual risk is narrow and real: a directory carrying no variable resolves
// identically for parent and child, so a build of the child writes where a build
// of the parent writes, and no command-line option redirects it.
//
// It cannot halt, and that is a property of the rule rather than caution about
// it: writing to the same resolved path is something an author may legitimately
// mean — a child that swaps only the rendering Recipes to compare their output,
// or that changes only sizing — and a rule that halted on the inference would
// refuse that author's legal input on a guess.
func ErrInheritedBoundaryDirectoryIsLiteral() error {
	return errInheritedBoundaryDirectoryIsLiteral
}

var errInheritedBoundaryDirectoryIsLiteral = cerrors.Register(
	"inherited-boundary-directory-is-literal", cerrors.Meta{
		ShortCode: "E0297",
		Message: "inherited boundary directory carries no template variable, so it resolves to the " +
			"path the extended Release writes to",
		RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errVerdictPluginTrustMismatch = cerrors.Register("verdict-plugin-trust-mismatch", cerrors.Meta{
	ShortCode: "E0246",
	Message:   "the digest recorded as trusted for this plugin does not match the digest its cached manifest reports",
	RulePlace: cerrors.NotARule("The digest recorded as trusted for a verdict plugin disagrees with the digest that plugin's " +
		"manifest reports. The manifest is read from the content cache as the resolution runs, so the " +
		"comparand is obtained when the comparison happens and what it reports is a trust " +
		"verification that did not hold."),
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
// Advice is what THIS occurrence tells the author to do, empty for a condition
// whose registered sentence says the whole of it. It is a separate field from
// Message because the two land in different parts of a composed diagnostic: the
// message names the values the finding is about and is quoted among them, while
// the advice is a sentence naming the edit. A raise site that folded its remedy
// into Message would have it rendered as one more quoted value, reading as one
// more thing the finding is about.
//
// It is per occurrence rather than per condition because that is the only place
// it can be true: a condition whose remedy differs between its own violation
// classes has no single sentence to register beside its identity, and a
// registered one would be wrong at whichever class it did not describe.
//
// File is the document Field is a path inside, as the workspace-relative path
// the compilation named it by, when the finding's own subject recorded one — a
// rule over an entry the Release wrote records the Release's file. It is empty
// where the subject recorded none, which a reader must take as "not known here"
// rather than as any particular document. It is carried for a surface that
// places the finding inside an open document and has to choose which one;
// Error and ToCompilerError do not read it, so a finding renders the same
// whether or not its file is known.
type ResolverError struct {
	Entity  string // e.g. "Aggregate my-app"
	Field   string // e.g. ".spec.components.bad name"
	File    string // e.g. "release.yaml"; empty when the finding's subject recorded no file
	Message string // what this occurrence supplied, not a description of the condition
	Advice  string // what this occurrence tells the author to do, empty where the condition's own sentence says it
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
	if e.Message == "" && e.Advice == "" && e.Err != nil {
		return e.Err.Error()
	}
	if e.Message != "" {
		values = append(values, e.Message)
	}
	return cerrors.ComposeMessage(e.Err, values, e.Advice)
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
// comes from [ResolverError.category], which reads the Entity prefix through
// cerrors.ClassifyEntityCategory: Aggregate → DeveloperError, Release →
// OpsError, catalog entities → CatalogError.
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
	source := cerrors.SourceLocation{File: e.Field}
	return cerrors.Wrap(e, e.category(), source, e.Error())
}

// category answers which authoring surface this finding is filed under, and
// returns the empty Category for a finding that is filed under none.
//
// The mapping is asked first and its answer is taken whenever it has one. What
// remains are the two references the mapping does not cover, and they are
// different facts rather than one fallback — which is why they are answered
// separately here rather than defaulted together in the classifier:
//
//   - A finding that named NO subject is filed under no surface. A raise site
//     leaves Entity empty when the condition it reports is not a fault in an
//     authored document — the views of the catalog disagreeing with each other
//     is the case that exists today — and there is then no surface for a
//     reader to fix. Naming one anyway sends them to a document that is not at
//     fault, and the head is the part of a diagnostic a reader acts on first.
//
//   - A finding that named a subject the mapping does not cover is filed at
//     the developer's authoring surface. Something WAS named: the references
//     reaching this shape are the labels the placement and resolution walks
//     supply for faults in the developer's own document, so that is where the
//     reader is sent. This one is still a judgement rather than a derivation,
//     and it is written here, at the one site that knows what its findings
//     mean, rather than in the shared mapping where every future caller would
//     inherit it without being asked.
func (e *ResolverError) category() cerrors.Category {
	if category, covered := cerrors.ClassifyEntityCategory(e.Entity); covered {
		return category
	}
	if e.Entity == "" {
		return ""
	}
	return cerrors.DeveloperError
}
