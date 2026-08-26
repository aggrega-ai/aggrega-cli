// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// CatalogLookup is the resolver-side adapter to catalog reads. The two
// function fields return a typed entity for a type URI, plus a found flag;
// the resolver guards consult them to validate Aggregate-side type
// references against catalog declarations (usesSchema, featuresSchema).
// The typical caller — cmd/aggrega/build.go
// — supplies the lookup via cat.CatalogLookup() after loading the catalog.
//
// Either function field may be nil. A nil field finds nothing, so a consumer
// reading it sees the same answer it sees for an identity the catalog holds no
// entry for; [RunGuards] states what that means for the rules it evaluates, and
// each other consumer's own documentation states it for that consumer.
type CatalogLookup struct {
	// Component returns the catalog Component for a type URI. Returns false if not found.
	Component func(typeURI string) (types.Component, bool)
	// Feature returns the catalog Feature for a type URI. Returns false if not found.
	Feature func(typeURI string) (types.Feature, bool)
	// Plugin returns the catalog Plugin entity for an entity URI, plus a
	// found flag. A plugin socket fronts the artifact-resolving cascade with this
	// lookup: entity URI → entity → its bare-identity artifact reference → cascade.
	// May be nil in catalog-less paths; a
	// socket that needs it then resolves nothing and halts as not-an-entity.
	Plugin func(uri string) (types.Plugin, bool)
	// PluginColocatedDirs maps a Plugin entity's bare-identity artifact reference
	// to the catalog directory its entity was indexed from — the directory a
	// co-located .wasm sits in beside its plugin.yaml. The build-time plugin
	// cascade's co-located tier reads it: a binary that embeds no core guests
	// resolves them from beside their catalog entities. Keyed by artifact
	// reference so the cascade's entity → artifact translation leaves the tier's
	// lookup unchanged. May be nil or empty in catalog-less paths or when no
	// catalog directory holds a co-located .wasm, which disables the tier.
	PluginColocatedDirs map[string]string
	// PluginFromCache resolves a Plugin entity from the pull-populated cache by its
	// entity URI WITHOUT a lockfile pin — the cache-not-pin read a verdict socket
	// (policy / linter) falls back to when the pinned / Tier-1 path does not
	// surface the gate plugin. It returns the parsed entity, the .wasm layer
	// content digest the artifact cache slot is keyed by, and a found flag; ok is
	// false on a clean cache miss. May be nil in catalog-less paths.
	PluginFromCache func(uri string) (types.Plugin, string, bool, error)
	// ProviderPlugin resolves a provider binding to the provider-rendering Plugin
	// entity serving it, plus a found flag and an error. The binding is the bare
	// provider name ("helm") or a version-pinned form ("helm:1.0.0", "helm:^1.0.0")
	// where the pin constrains the plugin version. A pluginType: provider Plugin
	// serves the provider names in its capabilities; this accessor finds the
	// unique such Plugin whose version satisfies the pin (highest-in-range for a
	// caret, highest overall when unpinned). A bare miss returns (_, false, nil),
	// leaving the binding served by no plugin — the render path reports that as an
	// unknown provider and halts; a version pin nothing satisfies returns a
	// non-nil error so the caller halts loudly. May be nil in catalog-less
	// paths; a consumer that needs it then resolves nothing.
	ProviderPlugin func(ref string) (types.Plugin, bool, error)
	// SchemaLibraries is the catalog's resolved SchemaLibrary set keyed by
	// library metadata.name, used to resolve cross-library schema `$ref`s when
	// compiling a Component's schema (whole-pillar and base+flavor composition).
	// May be nil in catalog-less or not-yet-primed paths; consumers that need it
	// guard on nil and fall back to inline-only behavior.
	SchemaLibraries map[string]types.SchemaLibrary
	// EffectiveSchema returns the composed (parent+child) schema for an entity
	// that declares extends:, keyed by type URI, in compactform form. Present
	// only for extends-ing entities; consumers (the schema-conformance gate,
	// the inject-key provides extraction) prefer it over compiling the entity's
	// own authored schema, so an inherited constraint is enforced on a child.
	// Returns ("", false) — i.e. (nil, false) — for a non-extends entity, which
	// falls the caller back to its authored schema. May itself be nil in
	// catalog-less / not-yet-resolved paths.
	EffectiveSchema func(typeURI string) (compactform.Schema, bool)
	// ParserPlugin resolves a source format to the parser Plugin entity that
	// advertises it — the pluginType: parser Plugin whose capabilities list the
	// composed parse:<format> string. A socket that must parse a foreign source
	// format (the render-policy stage handing a policy plugin a non-native
	// artifact's tree) fronts the parser-resolution seam with this lookup. It
	// halts loudly on an unadvertised or ambiguous format rather than returning a
	// soft miss — a requested format either has an advertiser or the build cannot
	// proceed. May be nil in catalog-less paths; a consumer that needs it then
	// resolves nothing and halts as no-parser-available.
	ParserPlugin func(format string) (types.Plugin, error)
}

