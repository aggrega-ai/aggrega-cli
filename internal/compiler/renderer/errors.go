// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
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

var errProvidesSelfContainment = cerrors.Register("provides-self-containment", cerrors.Meta{
	ShortCode: "E0057",
	Message:   "provides block references a root outside the self-contained set",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errTemplateUnknownRoot = cerrors.Register("template-unknown-root", cerrors.Meta{
	ShortCode: "E0060",
	Message:   "template references a root the render context does not define",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrLocalsForwardReference is the ordering fault a locals reference carries when
// the key it names is declared below the point the template runs. It is declared
// where the classification that decides it is declared, because the surface that
// resolves a block for rendered output and the surface that resolves one for a
// `source:` coordinate both report it and sit in packages that cannot reach each
// other — so this is a re-export rather than a second condition, and a match
// against either name succeeds for a halt raised by either surface.
func ErrLocalsForwardReference() error { return gotmpl.ErrLocalsForwardReference() }

// ErrLocalsUnknownReference is the naming fault a locals reference carries when
// the block declares no such key at all. Declared and re-exported for the reason
// stated above.
func ErrLocalsUnknownReference() error { return gotmpl.ErrLocalsUnknownReference() }

func ErrProvidesEvaluation() error { return errProvidesEvaluation }

var errProvidesEvaluation = cerrors.Register("provides-evaluation-failed", cerrors.Meta{
	ShortCode: "E0056",
	Message:   "provides template evaluation failed",
	RulePlace: cerrors.NotARule("A provides template could not be evaluated, so the entry produced no value. It reports that " +
		"answering the question became impossible rather than a rule that refused content, and what " +
		"failed is in the cause it wraps."),
})

func ErrSelfInjectKeyCollision() error { return errSelfInjectKeyCollision }

var errSelfInjectKeyCollision = cerrors.Register("self-inject-key-collision", cerrors.Meta{
	ShortCode: "E0059",
	Message:   "duplicate delivery key across self-inject and uses-inject",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

func ErrAttachmentInjectKeyCollision() error { return errAttachmentInjectKeyCollision }

var errAttachmentInjectKeyCollision = cerrors.Register("attachment-inject-key-collision", cerrors.Meta{
	ShortCode: "E0034",
	Message:   "duplicate delivery key across attachment-inject and another inject source",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrAttachmentInjectMappingKeyCollision is the halt raised when one
// attachment's FeatureRecipe injectMapping writes a delivery key, under one
// mechanism, that a different attachment attached to the same component already
// delivered — through that attachment's own mapping, or through its own inject
// block. Two attachments delivering one key would leave the consumer reading
// whichever of them happened to be merged last, so one credential could be
// presented as another with nothing in the build saying so.
//
// It is a sibling of ErrAttachmentInjectKeyCollision rather than a widening of
// it, because the two report different claims. That one is the claim an
// attachment's own inject block makes, and its registered wording names that
// block; this one is the claim an attachment's mapping makes, which no inject
// block wrote. Both are raised by one walk over one record of the component's
// deliveries, so they cannot disagree about which claim holds a key.
//
// A mapping rewriting a key the component's own inject block or one of its
// connections' inject blocks delivered is not this condition: that is
// interception, and the mapping's value replaces that one entry.
// Category: DEVELOPER_ERROR. Match with
// errors.Is(err, ErrAttachmentInjectMappingKeyCollision()).
func ErrAttachmentInjectMappingKeyCollision() error { return errAttachmentInjectMappingKeyCollision }

var errAttachmentInjectMappingKeyCollision = cerrors.Register("attachment-inject-mapping-key-collision", cerrors.Meta{
	ShortCode: "E0318",
	Message:   "duplicate delivery key across two attachments: an attachment's FeatureRecipe injectMapping writes a key another attachment already delivered",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrBoundaryEnvOverrideKeyCollision is the halt raised when the environment
// override a boundary-scope attachment's FeatureRecipe computes for a component
// writes a delivery key that a different attachment on that component already
// delivered — through that attachment's own inject block, through its
// FeatureRecipe's injectMapping, or through another boundary-scope attachment's
// override. The override is applied after every injectMapping, so it would
// silently replace whatever the other attachment delivered there.
//
// It is a sibling of ErrAttachmentInjectMappingKeyCollision rather than a
// widening of it, because the registered wording of that one names an
// attachment's injectMapping, which an override is not. Both are raised by one
// walk over one record of the component's deliveries, so they cannot disagree
// about which delivery holds a key.
//
// An override rewriting a key the component's own inject block or one of its
// connections' inject blocks delivered is not this condition: that is
// interception, and the override replaces that one entry — which is what the
// compose bridge does to a consumer's host variable.
// Category: OPS_ERROR. Match with
// errors.Is(err, ErrBoundaryEnvOverrideKeyCollision()).
func ErrBoundaryEnvOverrideKeyCollision() error { return errBoundaryEnvOverrideKeyCollision }

var errBoundaryEnvOverrideKeyCollision = cerrors.Register("boundary-env-override-key-collision", cerrors.Meta{
	ShortCode: "E0322",
	Message:   "duplicate delivery key across two attachments: the environment override a boundary-scope attachment computes for a component writes a key another attachment already delivered",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

func ErrUnmappedCustomMechanism() error { return errUnmappedCustomMechanism }

var errUnmappedCustomMechanism = cerrors.Register("custom-mechanism-has-no-mapping", cerrors.Meta{
	ShortCode: "E0062",
	Message:   "custom inject mechanism has no matching key in injectMapping output; add the mechanism's key to the Recipe injectMapping",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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
// file with no diagnostic. Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrProvidesContractUnfulfilled()).
//
// The two pairs report a shortfall differently, and the difference is
// user-visible. Over the Component/Recipe pair it is reported by a declared rule
// invoked once per declared key, so every key a Recipe is short of is named, each
// positioned at its own entry in that Recipe's provides block. Over the
// Feature/FeatureRecipe pair it is raised from the attachment's own resolution,
// which reports the first missing key in sorted order and stops — so the reported
// key and the halt are byte-identical across runs there. Keys are walked in
// sorted order either way, which is what makes both deterministic.
func ErrProvidesContractUnfulfilled() error { return errProvidesContractUnfulfilled }

var errProvidesContractUnfulfilled = cerrors.Register("provides-contract-unfulfilled", cerrors.Meta{
	ShortCode: "E0055",
	Message:   "Recipe or FeatureRecipe does not publish a provides key its abstract entity's schema.provides contract declares",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMappingImportShape fires when a valuesMapping $import: (or a recursive
// MappingLibrary entry body's field-root $import:) is neither a host-bearing
// reference nor a well-formed bare-name `<Library>#/mappings/<entry>` identity.
func ErrMappingImportShape() error { return errMappingImportShape }

var errMappingImportShape = cerrors.Register("mapping-import-shape", cerrors.Meta{
	ShortCode: "E0045",
	Message:   "valuesMapping $import: is not a bare-name mapping-library reference (<Library>#/mappings/<entry>)",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMappingLibraryNotFound fires when a bare-name mapping-library import
// names a MappingLibrary absent from the resolved set (the local catalog plus
// any lockfile-pinned libraries bridged in at load).
//
// # Why no rule over the authored reference is declared for it
//
// A values-mapping reference naming a library nothing publishes is refused as the
// Release's dependencies are resolved, under the condition raised for a reference
// the catalog holds no entity for — and that resolution precedes the render on
// every surface that renders at all. So a rule declared over the authored
// reference could not be invoked ahead of the resolution that already refuses it,
// and the declaration would read as enforcement while nothing could reach it.
//
// The arms that raise this condition stay where they are: they are the refusal for
// the values mapping a FeatureRecipe carries, for a reference lifted out of a
// library entry's own body by the recursion, and for a caller assembling a render's
// input without resolving dependencies first.
func ErrMappingLibraryNotFound() error { return errMappingLibraryNotFound }

var errMappingLibraryNotFound = cerrors.Register("mapping-library-not-found", cerrors.Meta{
	ShortCode: "E0046",
	Message:   "mapping-library import references a MappingLibrary not present in the catalog",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMappingEntryNotFound fires when a bare-name mapping-library import names
// an entry absent from the resolved MappingLibrary's mappings map.
func ErrMappingEntryNotFound() error { return errMappingEntryNotFound }

var errMappingEntryNotFound = cerrors.Register("mapping-entry-not-found", cerrors.Meta{
	ShortCode: "E0044",
	Message:   "mapping-library import references an entry not present in the MappingLibrary",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrWorkloadImportShape fires when a workload $import: (or a recursive
// WorkloadLibrary entry body's field-root $import:) is neither a host-bearing
// reference nor a well-formed bare-name `<Library>#/workloads/<entry>` identity.
func ErrWorkloadImportShape() error { return errWorkloadImportShape }

var errWorkloadImportShape = cerrors.Register("workload-import-shape", cerrors.Meta{
	ShortCode: "E0064",
	Message:   "workload $import: is not a bare-name workload-library reference (<Library>#/workloads/<entry>)",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrWorkloadLibraryNotFound fires when a bare-name workload-library import
// names a WorkloadLibrary absent from the resolved set (the local catalog plus
// any lockfile-pinned libraries bridged in at load).
//
// # Why no rule over the authored reference is declared for it
//
// A workload reference naming a library nothing publishes is refused as the
// Release's dependencies are resolved, under the condition raised for a reference
// the catalog holds no entity for — and that resolution precedes the render on
// every surface that renders at all. So a rule declared over the authored reference
// could not be invoked ahead of the resolution that already refuses it, and the
// declaration would read as enforcement while nothing could reach it.
//
// The arms that raise this condition stay where they are: they are the refusal for
// the workload a FeatureRecipe carries, for a reference lifted out of a library
// entry's own body by the recursion, and for a caller assembling a render's input
// without resolving dependencies first.
func ErrWorkloadLibraryNotFound() error { return errWorkloadLibraryNotFound }

var errWorkloadLibraryNotFound = cerrors.Register("workload-library-not-found", cerrors.Meta{
	ShortCode: "E0065",
	Message:   "workload-library import references a WorkloadLibrary not present in the catalog",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrWorkloadEntryNotFound fires when a bare-name workload-library import names
// an entry absent from the resolved WorkloadLibrary's workloads map.
func ErrWorkloadEntryNotFound() error { return errWorkloadEntryNotFound }

var errWorkloadEntryNotFound = cerrors.Register("workload-entry-not-found", cerrors.Meta{
	ShortCode: "E0063",
	Message:   "workload-library import references an entry not present in the WorkloadLibrary",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactImportShape fires when an artifacts entry `$import:` value is
// neither a host-bearing reference nor a well-formed bare-name
// `<Library>#/artifacts/<entry>` identity — including a field-root `$import:`
// nested inside a bundle file body, which is not supported (a single bundle
// file cannot expand into another bundle; compose at the consumption site).
func ErrArtifactImportShape() error { return errArtifactImportShape }

var errArtifactImportShape = cerrors.Register("artifact-import-shape", cerrors.Meta{
	ShortCode: "E0027",
	Message:   "artifacts $import: is not a bare-name artifact-library reference (<Library>#/artifacts/<entry>)",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactLibraryNotFound fires when a bare-name artifact-library import
// names an ArtifactLibrary absent from the resolved set (the local catalog
// plus any lockfile-pinned libraries bridged in at load).
func ErrArtifactLibraryNotFound() error { return errArtifactLibraryNotFound }

var errArtifactLibraryNotFound = cerrors.Register("artifact-library-not-found", cerrors.Meta{
	ShortCode: "E0031",
	Message:   "artifact-library import references an ArtifactLibrary not present in the catalog",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactLibraryEntryNotFound fires when a bare-name artifact-library
// import names a bundle absent from the resolved ArtifactLibrary's artifacts map.
func ErrArtifactLibraryEntryNotFound() error { return errArtifactLibraryEntryNotFound }

var errArtifactLibraryEntryNotFound = cerrors.Register("artifact-library-entry-not-found", cerrors.Meta{
	ShortCode: "E0030",
	Message:   "artifact-library import references a bundle not present in the ArtifactLibrary",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errArtifactOutputCollision = cerrors.Register("artifact-output-collision", cerrors.Meta{
	ShortCode: "E0028",
	Message:   "artifacts output filename collision: two artifact outputs carry the same filename",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errArtifactPillarNotDeclared = cerrors.Register("artifact-pillar-not-declared", cerrors.Meta{
	ShortCode: "E0029",
	Message:   "artifact template references a pillar the Recipe does not declare",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrCallerRenderFactsNotDeclared fires when a template in the FeatureRecipe a
// per-component attachment binds reads the render facts of the components
// connecting to its host — `render_facts` reached beneath `.context.used_by` —
// while the FeatureRecipe does not set readsCallerRenderFacts.
//
// The declaration is what decides when such an attachment renders: one that sets
// it renders after every node has rendered, so each caller has reported what it
// emitted by then, and one that does not renders in its host's turn, before a
// caller later in the walk has rendered at all. A template reading the facts
// without the declaration is a template reading something that cannot be there
// for every caller, and the build halts rather than render it. The remedy is the
// declaration, which is always available and changes nothing but when the
// attachment renders.
//
// It covers the templates that produce the attachment's own resources — its values
// mapping, its workload and its artifacts entries. A pipeline step's input or gate
// reading the same facts is ErrPipelineStepReadsCallerRenderFacts instead, whether
// or not the FeatureRecipe sets the key: a step never sees the connections, so the
// declaration this condition asks for would not make the read work.
//
// It is read from the authored template text before anything is evaluated, so a
// reference the text does not spell — one built at evaluation time, or written in
// content imported from a file or a library — is not seen by it. The declaration
// is the contract; this condition is the help an author gets when a template
// visibly needs it.
// Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrCallerRenderFactsNotDeclared()).
func ErrCallerRenderFactsNotDeclared() error { return errCallerRenderFactsNotDeclared }

var errCallerRenderFactsNotDeclared = cerrors.Register("caller-render-facts-not-declared", cerrors.Meta{
	ShortCode: "E0323",
	Message:   "template reads its callers' render facts, but the FeatureRecipe does not set readsCallerRenderFacts",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrPipelineStepReadsCallerRenderFacts fires when a pipeline step's input or gate
// — its `with:` or its `when:` — in the FeatureRecipe a per-component attachment
// binds reads the render facts of the components connecting to its host:
// `render_facts` reached beneath `.context.used_by`.
//
// A step's templates are evaluated without the connections made to the host, and
// that holds for every FeatureRecipe. A step may rewrite the host's own
// documents, so a step that could read who connects to the host would make the
// host's rendered output depend on its callers — and a component's own output is
// kept the same whoever uses it. Such a reference therefore reads nothing, and the
// build halts rather than render it as an empty value.
//
// It is raised whether or not the FeatureRecipe sets readsCallerRenderFacts. That
// key decides when the attachment renders, never what a step sees, so the remedy
// the undeclared condition names would not make the read work; this condition
// names the one that does. A step reads the host's own render facts at
// `.context.render_facts`, which come from the host's own render and make nothing
// depend on its callers; the callers' facts are read by the templates that produce
// the attachment's own resources.
//
// It is read from the authored template text before anything is evaluated, with
// the bound ErrCallerRenderFactsNotDeclared states: a reference the text does not
// spell is not seen by it.
// Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrPipelineStepReadsCallerRenderFacts()).
func ErrPipelineStepReadsCallerRenderFacts() error { return errPipelineStepReadsCallerRenderFacts }

var errPipelineStepReadsCallerRenderFacts = cerrors.Register("pipeline-step-reads-caller-render-facts", cerrors.Meta{
	ShortCode: "E0326",
	Message:   "pipeline step reads its callers' render facts, but a pipeline step does not read the components connecting to its host; it reads only the host's own render facts",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrTargetRenderFactsNotDeclared fires when a template in the FeatureRecipe a
// per-component attachment binds reads the render facts of the components its
// host is wired to — `render_facts` reached beneath `.context.uses` — while the
// FeatureRecipe does not set readsTargetRenderFacts.
//
// It is the twin of ErrCallerRenderFactsNotDeclared for the other end of a
// connection, and a condition of its own rather than that one widened, because
// its remedy is a different key. The declaration decides two things: that the
// attachment renders after every node has rendered, so a target later in the walk
// than the host has reported what it emitted by then, and that the connections on
// the attachment's context carry their targets' facts at all. A template reading
// them without the declaration is a template reading something that is not there,
// and the build halts rather than render it. The remedy is the declaration, which
// is always available and changes nothing but when the attachment renders and what
// its connections carry.
//
// It covers the templates that produce the attachment's own resources — its values
// mapping, its workload and its artifacts entries. A pipeline step's input or gate
// reading the same facts is ErrPipelineStepReadsTargetRenderFacts instead, whether
// or not the FeatureRecipe sets the key.
//
// It is read from the authored template text before anything is evaluated, with
// the bound ErrCallerRenderFactsNotDeclared states: a reference the text does not
// spell is not seen by it.
// Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrTargetRenderFactsNotDeclared()).
func ErrTargetRenderFactsNotDeclared() error { return errTargetRenderFactsNotDeclared }

var errTargetRenderFactsNotDeclared = cerrors.Register("target-render-facts-not-declared", cerrors.Meta{
	ShortCode: "E0347",
	Message:   "template reads the render facts of a component its host is wired to, but the FeatureRecipe does not set readsTargetRenderFacts",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrPipelineStepReadsTargetRenderFacts fires when a pipeline step's input or gate
// — its `with:` or its `when:` — in the FeatureRecipe a per-component attachment
// binds reads the render facts of the components its host is wired to:
// `render_facts` reached beneath `.context.uses`.
//
// A step's templates are evaluated with the connections the host makes, as the
// host's own Recipe reads them, and never with their targets' render facts — that
// holds for every FeatureRecipe. A step may rewrite the host's own documents, and
// the host's own render reads what its targets publish, never what they emitted;
// a step reading what they emitted would make the host's rendered output depend on
// their renders. Such a reference therefore reads nothing, and the build halts
// rather than render it as an empty value.
//
// It is raised whether or not the FeatureRecipe sets readsTargetRenderFacts. That
// key decides when the attachment renders and what the templates producing its own
// resources are handed, never what a step sees, so the remedy the undeclared
// condition names would not make the read work. A step reads the host's own render
// facts at `.context.render_facts`; the targets' facts are read by the templates
// that produce the attachment's own resources.
//
// It is read from the authored template text before anything is evaluated, with
// the bound ErrCallerRenderFactsNotDeclared states.
// Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrPipelineStepReadsTargetRenderFacts()).
func ErrPipelineStepReadsTargetRenderFacts() error { return errPipelineStepReadsTargetRenderFacts }

var errPipelineStepReadsTargetRenderFacts = cerrors.Register("pipeline-step-reads-target-render-facts", cerrors.Meta{
	ShortCode: "E0348",
	Message:   "pipeline step reads the render facts of a component its host is wired to, but a pipeline step reads only the host's own render facts",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errAssembledOutputCollision = cerrors.Register("assembled-output-collision", cerrors.Meta{
	ShortCode: "E0032",
	Message:   "assembled output collision: two contributors write the same output path",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrDerivedKeyCollision fires when one derived name is declared more than once
// in one key space by contributors whose output can reach each other — whether
// two contributors each derived it, or one contributor's single declaration
// carried it twice. A provider guest derives the names its output addresses
// resources by, and those names live inside the file bytes the host writes
// verbatim — so the host cannot see them. The guest declares them instead, and
// this sentinel is the halt when the declarations are not injective within a
// space: the colliding contributors halt loudly rather than one silently
// displacing the other in the output (a Loud Guardrail, per the No Ghost
// Infrastructure invariant).
//
// One contributor restating a name across its several render crossings is not a
// collision and does not fire this: the name describes one resource and the
// output carries it once. What fires is one declaration carrying the name twice,
// which is that contributor's own derivation failing to be injective. That half
// fires wherever the contributor's output lands, because it is a fault inside one
// contributor rather than a meeting between two.
//
// Which contributors can reach each other follows from what the provider does
// with the group's output. Where the provider folds the group's per-node output
// into one document, the contributors folded into it are compared against one
// another, and a contributor whose output is written as discrete files at its own
// position stands outside that document — its names meet nobody else's, and no
// rename would change that, so it is compared only within itself. Where the
// provider folds nothing — nesting each node under its own position, writing flat
// output, or declaring no assembly — every contributor to the group is compared
// against every other, because a name whose uniqueness is scoped to something
// wider than a file still collides across two files.
//
// The check does not interpret what a key means. It compares declared strings for
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
//
// The remedy is to change what is derived so each name is derived once within its
// space — renaming whichever contributor the author controls, or, where one
// contributor is named as having derived the name more than once, changing what
// one of its own resources is named after. It is stated here rather
// than in the registered wording, which names the collision and not its fix.
func ErrDerivedKeyCollision() error { return errDerivedKeyCollision }

var errDerivedKeyCollision = cerrors.Register("derived-key-collision", cerrors.Meta{
	ShortCode: "E0038",
	Message:   "derived name collision: one name is derived more than once in one key space",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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
// registry host — the `oci://…` form. It is shared by all three $import
// fragment surfaces: valuesMapping (`<Library>#/mappings/<entry>`),
// workload (`<Library>#/workloads/<entry>`) and artifacts
// (`<Library>#/artifacts/<entry>`). A library import is addressed by
// bare-name identity, never by physical registry location — the library
// name resolves against the loaded catalog and the version is supplied by
// the lockfile. A host in the reference is rejected so physical location
// cannot leak into authored source. The per-surface wrapping context names
// which surface carried the host.
//
// # Why no rule over the authored reference is declared for it
//
// The reference an entity itself authors never reaches this sentinel. A Recipe
// or FeatureRecipe carrying an `oci://` prefix at any of its three $import
// positions is refused when its document envelope is validated as the catalog
// loads, because no entity schema admits one there. That file is skipped, so the
// catalog holds nothing under the identity it declares, and every surface that
// renders then fails resolving the reference to it — ahead of any render. A rule
// declared over the authored reference could not be invoked ahead of that, and
// the declaration would read as enforcement while nothing could reach it.
//
// What still reaches this sentinel is the reference no entity schema describes:
// one lifted out of a library entry's own body by the recursion the mapping and
// workload resolvers perform. A library entry body is under no such constraint,
// and the reference is one the consuming entity never wrote, so no rule over
// that entity's authored text can see it however completely its own references
// are covered.
//
// The arms that raise this condition therefore stay where they are: they are the
// refusal for a reference the recursion lifts out of a library entry's body, for
// the values mapping and the workload a FeatureRecipe carries, and for a caller
// assembling a render's input without loading it through the catalog first.
func ErrHostInLibraryImport() error { return errHostInLibraryImport }

var errHostInLibraryImport = cerrors.Register("host-in-library-import", cerrors.Meta{
	ShortCode: "E0040",
	Message:   "library $import: carries a registry host; reference the library by bare-name identity",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errProviderNodeInputInvalid = cerrors.Register("provider-node-input-invalid", cerrors.Meta{
	ShortCode: "E0054",
	Message:   "provider rejected node input",
	RulePlace: cerrors.RuleEvaluatedElsewhere("The provider guest's input-validation export evaluates the rule over the node's resolved " +
		"input, inside its own module, and returns its diagnostics; this package raises the condition " +
		"on a halt-severity one. The rule runs where no view of a compilation reaches, so there is no " +
		"view for the rule dispatch to invoke a rule over."),
})

// ErrUnknownProvider is the compiler-level fatal halt produced by the
// build-time engineFactory when a Recipe declares a provider name that
// the CLI does not register. The CLI's engineFactory closure wraps this
// sentinel with the offending provider name so callers can match via
// errors.Is(err, ErrUnknownProvider()) regardless of which provider
// triggered the halt.
func ErrUnknownProvider() error { return errUnknownProvider }

var errUnknownProvider = cerrors.Register("unknown-provider", cerrors.Meta{
	ShortCode: "E0061",
	Message:   "unknown provider",
	RulePlace: cerrors.NotARule("A Recipe named a provider that resolution did not register. The predicate is the resolution " +
		"itself, so there is no resolved subject for a rule to be declared over: what it reports is " +
		"that none resolved."),
})

// ErrPathCollision fires when two boundaries with the same evaluated
// dispatch path target different providers AND the operator overrode
// subDirectory: to defeat the per-provider segregation default. The
// resulting per-(path, provider) dispatch produces multiple assembler
// invocations whose on-disk outputs would collide. Category: OPS_ERROR —
// the collision exists because of how the boundaries were configured, so
// it is the operator who has the change to make.
//
// It is raised once per boundary that fed a contested dispatch path, and
// each raise names the path, that boundary, and the provider that boundary's
// output was rendered by — so the conflicting providers and the conflicting
// boundaries are named across the set rather than listed in one line, and
// each finding is positioned at the Release document the boundary was
// declared in. The remedy is to restore the per-provider sub-path default or
// to change one boundary's directory, which the condition's registered
// wording does not yet state.
func ErrPathCollision() error { return errPathCollision }

var errPathCollision = cerrors.Register("path-collision", cerrors.Meta{
	ShortCode: "E0048",
	Message:   "dispatch path produced multiple provider outputs",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errInstanceIDCollision = cerrors.Register("instance-id-collision", cerrors.Meta{
	ShortCode: "E0041",
	Message:   "multiple contributors share an instance ID at the same dispatch path",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrChartNameMismatch fires when two or more boundaries' contributing
// components evaluate to the same dispatch path under a provider that names
// the group's assembly from a boundary annotation and requires that name to
// be one value, but the boundaries resolved that annotation — through the
// boundary annotation cascade — to different values. A merged assembly has
// exactly one name, so the compiler has no basis for choosing which of them
// it is. The remedy — cascade one value to every contributing boundary, or
// declare matching values on each explicitly — is stated here rather than in
// the registered wording, which names the conflict and not its fix.
// Category: TRANSLATION_ERROR.
func ErrChartNameMismatch() error { return errChartNameMismatch }

var errChartNameMismatch = cerrors.Register("chart-name-mismatch", cerrors.Meta{
	ShortCode: "E0036",
	Message:   "merged-path assembly has conflicting assembly-name annotation values",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errPathTemplateInvalidField = cerrors.Register("path-template-invalid-field", cerrors.Meta{
	ShortCode: "E0049",
	Message:   "path template references a field not in the documented allowlist",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errFRTemplateInvalidField = cerrors.Register("featurerecipe-template-invalid-field", cerrors.Meta{
	ShortCode: "E0039",
	Message:   "FeatureRecipe template references a per-component-only field with executionScope: boundary",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrBoundaryScopeAttachmentNotDeclaredAtABoundary fires when an attachment
// binds a FeatureRecipe with spec.executionScope: boundary and no boundary
// holding the component declares an attachment of that name.
//
// Such a FeatureRecipe generates once for a boundary rather than once for each
// component, so the boundary is where the attachment has to be written:
// .spec.boundaries.<scope>.features.<name> in the Release document, at the
// scope the component sits in or at one enclosing it. An attachment of that
// name written only against a component names no scope for the recipe to
// generate for, so nothing decides which boundary it would run at, which values
// it would read, or where its output would be written.
//
// It does not fire on a component that carries such an attachment while a scope
// enclosing it also declares one of that name. That is the ordinary shape of a
// boundary attachment reaching a component, and the Release renders exactly
// as it does when only the scope declares it.
//
// The remedy is to declare the attachment on the boundary it should generate
// for, or — where the intent was per-component generation — to bind a
// FeatureRecipe that does not declare the boundary scope. Category:
// CATALOG_ERROR.
func ErrBoundaryScopeAttachmentNotDeclaredAtABoundary() error {
	return errBoundaryScopeAttachmentNotDeclaredAtABoundary
}

var errBoundaryScopeAttachmentNotDeclaredAtABoundary = cerrors.Register(
	"boundary-scope-attachment-not-declared-at-a-boundary", cerrors.Meta{
		ShortCode: "E0259",
		Message: "attachment binds a FeatureRecipe with executionScope: boundary " +
			"and no boundary holding the component declares it",
		RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errAssertionFailed = cerrors.Register("authored-assertion-failed", cerrors.Meta{
	ShortCode: "E0033",
	Message:   "assertion failed",
	RulePlace: cerrors.NotARule("An assertion an author wrote in their own template evaluated to a failure. The predicate is " +
		"that template text rather than a rule this compiler declares, so there is no Go predicate " +
		"here for the rule dispatch to hold."),
})

// ErrPipelineTargetNotEmitted fires when a pipeline PATCH step addresses a
// document the keyed set does not hold — a scope/key pair absent from the
// node's emitted documents. It is the renderer's build-halting Loud
// Guardrail promoting the pipeline walk's local absent-target cause: the
// walk reports the typed condition; the integration halts the build with
// this sentinel, naming the offending step and the addressed key.
func ErrPipelineTargetNotEmitted() error { return errPipelineTargetNotEmitted }

var errPipelineTargetNotEmitted = cerrors.Register("pipeline-target-not-emitted", cerrors.Meta{
	ShortCode: "E0052",
	Message:   "pipeline PATCH targets a document the node did not emit",
	RulePlace: cerrors.NotARule("This condition carries no predicate of its own. It is the renderer's halting identity " +
		"for the pipeline walk's absent-target cause, selected by matching that cause as a node's walk " +
		"returns. What decides the underlying condition is whether the documents a node emitted hold the " +
		"scope and key an operation a guest returned addresses — both of them products of the render. No " +
		"predicate over authored content decides it."),
})

// ErrPipelineHostScopeUnavailable fires when a pipeline step performs a
// /host operation but the step has no host component — a boundary-scope
// FeatureRecipe step (which has no single host) or a Recipe step (whose
// only scope is its own output). The host root is absent for these
// surfaces, so a /host patch or emit halts loudly rather than silently
// resolving against an empty set.
//
// It also carries the finding the rules over a hostless site's pipeline steps
// report: a step whose export declares it operates on host documents, authored
// at a site standing against no host component, addresses a document set that
// site never has. That is reported once the build knows what every plugin and
// provider declares, ahead of any node's walk. The remedy is to move the step to
// a site that has a host component, address the site's own documents, or remove
// the step.
//
// The per-node halt described above remains the floor beneath that reading, and
// the two are a division of one condition rather than the same finding twice: a
// declared scope does not bind the scope of the operations a guest returns, so a
// step declaring its own scope may still return a host operation, and only the
// walk that ran it can see that. What a declaration can answer is answered from
// the declaration; what only the walk can answer stays with the walk.
func ErrPipelineHostScopeUnavailable() error { return errPipelineHostScopeUnavailable }

var errPipelineHostScopeUnavailable = cerrors.Register("pipeline-host-scope-unavailable", cerrors.Meta{
	ShortCode: "E0050",
	Message:   "pipeline /host operation from a step with no host component",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errPipelinePatchPreAssembly = cerrors.Register("pipeline-patch-targets-unassembled-fragments", cerrors.Meta{
	ShortCode: "E0051",
	Message:   "pipeline PATCH targets an assembler provider's pre-assembly fragments",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errPluginProviderIncompatible = cerrors.Register("plugin-provider-incompatible", cerrors.Meta{
	ShortCode: "E0053",
	Message:   "pipeline plugin is not compatible with the rendering provider",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrBoundaryAssemblerMissing fires when a provider that self-reports assembling
// a dispatch group into a single artifact (a merged docker-compose.yaml, an
// umbrella chart) is served by a rendering plugin that advertises per-node
// rendering but not the matching boundary-assembly export. The dispatched engine
// can render each node but cannot merge them, so assembly would be silently
// skipped and the build would emit unmerged per-node fragments. Per the No Ghost
// Infrastructure / Loud Guardrails invariant the build halts here instead, rather
// than producing an unassembled result the operator only discovers at debug time.
//
// It is raised by AssemblyGroupProviderHasItsDeclaredAssembler, once per offending
// dispatch group, positioned at the Release document and carrying no locator
// within it: an assembly path is evaluated output and a provider is selected, so
// neither is a key that document holds, and a build given no Release document
// leaves the finding unpositioned rather than mispositioned. Each objection names
// the provider and the dispatch path; the remedy — serve the provider through a
// plugin that advertises both render_node and assemble_boundary — is stated here
// rather than in the registered wording, which names the condition and not its
// fix. A group at no dispatch path is passed over, because output written at none
// is per-node output and no assembly was going to happen there.
// Category: TRANSLATION_ERROR.
func ErrBoundaryAssemblerMissing() error { return errBoundaryAssemblerMissing }

var errBoundaryAssemblerMissing = cerrors.Register("boundary-assembly-not-provided", cerrors.Meta{
	ShortCode: "E0035",
	Message:   "provider expected to assemble its dispatch group provides no boundary assembler",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactContentMismatch is the Loud Guardrail halt raised when an
// artifact's imported file ($import) has drifted from the content digest
// recorded for it in the entity.lock beside the Recipe or FeatureRecipe that
// imports it. The pin step records one artifactDigests pin there per $import
// artifact, digested from the file's bytes; the build re-digests the file and
// halts here when the two disagree, so an imported artifact edited after it was
// pinned never renders unverified, and a verifying pull reports the same
// disagreement under the same condition. The diagnostic is positioned at the
// importing document and names the artifact key, the imported path and both
// digests; the remedy is to pin the entity again if the change was intended, or
// restore the file. Category:
// CATALOG_ERROR (a catalog-authored imported artifact drifted from its recorded
// pin). Match with errors.Is(err, ErrArtifactContentMismatch()).
func ErrArtifactContentMismatch() error { return errArtifactContentMismatch }

var errArtifactContentMismatch = cerrors.Register("artifact-content-mismatch", cerrors.Meta{
	ShortCode: "E0026",
	Message:   "imported artifact content does not match the digest recorded for it in entity.lock",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ArtifactContentPinMismatch returns the halt a build raises for an imported file
// whose content no longer digests to the pin recorded for it, for a command that
// finds the same disagreement outside a build.
//
// It composes the finding from the parts the build's own checks compose it from —
// the registered sentence, the detail list and the advice — and renders the detail
// and the position through the constraint framework's own spelling, so the two
// commands cannot come to report one disagreement in two ways. It is positioned
// at the entry inside the document that authors the import, which is where the
// build's check over a Recipe's own entry positions the same finding.
//
// A build reaching the same entry through a FeatureRecipe attachment adds the
// attachment to its detail, because a node may carry several; a caller here holds
// the document itself, so it has no attachment to name.
//
// artifact is the name the entity lock pins the entry under. A Recipe's boundary
// block pins its imports under a prefixed name (recipepin.BoundaryArtifactPinName),
// so such a name positions the finding at that block's entry rather than at an
// artifacts entry, while the detail names it as the lock records it.
func ArtifactContentPinMismatch(documentPath, artifact, importedPath, current, recorded string) error {
	condition := ErrArtifactContentMismatch()
	locator := constraint.ArtifactEntryLocator(artifact)
	if key, inBoundary := recipepin.BoundaryArtifactKeyOf(artifact); inBoundary {
		locator = constraint.BoundaryArtifactEntryLocator(key)
	}
	return cerrors.Wrap(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: documentPath, Locator: locator},
		cerrors.ComposeMessage(condition,
			constraint.RenderDetail(artifactContentPinDetail(artifact, importedPath, current, recorded)...),
			artifactContentPinAdvice))
}

// ErrCrossProviderLinkUnsupported marks a cross-boundary uses edge whose two
// ends assemble into dispatch groups rendered by different providers, where at
// least one of those providers advertises cross-boundary linking. Native
// cross-reference wiring between two different providers' assembled artifacts
// is not supported: a provider's link exports produce wiring in that
// provider's own artifact vocabulary, and one provider cannot resolve a
// reference published in another's. Rather than emit a wrong reference, the
// link pass leaves the existing per-node scalar value-pipe in place and edits
// no file.
//
// The finding is raised by the declared advisory
// [CrossBoundaryLinkEndsShareOneProvider], once per such connection, positioned
// at the site the connection was authored in. Its message names the far end, the
// socket and each end's provider; the position names the document and the key an
// author edits. It is an advisory and not a build halt — the edge still carries
// its scalar value across the boundary — so the build completes with the
// value-pipe fallback rather than failing.
//
// Two ends whose providers differ are always in different dispatch groups,
// because a group is keyed by an assembly path and a provider together. So the
// condition turns on the two providers and their capability claims, and on where
// either end was placed not at all.
// Match with errors.Is(err, ErrCrossProviderLinkUnsupported()).
func ErrCrossProviderLinkUnsupported() error { return errCrossProviderLinkUnsupported }

var errCrossProviderLinkUnsupported = cerrors.Register("cross-provider-link-unsupported", cerrors.Meta{
	ShortCode: "E0037",
	Message:   "cross-boundary link between endpoints rendered by different providers is not supported",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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
// provider-side non-determinism in synthesized output). Match with
// errors.Is(err, ErrRenderNondeterministic()).
func ErrRenderNondeterministic() error { return errRenderNondeterministic }

var errRenderNondeterministic = cerrors.Register("render-nondeterministic", cerrors.Meta{
	ShortCode: "E0058",
	Message:   "provider render is not deterministic",
	RulePlace: cerrors.NotARule("A provider guest rendered one node twice from identical input and returned different bytes. " +
		"The comparison drives that guest twice and so produces its own comparand, and what it " +
		"reports is a guest that is not deterministic rather than a predicate over a fact anything " +
		"held."),
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

var errOrphanCrossBoundaryLinkSentinel = cerrors.Register("orphan-cross-boundary-link-placeholder", cerrors.Meta{
	ShortCode: "E0047",
	Message:   "assembled output retains an unresolved cross-boundary link placeholder",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrCrossBoundaryLinkValueNotCopied is the Loud Guardrail halt raised when a
// template does more than copy a value it reads off a connection whose two ends
// render into different outputs. On such a connection the producer's provides
// values reach a template as placeholders the provider later rewrites into its own
// cross-reference, so a template may write one whole or inside a larger string,
// but a comparison, a branch, a length or a function that changes the value would
// evaluate against the placeholder rather than the value. The host proves the rule
// by evaluating the template over the placeholders and over the real values and
// halting where the two disagree once each placeholder is written back. The
// diagnostic names the template and, for each provides key involved, the
// consuming component, the connection's alias and socket, and the producing
// component. Category: CATALOG_ERROR, because the template that reads the value is
// the Recipe's or the FeatureRecipe's. Match with
// errors.Is(err, ErrCrossBoundaryLinkValueNotCopied()).
func ErrCrossBoundaryLinkValueNotCopied() error { return errCrossBoundaryLinkValueNotCopied }

var errCrossBoundaryLinkValueNotCopied = cerrors.Register("cross-boundary-link-value-not-copied", cerrors.Meta{
	ShortCode: "E0354",
	Message:   "a template does more than copy a value it reads off a cross-boundary link",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrNestSegmentNotOneDirectory is the Loud Guardrail halt raised when a
// provider declares a per-node sub-directory that is not a single directory
// name. A nesting provider names the directory its contributors' own
// sub-directories sit under, and the orchestrator composes each node's identity
// into it — so a value carrying a path separator, or naming the current or the
// parent directory, would place a node's files somewhere other than beneath the
// node's own position in the dispatch group. That is either an escape out of the
// dispatch position or a silent re-layout of the group, so the build halts here
// instead of composing the path. The diagnostic names the provider that declared
// the value and the value itself, so an author sees which provider to change and
// what it declared. The remedy is to name one directory. A provider that names
// nothing is not affected: its contributors then sit directly at the dispatch
// position. Category: TRANSLATION_ERROR, because the value came from a
// provider's own self-description with nothing in any catalog document to
// correct. Match with errors.Is(err, ErrNestSegmentNotOneDirectory()).
func ErrNestSegmentNotOneDirectory() error { return errNestSegmentNotOneDirectory }

var errNestSegmentNotOneDirectory = cerrors.Register("nest-segment-not-one-directory", cerrors.Meta{
	ShortCode: "E0250",
	Message:   "provider named a directory for each contributor's own sub-directory that is not a single directory name",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrBoundaryDirectoryNotBeneathOutputRoot is the Loud Guardrail halt raised when
// a Release boundary's `directory:` or `subDirectory:`, once evaluated and
// cleaned, does not name a directory beneath the output root: it climbs out of
// it (`..`, `../x`, `a/../..`), names an absolute location, or names no
// directory at all — empty, or cleaning to the output root itself.
//
// The evaluated path is the directory every file the boundary's components
// render is written under, and it is what groups contributors into one dispatch
// group, so it is cleaned once before either: `a//b`, `./a/b` and `a/b/` are one
// directory and form one group. A path that climbs out would otherwise be refused
// only when the files are written, through no condition a reader could look up;
// a path naming the root itself would put the boundary's output at the top of the
// output directory, beside every other boundary's. `subDirectory: "."`, and a
// `subDirectory:` template that evaluates to an empty string, are untouched: they
// write a boundary's output directly in its `directory:`.
//
// The diagnostic names the Release, the boundary, the field at fault and the
// value it evaluated to, together with the path that value produced, because the
// value is often a template whose evaluation the author has not seen. Category:
// OPS_ERROR, because the value comes from a Release the operator wrote. Match
// with errors.Is(err, ErrBoundaryDirectoryNotBeneathOutputRoot()).
func ErrBoundaryDirectoryNotBeneathOutputRoot() error {
	return errBoundaryDirectoryNotBeneathOutputRoot
}

var errBoundaryDirectoryNotBeneathOutputRoot = cerrors.Register("boundary-directory-not-beneath-output-root", cerrors.Meta{
	ShortCode: "E0352",
	Message:   "boundary output directory does not name a directory beneath the output root",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// boundaryDirectoryAdvice is the remedy a refused boundary output directory names.
const boundaryDirectoryAdvice = "write a directory beneath the output root, such as \"deploy/prod\" — no leading " +
	"\"/\", no \"..\" climbing above it, and not the output root itself"

// ErrBoundaryFeatureNameNotADirectory is the Loud Guardrail halt raised when a
// Feature attached at a Release boundary is named `.` or `..` and the provider
// rendering it gives each contributor a directory of its own.
//
// Such a provider places a boundary-level Feature's files in a directory named
// for the instance alone, beside the boundary's components. `.` and `..` are not
// directory names: the first would put the Feature's files in the directory that
// holds every contributor, the second in the one above it, so the files would
// land outside the position the provider gave the Feature. The build halts rather
// than composing that directory. Any other name the Release admits is untouched,
// a dotted one such as `audit.v2` included.
//
// The diagnostic names the Release, the boundary and the instance; the remedy is
// to rename the instance. Category: OPS_ERROR, because the instance name comes
// from a Release the operator wrote. Match with
// errors.Is(err, ErrBoundaryFeatureNameNotADirectory()).
func ErrBoundaryFeatureNameNotADirectory() error { return errBoundaryFeatureNameNotADirectory }

var errBoundaryFeatureNameNotADirectory = cerrors.Register("boundary-feature-name-not-a-directory", cerrors.Meta{
	ShortCode: "E0353",
	Message:   "boundary-level Feature instance name is not a directory name",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// boundaryFeatureNameAdvice is the remedy a refused boundary-level Feature
// instance name names.
const boundaryFeatureNameAdvice = "rename the Feature instance under the boundary's features to an ordinary " +
	"name, such as \"audit\""

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

var errEmptySource = cerrors.Register("recipe-source-is-empty", cerrors.Meta{
	ShortCode: "E0251",
	Message:   "recipe source is an empty string, which names neither sourceless mode nor sourced mode",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrArtifactLibraryFileEscapesKey is the Loud Guardrail halt raised when a
// file inside an ArtifactLibrary bundle carries a name that does not name a file
// of its own beneath the artifacts key the bundle was consumed at. One artifacts
// entry importing a bundle expands into one output per bundle file, each nested
// under that key — so the key names a directory and every file the bundle
// contributes belongs beneath it. A file name climbing out of that directory,
// naming an absolute location, or naming nothing at all does not belong beneath
// it, and the build halts here rather than placing the file somewhere the
// consuming Recipe never named.
//
// Naming nothing covers every name that is not one file's one spelling beneath
// the key. "." and "./", and a name that collapses back onto the key such as
// "x/..", name the key itself, so the file would be written where the
// directory belongs. A name with a trailing or doubled separator, or a "." or
// ".." part, such as "x/", "a//b" or "a/./b", is a second spelling of a file
// another name can carry too: "a//b" beside "a/b" is two names for one file, and
// the later one overwrites the earlier without any collision being reported.
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
// A file name written plainly beneath the key's directory is untouched,
// including one naming a subdirectory of its own, such as "rbac/role.yaml": a
// bundle is free to carry a nested layout, and nesting that layout under the
// consumption key is exactly what the key is for.
//
// The consuming Recipe and the published bundle are usually written by different
// authors, so the diagnostic names both halves — the artifacts key the bundle
// was consumed at and the file name the bundle carries — together with the
// remedy, which is to give the file an ordinary name beneath that key.
// Category: CATALOG_ERROR, because the offending value came from an authored
// catalog document, unlike the provider-supplied sub-directory name one layer up
// that ErrNestSegmentNotOneDirectory refuses as a translation fault. Match with
// errors.Is(err, ErrArtifactLibraryFileEscapesKey()).
func ErrArtifactLibraryFileEscapesKey() error { return errArtifactLibraryFileEscapesKey }

var errArtifactLibraryFileEscapesKey = cerrors.Register("artifact-library-file-escapes-key", cerrors.Meta{
	ShortCode: "E0252",
	Message:   "artifact-library bundle file name does not name a file of its own beneath the artifacts key its output is nested under",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// artifactLibraryFileNameAdvice is the remedy every refusal of a bundle file name
// states — the render walk's and the rule's declared over the same names — so an
// author is told one thing whichever of the two caught the name.
const artifactLibraryFileNameAdvice = "rename the file inside the ArtifactLibrary bundle to an ordinary file name " +
	"beneath the artifacts key it is consumed at, such as \"config.yaml\" or \"rbac/role.yaml\" — no \".\" or \"..\" " +
	"part, and no leading, trailing or doubled \"/\""

// ErrComponentIdentityTargetRefuses is the Loud Guardrail halt raised when a
// component identity an AUTHOR wrote yields a name the target it renders for
// will not accept, and no shorter name would help either.
//
// It is the refusal half of the split between a name a person wrote and a name
// the compiler composed. A composed identity is shortened until the target
// accepts it, because its parts come from several authoring domains and no
// single author can see the length it reaches. An authored identity is not
// shortened: it is the deployed name of the workload, so rewriting it would
// rename every resource in every existing output, and the person who can fix it
// is the person who wrote it. So this halt names the field they wrote and the
// target's own reason for refusing, and leaves the name alone.
//
// The reason and the kind of name it covers both come from the guest that
// renders for the target, verbatim: what a target accepts is that target's
// knowledge, and restating it here would put a second copy of it in the
// compiler, where it would drift from the check that actually refuses.
// Category: CATALOG_ERROR — the offending value is a name in an authored
// document. Match with errors.Is(err, ErrComponentIdentityTargetRefuses()).
func ErrComponentIdentityTargetRefuses() error { return errComponentIdentityTargetRefuses }

var errComponentIdentityTargetRefuses = cerrors.Register("component-identity-target-refuses", cerrors.Meta{
	ShortCode: "E0263",
	Message:   "the target will not accept a name derived from this component's authored identity: rename the component",
	RulePlace: cerrors.RuleEvaluatedElsewhere("The provider guest that renders for the target decides whether a name derived from the " +
		"component's authored identity is one the target accepts, and returns its own reason and the " +
		"kind of name it covers verbatim. The grammar belongs to the target and is the guest's to " +
		"know, so no view of a compilation carries the fact this turns on."),
})

// ErrNameBoundNotConverging is the Loud Guardrail halt raised when a provider
// guest keeps refusing the names the host composes for it and never accepts one.
//
// Shortening and refusal converge by design: the host proposes an identity, the
// guest bounds it, the host composes a shorter one, and the guest is asked
// again. A second refusal is the MECHANISM rather than a fault — shortening to a
// length can land on a character the target's grammar refuses in the position it
// landed in, and the guest saying so with a smaller bound is how host-generic
// shortening and target-specific grammar agree without the host learning the
// grammar. What is a fault is a guest that never stops: one that returns a bound
// it then refuses, or that walks a bound down by nothing.
//
// The build halts and names the guest rather than truncating the name to
// something the guest never accepted. A silently truncated name would be a name
// no target agreed to, stamped on real resources, with nothing in the output
// recording that the agreement failed — and the defect would then be found at
// apply time, in the deployment, rather than here in the build that produced it.
// Category: TRANSLATION_ERROR — the fault is in the plugin's answer, not in
// anything an author wrote. Match with errors.Is(err, ErrNameBoundNotConverging()).
func ErrNameBoundNotConverging() error { return errNameBoundNotConverging }

var errNameBoundNotConverging = cerrors.Register("provider-refused-every-component-identity", cerrors.Meta{
	ShortCode: "E0264",
	Message:   "provider guest never accepted a component identity the compiler composed for it",
	RulePlace: cerrors.NotARule("A provider guest refused every name the host composed for it and never accepted one — " +
		"returning a bound it then refuses, or walking a bound down by nothing. It reports a guest " +
		"that does not honour the convergence contract rather than anything an author wrote."),
})

// ErrUnsupportedInputKind is the Loud Guardrail halt raised when the input kind
// a node resolved to is not one the provider bound to that node supports.
//
// The condition is a provider capability rather than a semantic opinion about
// what a node ought to declare: the compiler reads the input kinds the
// provider's contract claims and compares them against the kind the node's
// input blocks classify to, and it holds no view on which kinds a node should
// carry. One identity covers both authoring surfaces, because the gate is one
// gate — a Recipe on a component and a FeatureRecipe on an attachment are
// classified and compared identically — so the sentence names the node and its
// provider rather than either entity kind, and stays true on both paths.
//
// Why the condition is identified at all, rather than left as a categorised
// message with no identity, is worth stating here so it is not later read as
// bookkeeping. A halt with no identity can be named by no coverage declaration,
// so its published entry sits outside the comparison that holds every other
// entry's quoted output against what a build prints — and an entry outside that
// comparison is exactly where a message prefix no build emits can sit
// indefinitely with every check green, because nothing compares it to anything.
// An unheld entry is a defect that recurs; a short code is stable by design,
// never reissued even when its condition is withdrawn. That asymmetry is what
// makes the identity the cheaper side of the trade, and the cost paid for it is
// real: the code appears in a head users match on and is published permanently.
//
// Category: CATALOG_ERROR — the pairing at fault is between an authored catalog
// document's input blocks and the provider that document binds. Match with
// errors.Is(err, ErrUnsupportedInputKind()).
func ErrUnsupportedInputKind() error { return errUnsupportedInputKind }

var errUnsupportedInputKind = cerrors.Register("unsupported-input-kind", cerrors.Meta{
	ShortCode: "E0265",
	Message:   "provider does not support the node's input kind",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnsupportedOmits is the Loud Guardrail halt raised when a Recipe or a
// FeatureRecipe declares omits and the provider rendering it does not declare that
// it honours them.
//
// Leaving a rendered document out is something only the provider can do, because
// only the provider knows what its source renders and in which terms a document is
// named. So the entries are handed to the provider and the provider does the
// leaving out — and a provider that does not do it would render every document the
// author asked to leave out and report success. The host reads the provider's own
// declared claim and refuses before anything is dispatched, so an author is told at
// once that the key has no effect on this provider rather than discovering the
// document in the output. The refusal is the declared rules'
// [OmitsProviderHonoursTheEntries], [AttachmentOmitsProviderHonoursTheEntries] and
// [BoundaryAttachmentOmitsProviderHonoursTheEntries], one per document that can
// carry the list, asked before the first node renders and positioned where the
// component or the attachment was written.
//
// The claim is the provider's own self-description, never a table of provider names
// held here: a provider that starts honouring omits declares it, and this refusal
// stops applying to it with no change to the compiler. Any entry declared counts,
// whatever its when renders, because whether the provider honours the key does not
// depend on which entries a build keeps. One identity covers a Recipe and a
// FeatureRecipe, for the reason the input-kind refusal beside it gives.
//
// Category: CATALOG_ERROR — the pairing at fault is between an authored catalog
// document and the provider that document binds. Match with
// errors.Is(err, ErrUnsupportedOmits()).
func ErrUnsupportedOmits() error { return errUnsupportedOmits }

var errUnsupportedOmits = cerrors.Register("unsupported-omits", cerrors.Meta{
	ShortCode: "E0335",
	Message:   "provider does not support omits",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnsupportedBoundaryArtifacts is the Loud Guardrail halt raised when a Recipe
// declares spec.boundary.artifacts and the provider rendering it does not declare
// that it accepts the files a Recipe contributes to its boundary's shared output.
//
// The host writes those files itself, once, in the directory holding the dispatch
// group's assembled output, and the provider is handed none of them. So whether
// such a file means anything is a property of the provider's target, which only
// the provider can state: a target that does not read a file placed beside its
// assembled output would ship the file and ignore it, and the author would
// discover only once the Release was applied that the configuration they wrote
// never took effect.
// The host reads the provider's own declared claim and refuses before anything is
// dispatched, so the author is told at once. The refusal is the declared rule
// [BoundaryArtifactsProviderAcceptsTheFiles], asked before the first node renders
// and positioned where the component was written.
//
// The claim is the provider's own self-description, never a table of provider
// names held here: a provider that starts accepting the files declares it, and
// this refusal stops applying to it with no change to the compiler. A FeatureRecipe
// carries no such block, so the one rule is over a node's Recipe.
//
// Category: CATALOG_ERROR — the pairing at fault is between an authored Recipe and
// the provider it binds. Match with errors.Is(err, ErrUnsupportedBoundaryArtifacts()).
func ErrUnsupportedBoundaryArtifacts() error { return errUnsupportedBoundaryArtifacts }

var errUnsupportedBoundaryArtifacts = cerrors.Register("unsupported-boundary-artifacts", cerrors.Meta{
	ShortCode: "E0351",
	Message:   "provider does not support boundary artifacts",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrOmitMatchedNothing is the Loud Guardrail halt raised when an omit entry a
// render kept left out nothing the source rendered: the provider reported that the
// entry matched no document.
//
// An entry that matches nothing is a statement the author made about the source
// that is no longer true — most often a chart upgrade that renamed the document, or
// a typo in the name — and the document the author meant to leave out may now be in
// the output under its new name. Passing over it would ship exactly what the entry
// was written to keep out, so the build halts and names the entry: its position in
// the list the author wrote, and the kind, name, namespace and subchart it resolved to, with
// any of those its template produced from a value declared sensitive withheld. The
// first such entry in the order the author wrote them is the one named, so the same
// entry is named on every run.
//
// An entry whose when dropped it is never handed over and never checked: it is the
// author's statement that the entry does not apply to this build.
//
// Category: CATALOG_ERROR — the entry is in an authored catalog document. Match with
// errors.Is(err, ErrOmitMatchedNothing()).
func ErrOmitMatchedNothing() error { return errOmitMatchedNothing }

var errOmitMatchedNothing = cerrors.Register("omit-matched-nothing", cerrors.Meta{
	ShortCode: "E0334",
	Message:   "an omits entry left out nothing the source rendered",
	RulePlace: cerrors.RuleEvaluatedElsewhere("The provider guest rendering the source decides whether an omit entry " +
		"matches a document, comparing it in its own format's terms against what it rendered, and returns how many " +
		"documents each entry matched; the host halts on a count of zero. Which documents a source renders is known " +
		"only inside that guest, so no view of a compilation carries the fact this turns on."),
})

// ErrOmitReportMismatch is the Loud Guardrail halt raised when a provider that
// declares it honours omits returns anything but one count per entry it was handed:
// no counts, too few, too many, or a count below zero.
//
// The counts are the provider's whole account of what it left out — the documents
// themselves are simply absent from what it returned — so a report that does not
// line up with the entries leaves the host unable to tell whether an entry left
// anything out at all, and an entry left unaccounted for is exactly the case the
// zero-count refusal exists to catch. Which count was meant for which entry is a
// question only the provider can answer, so the build halts and names how many
// entries were handed over and how many counts came back.
//
// It is raised against what a guest RETURNED, so the reader holding it is someone
// debugging a provider they wrote; the wording is the plugin contract's. Category:
// CATALOG_ERROR. Match with errors.Is(err, ErrOmitReportMismatch()).
func ErrOmitReportMismatch() error { return errOmitReportMismatch }

var errOmitReportMismatch = cerrors.Register("omit-report-mismatch", cerrors.Meta{
	ShortCode: "E0336",
	Message:   "a provider's omitted counts do not match the omits entries it was handed",
	RulePlace: cerrors.NotARule("A provider guest that declares it honours omit entries returned a count list that " +
		"does not hold one non-negative count per entry it was handed. Which count it meant for which entry is a " +
		"question only that guest can answer, so it reports a guest whose returned output does not conform to the " +
		"contract rather than anything an author wrote."),
})

// The four conditions below share an audience and a reason for carrying an
// identity, so the reason is stated once here rather than four times.
//
// Each is a refusal of the plugin contract this project publishes, and each is
// raised against what a guest RETURNED rather than against anything an author
// wrote. No corpus of authored documents can reach any of them: an Aggregate, a
// Release, a Recipe and a Manifest together decide which guest runs and what it
// is handed, and none of them decides what the guest hands back. So the reader
// holding one of these diagnostics is someone debugging a guest they wrote, and
// the wording is theirs — the vocabulary is the export and wire-field names the
// published plugin contract teaches, and nothing else.
//
// They carry identities for the same reason every other halting condition does,
// and the audience does not weaken it. A code is what a reader quotes in a
// report, searches for, and matches on programmatically, and a refusal without
// one leaves a plugin author holding a sentence and no handle. It is also what
// admits the condition to the published reference at all, where the entry's
// quoted output is held against what a build actually prints; an unidentified
// halt can be named by no coverage declaration, so its documentation would sit
// outside that comparison indefinitely.

// ErrProviderRenderDuplicatePath fires when a provider guest returns two output
// files under one path, so the second would overwrite the first.
//
// The host collects the returned files into a set keyed by path and writes each
// file's bytes verbatim, running no serializer of its own. A duplicate key
// therefore has no correct resolution available to the host: taking the last
// wins silently discards a file the guest said to write, and taking the first
// discards a later one just as silently. Which of the two the guest meant is a
// question only the guest can answer, so the build halts and names the path.
//
// One identity covers both surfaces a provider guest returns files on — the
// per-node render and the boundary assembly of a dispatch group — because the
// guest mistake and its remedy are the same on both, and a reader acts on the
// path the message names rather than on which export produced it. The sentence
// names neither export for that reason: a sentence naming one would be false
// wherever the other raised it, and the surface is already evident from the
// position the diagnostic carries. Match with
// errors.Is(err, ErrProviderRenderDuplicatePath()).
// Category: CATALOG_ERROR.
func ErrProviderRenderDuplicatePath() error { return errProviderRenderDuplicatePath }

var errProviderRenderDuplicatePath = cerrors.Register("provider-render-duplicate-path", cerrors.Meta{
	ShortCode: "E0269",
	Message: "provider guest returned two output files under one path; " +
		"a conforming guest returns each output path at most once",
	RulePlace: cerrors.NotARule("A provider guest returned two output files under one path, so one would overwrite the other. " +
		"Which of the two the guest meant is a question only that guest can answer, so it reports a " +
		"guest whose returned output does not conform to the contract rather than anything an author " +
		"wrote."),
})

// DuplicateRenderPathErr raises [ErrProviderRenderDuplicatePath] for the given
// duplicated output path, naming the node it was returned for when the caller
// has one.
//
// It is exported because the surfaces that detect the duplicate are not all in
// this package: a guest's files are collected per node and again when a dispatch
// group is assembled. Every one of those calls this rather than composing its
// own message, so the condition has one raise implementation and cannot come to
// be worded two ways — which is what the several independent duplicate-path
// checks that preceded it had done.
//
// An empty nodeID contributes no node to the message rather than an empty one,
// so a caller with no node in hand produces a sentence about the path alone
// instead of one trailing an empty value.
func DuplicateRenderPathErr(path, nodeID string) error {
	values := []string{fmt.Sprintf("path %q", path)}
	if nodeID != "" {
		values = append(values, fmt.Sprintf("node %q", nodeID))
	}
	return cerrors.Wrap(ErrProviderRenderDuplicatePath(), cerrors.CatalogError,
		cerrors.SourceLocation{File: path},
		cerrors.ComposeMessage(ErrProviderRenderDuplicatePath(), values, ""))
}

// ErrProviderRenderFactNotList fires when a provider guest reports a render fact
// whose value is not a list.
//
// Every render fact a provider reports carries a list, even when it holds one
// entry, because one name can be reported by more than one render of the same
// component — its own render calls, and the renders of the Features attached to
// it — and the lists are joined into one under that name for the templates that
// read it. A value that is not a list has no join a template could rely on, and
// whether it would have been the only report under its name is something no
// provider can know when it renders. So the value is refused where the guest
// hands it over, naming the provider and the fact, rather than passed on to fail
// or vanish in a template far from its cause.
//
// It is the one thing the compiler learns about a value: that it is a list,
// read from the value's own syntax. What a name or an entry means stays the
// provider's documented contract. Match with
// errors.Is(err, ErrProviderRenderFactNotList()).
// Category: CATALOG_ERROR.
func ErrProviderRenderFactNotList() error { return errProviderRenderFactNotList }

var errProviderRenderFactNotList = cerrors.Register("provider-render-fact-not-list", cerrors.Meta{
	ShortCode: "E0325",
	Message: "provider guest reported a render fact whose value is not a list; " +
		"a conforming guest reports every render fact's value as a JSON array, even with one entry",
	RulePlace: cerrors.NotARule("A provider guest reported a render fact whose value is not a list. Which value " +
		"the guest meant to report is a question only that guest can answer, so it reports a guest whose " +
		"returned output does not conform to the contract rather than anything an author wrote."),
})

// RequireRenderFactLists raises [ErrProviderRenderFactNotList] for the first fact
// in reported whose value is not a list, naming the fact, the node it was
// reported for and the provider that reported it, and returns nil when every
// fact's value is a list.
//
// It is the one place the rule is applied, and it is exported because the
// crossing that hands a guest's report over is in another package. Each fact is
// judged by the first token of its value, which is all the rule needs: a JSON
// value is a list exactly when it opens with an array bracket. Nothing else of
// the value is read.
func RequireRenderFactLists(reported []RenderFact, nodeID, provider string) error {
	for _, fact := range reported {
		kind := renderFactValueKind(fact.Value)
		if kind == renderFactListKind {
			continue
		}
		values := []string{fmt.Sprintf("fact %q", fact.Name)}
		if nodeID != "" {
			values = append(values, fmt.Sprintf("node %q", nodeID))
		}
		if provider != "" {
			values = append(values, fmt.Sprintf("provider %q", provider))
		}
		values = append(values, "its value is "+kind)
		return cerrors.Wrap(ErrProviderRenderFactNotList(), cerrors.CatalogError,
			cerrors.SourceLocation{File: nodeID},
			cerrors.ComposeMessage(ErrProviderRenderFactNotList(), values, ""))
	}
	return nil
}

// renderFactListKind is what renderFactValueKind answers for a list.
const renderFactListKind = "a list"

// renderFactValueKind names the kind of JSON value raw holds, read from its first
// token alone: renderFactListKind for an array and the kind of any other value,
// which is what the refusal tells a guest's author it found instead.
func renderFactValueKind(raw []byte) string {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 {
		return "empty"
	}
	switch trimmed[0] {
	case '[':
		return renderFactListKind
	case '{':
		return "an object"
	case '"':
		return "a string"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	default:
		return "a number"
	}
}

// ErrLinkPatchFileNotEmitted fires when a provider guest returns artifact
// wiring for a cross-boundary link that names an output file the dispatch group
// it applies to did not emit.
//
// The wiring is applied by a later host pass that carves the named file, applies
// the ordered operations, and reassembles it byte-faithfully. A name that
// matches no emitted file leaves that pass nothing to carve, and the operations
// it carries are the wiring that resolves the link — so dropping them would
// leave assembled output referencing an endpoint nothing wired, which surfaces
// far from its cause. The build halts and names the file instead.
// Category: TRANSLATION_ERROR.
func ErrLinkPatchFileNotEmitted() error { return errLinkPatchFileNotEmitted }

var errLinkPatchFileNotEmitted = cerrors.Register("link-patch-file-not-emitted", cerrors.Meta{
	ShortCode: "E0270",
	Message: "link patch names an output file the dispatch group did not emit; " +
		"a conforming link patch names a file the group's own render returned",
	RulePlace: cerrors.NotARule("A provider guest returned link wiring naming an output file the dispatch group never " +
		"emitted. The wiring is that guest's own return value and the file set is that group's own " +
		"render, so it reports a guest whose output does not conform to the contract it renders " +
		"under."),
})

// ErrPipelineEmitFileAbsent fires when a pipeline step's guest returns a side
// effect declaring the emit_file kind while carrying no file to write.
//
// It is the narrower of the two side-effect refusals and the distinction is
// worth keeping: the kind here is one the contract DOES define, and the arm that
// reads a file has already been selected on the strength of it. What is absent
// is the member that arm needs — a path to write at and the content to write —
// so the guest declared an intention the payload does not carry out. The
// remedy names the two fields rather than the kind, because the kind is right.
// Category: CATALOG_ERROR.
func ErrPipelineEmitFileAbsent() error { return errPipelineEmitFileAbsent }

var errPipelineEmitFileAbsent = cerrors.Register("pipeline-emit-file-absent", cerrors.Meta{
	ShortCode: "E0271",
	Message: "emit_file side effect carries no file; " +
		"a conforming emit_file side effect carries a file with its path and content",
	RulePlace: cerrors.NotARule("A pipeline step's guest returned a side effect declaring the emit_file kind while carrying " +
		"no file to write. It reports a guest whose returned payload does not carry the member the " +
		"kind it declared requires."),
})

// ErrPipelineSideEffectKindUndefined fires when a pipeline step's guest returns
// a side effect whose kind the plugin contract does not define, so the host has
// no arm that knows what bytes the effect contributes.
//
// The kind is an open string on the wire, which is what lets a later effect kind
// extend the contract without breaking the ABI. That openness is why the refusal
// exists: an undefined kind cannot be told apart from a future one by its shape
// alone, so a host that passed it over would silently drop whatever the guest
// meant by it and emit output missing an effect the step returned. Halting is
// the reading that keeps the extension point open without making it a hole —
// a kind this build does not define is reported as undefined, naming what the
// guest sent, rather than accepted and ignored.
// Category: CATALOG_ERROR.
func ErrPipelineSideEffectKindUndefined() error { return errPipelineSideEffectKindUndefined }

var errPipelineSideEffectKindUndefined = cerrors.Register("pipeline-side-effect-kind-undefined", cerrors.Meta{
	ShortCode: "E0272",
	Message: "side effect declares a kind the plugin contract does not define; " +
		"a conforming side effect declares emit_file or emit_crd",
	RulePlace: cerrors.NotARule("A pipeline step's guest returned a side effect whose kind the plugin contract does not " +
		"define, so the host has no arm that knows what bytes it contributes. It reports a guest " +
		"whose returned output names something outside the contract."),
})

// ErrRenderedNameUnavailable fires when a Recipe writes a template reading the
// name its provider gives the resources it renders for the node — from source the
// author supplied, or from a workload it synthesizes — and that provider reports
// no such name for the node.
//
// It halts rather than resolving the reference away, and the reason is the defect
// it exists to remove. An empty value here does not fail: it renders, the build
// finishes, and the value the template produced names a resource that was never
// emitted — a fault that surfaces at deployment time, far from the template that
// caused it. Refusing the reference reports it where the author can act on it.
//
// It reaches every authored surface the value lands on rather than one of them —
// a Recipe's provides entries, its values mapping, its workload body, each of its
// artifacts entries, and each pipeline step's input and its gate — because all of
// them render against the node's own context, so the reference resolves away on
// every one alike. The registered sentence therefore names a template rather than
// any single surface; which surface an occurrence sits on reaches the reader as
// the position the finding is reported at, and as the field its detail names where
// the position cannot spell it.
//
// It is a fact rather than a guess, which is what makes halting the honest
// reading: whether the node's provider reports such a name is settled once the
// Recipe binding selects that provider, before anything is evaluated.
//
// The remedy names what to write instead, because in most cases there is a
// correct answer and it is already reachable: a node that supplies no source, on a
// target that reports no name for it, has its output named after the node's
// identity. A refusal that only refuses sends an author looking for a key that
// does not exist.
// Category: CATALOG_ERROR.
func ErrRenderedNameUnavailable() error { return errRenderedNameUnavailable }

var errRenderedNameUnavailable = cerrors.Register("rendered-name-unavailable", cerrors.Meta{
	ShortCode: "E0273",
	Message: "template reads the name the target gives the resources it renders for the node, and this " +
		"node's provider reports no such name; a conforming template reads a value the node's own provider supplies",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