// RunGuards evaluates the rules that can be asked of the two authoring documents
// before the composition is flattened, and returns one finding per violation.
//
// Almost every rule it asks is a declared value rather than a function invoked
// here: each names the sentinel it enforces, what a violation does to the build,
// the facts it reads and what it is invoked once per. This suite hands each one
// to the traversal its subject kind is walked by, so a rule's point in the
// compilation follows from the facts it reads rather than from where a call
// happens to sit, and the declarations themselves are the statement of how many
// there are — a list written here beside them would be a second copy that reads
// as current while lagging the set it names.
//
// One decision is made here and only one: which authoring document each rule's
// findings name. That is the rule's subject matter — a rule measuring what the
// deployment wrote is not attributable to the application — and it is why the
// conversion takes the document as a parameter instead of deriving one for
// everything.
//
// # What is asked here, and what is not
//
// Neither of the two attachment REQUIREMENT rules is asked here, and for the
// same reason: a component's required-attachment-type mandate and a slot's
// `required: true` are both satisfied by the node's final merged attachment set,
// which does not exist until the attachment cascade has run. Both are evaluated
// over the finalised graph instead. What IS asked here is the shape of what the
// author wrote, which is a question about the authored document and belongs at
// its authoring site.
//
// The required uses-socket rule is not asked here either, and the reason is a
// third one worth keeping separate from the cascade above. Three surfaces can
// populate a socket a Component's usesSchema declares required: the component's
// own `uses` block, the deployment's per-edge `uses`, and the connection set the
// composition produced. Two of the three are in scope here and the third is not,
// so a reading taken at this point would answer for the components the
// application declares directly and would have to decline for the rest — one
// rule in two mechanisms, each seeing a different half of the input. The rule is
// declared over the composed application instead, where all three surfaces have
// contributed, and asked once over every node.
//
// # The catalog
//
// The catalog is read as a fact through the presentation, so a rule about what a
// component type declares reads the same empty answer for a type the catalog
// holds no entry for as for a type declaring nothing, and simply ends there. The
// rule reporting a type resolved as a catalog identity that the catalog holds no
// entry for is declared separately, which is what lets every other rule end
// rather than carry its own copy of that classification. A lookup that finds
// nothing at all is therefore reported by that one rule, once per component
// naming such a type, rather than skipped in silence by each rule in turn.
//
// # Cancellation, and its grain
//
// cancelled is read once per outermost coordinate of each traversal — per
// component, and per declared scope — and a walk that sees it turn hands back
// nothing rather than a truncated set, which is why this returns an error
// alongside the findings. A caller with no cancellation concern passes nil. The
// inner coordinates a component holds — its attachments, its delivery mechanisms
// and their keys — carry no additional read, so the bound is one component's
// authored surface rather than one objection.
//
// # Order
//
// All violations are collected; no rule short-circuits another. Findings come
// back rule by rule, and within a rule in the order its traversal invoked it,
// which every axis of the enumeration promises — so identical input yields a
// byte-identical sequence.
//
// Returns no findings when the application document is absent.
//
// The derived contracts the rules read are resolved ONCE, here, and every rule
// that needs one reads it rather than compiling it again. Compiling per rule
// would multiply the work by the number of rules, and — because the compile
// entry reports every failure the same way it reports an absent contract — would
// give each rule its own chance to read a compile failure as a type that simply
// declares nothing, validate nothing, and say nothing. One resolution has one
// place a failure can be named.
//
// # Where a contract that did not compile is named
//
// That one place is here, and it is read before the rules are asked. A contract
// that failed to compile is why the rules over it would say nothing, so it is
// reported ahead of them: reporting it afterwards would put the cause of a
// silence beneath findings the silence suppressed. The resolution keeps the
// failure — see [normalizedView.UnresolvableContracts] — and this reads it
// rather than testing any contract again, so the classification has one producer
// and one reader.
//
// A build carrying such a failure halts, and no rule over the affected subject
// runs to completion in a way a reader could trust. That order is the whole
// point: a rule reading the fact sees the same absence an unauthored contract
// presents, so it cannot distinguish the two and must not be asked to.
func RunGuards(
	cancelled constraint.Cancellation,
	na types.NormalizedAggregate,
	release *types.Release,
	catalog CatalogLookup,
	boundaryInfra map[string]*BoundaryInfra,
	parameters map[string]any,
) ([]*ResolverError, error) {
	agg := na.Aggregate()
	if agg == nil {
		return nil, nil
	}
	contracts := newNormalizedView(na, release, catalog, parameters)

	errs, err := runDeclaredRules(cancelled, contracts,
		fmt.Sprintf("Aggregate %s", agg.Metadata.Name), releaseDocumentName(release))
	if err != nil {
		return nil, err
	}
	return append(contractCompileFailures(contracts), errs...), nil
}

// contractCompileFailures renders every declared contract this build could not
// compile as a halting finding, in the order the resolution names them.
//
// Each finding is attributed to the catalog entity that DECLARED the contract
// rather than to the document that wrote the configuration, because the
// correction is on the declaration: an application author handed a component
// type whose contract does not compile has nothing to change in their own file.
// The locator is the authored field inside that entity, so the diagnostic names
// the key the fix goes in.
//
// The message names the compile failure verbatim. That text already carries the
// offending key, the source line and the keyword, and re-deriving any of it here
// would be a second answer to what went wrong.
func contractCompileFailures(v *normalizedView) []*ResolverError {
	failures := v.UnresolvableContracts()
	if len(failures) == 0 {
		return nil
	}
	out := make([]*ResolverError, 0, len(failures))
	for _, f := range failures {
		out = append(out, &ResolverError{
			Entity:  fmt.Sprintf("Component %s", f.ComponentType),
			Field:   contractField(f),
			Message: f.Err.Error(),
			Err:     ErrSchemaContractCompileFailure(),
		})
	}
	return out
}

// contractField locates a contract inside the entity declaring it: a component
// type's own contract sits at its schema field, and a socket's per-edge contract
// at the pillar field of that socket's entry.
//
// Which of the two a failure is about is read from whether it names a socket,
// which is the same reading the resolution used to key it — so a failure cannot
// be located as one kind of contract and reported as the other.
//
// The pillar is mapped to the field that declares it rather than printed as the
// resolution names it, because the two spellings differ: a pillar is keyed by the
// configuration it covers and the socket declares it under a field of its own. A
// pillar outside the pair a socket can declare is named as it arrived, so a
// diagnostic still locates something rather than claiming the wrong field.
func contractField(f ContractResolutionFailure) string {
	if f.Socket == "" {
		return ".spec.schema"
	}
	field := f.Pillar
	switch f.Pillar {
	case "values":
		field = "valueSchema"
	case "infra":
		field = "infraSchema"
	}
	return fmt.Sprintf(".spec.usesSchema.%s.%s", f.Socket, field)
}

// releaseDocumentName renders the deployment document a finding names. A build
// with no deployment document names the kind alone; nothing it could be asked
// about exists, so no rule attributed to it raises.
func releaseDocumentName(release *types.Release) string {
	if release == nil {
		return "Release"
	}
	return fmt.Sprintf("Release %s", release.Metadata.Name)
}

// runDeclaredRules asks every rule declared over the two pre-flatten documents.
//
// developerDocument and operatorDocument are the two authoring documents a
// finding can name. Which one each rule takes is the rule's subject matter: a
// rule reading what the deployment wrote names the deployment, so an operator
// reading the diagnostic is sent to the file they wrote in rather than to the
// application's.
//
// 🔴 Each traversal call NAMES its rule as a literal argument, and that is a
// constraint of the framework rather than a style. Which rules a caller invokes
// is derived by reading this module's source for a traversal entry called with a
// declared rule named at the call site; a helper taking a rule and calling the
// traversal itself would name a parameter there instead, and the derivation would
// report an invocation it can attribute to no rule — leaving every rule reached
// that way indistinguishable from one nothing invokes at all.
func runDeclaredRules(
	cancelled constraint.Cancellation,
	view *normalizedView,
	developerDocument string,
	operatorDocument string,
) ([]*ResolverError, error) {
	coordinates := newCoordinateSet(view)
	facts := constraint.NormalizedView(view)

	var errs []*ResolverError
	// A traversal that stopped on the cancellation signal ends the whole suite:
	// a partial set of findings would report a document as satisfying rules that
	// were never asked of it.
	collect := func(document string, objections []constraint.Objection, err error) error {
		if err != nil {
			return err
		}
		errs = append(errs, FindingsFromObjections(objections, document)...)
		return nil
	}
	developerRule := func(objections []constraint.Objection, err error) error {
		return collect(developerDocument, objections, err)
	}
	operatorRule := func(objections []constraint.Objection, err error) error {
		return collect(operatorDocument, objections, err)
	}

	// Once per component.
	if err := developerRule(constraint.RunOnNodes(
		cancelled, coordinates, facts, componentTypeResolvesToACatalogEntry)); err != nil {
		return nil, err
	}
	if err := developerRule(constraint.RunOnNodes(
		cancelled, coordinates, facts, componentValuesConformToTheType)); err != nil {
		return nil, err
	}
	if err := operatorRule(constraint.RunOnNodes(
		cancelled, coordinates, facts, componentInfraConformsToTheType)); err != nil {
		return nil, err
	}
	if err := developerRule(constraint.RunOnNodes(
		cancelled, coordinates, facts, selfInjectBlockAllowed)); err != nil {
		return nil, err
	}
	if err := operatorRule(constraint.RunOnNodes(
		cancelled, coordinates, facts, releaseEdgeSocketDeclared)); err != nil {
		return nil, err
	}

	// Once per connection.
	if err := developerRule(constraint.RunOnEdges(
		cancelled, coordinates, facts, connectionValuesConformToTheSocket)); err != nil {
		return nil, err
	}
	if err := operatorRule(constraint.RunOnEdges(
		cancelled, coordinates, facts, connectionInfraConformsToTheSocket)); err != nil {
		return nil, err
	}
	if err := developerRule(constraint.RunOnEdges(
		cancelled, coordinates, facts, connectionInjectReferenceIsPublished)); err != nil {
		return nil, err
	}

	// Once per socket an attachment on a component wires. Ahead of the
	// per-attachment rules below, which is the position the two held before the
	// framework began walking their socket axis: findings reach the author in
	// invocation order, so moving a rule to a finer axis must not also move where
	// its findings land in the report.
	if err := developerRule(constraint.RunOnNodeAttachmentSockets(
		cancelled, coordinates, coordinates, coordinates, facts,
		componentAttachmentSocketDeclared)); err != nil {
		return nil, err
	}
	if err := operatorRule(constraint.RunOnNodeAttachmentSockets(
		cancelled, coordinates, coordinates, coordinates, facts,
		releaseComponentAttachmentSocketDeclared)); err != nil {
		return nil, err
	}

	// Once per attachment a component carries.
	if err := developerRule(constraint.RunOnNodeAttachments(
		cancelled, coordinates, coordinates, facts, attachmentTypeAgreesAcrossDocuments)); err != nil {
		return nil, err
	}
	if err := developerRule(constraint.RunOnNodeAttachments(
		cancelled, coordinates, coordinates, facts, attachmentDeliveryIsSlotPermitted)); err != nil {
		return nil, err
	}

	// Once per delivery mechanism a component's injection block dictates.
	if err := developerRule(constraint.RunOnNodeMechanisms(
		cancelled, coordinates, coordinates, facts, selfInjectMechanismAllowed)); err != nil {
		return nil, err
	}

	// Once per key a component delivers through one mechanism.
	if err := developerRule(constraint.RunOnNodeMechanismDeliveryKeys(
		cancelled, coordinates, coordinates, coordinates, facts,
		selfInjectReferenceIsPublished)); err != nil {
		return nil, err
	}
	if err := developerRule(constraint.RunOnNodeMechanismDeliveryKeys(
		cancelled, coordinates, coordinates, coordinates, facts,
		selfInjectKeyNotAlsoOnAConnection)); err != nil {
		return nil, err
	}
	if err := developerRule(constraint.RunOnNodeMechanismDeliveryKeys(
		cancelled, coordinates, coordinates, coordinates, facts,
		selfInjectKeyNotOnTwoMechanisms)); err != nil {
		return nil, err
	}

	// Once per attachment a declared scope of either document carries.
	if err := developerRule(constraint.RunOnSubsystemAttachments(
		cancelled, coordinates, coordinates, facts, subsystemAttachmentSocketDeclared)); err != nil {
		return nil, err
	}
	if err := operatorRule(constraint.RunOnBoundaryAttachments(
		cancelled, coordinates, coordinates, facts, boundaryAttachmentSocketDeclared)); err != nil {
		return nil, err
	}

	return errs, nil
}
