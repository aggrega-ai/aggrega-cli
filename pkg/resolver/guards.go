// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
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
	// co-located .wasm sits in beside its plugin.yaml — with the root a read there
	// is held to, the one that catalog directory picks. The build-time plugin
	// cascade's co-located tier reads it: a binary that embeds no core guests
	// resolves them from beside their catalog entities. Keyed by artifact
	// reference so the cascade's entity → artifact translation leaves the tier's
	// lookup unchanged. May be nil or empty in catalog-less paths or when no
	// catalog directory holds a co-located .wasm, which disables the tier.
	PluginColocatedDirs map[string]pluginpath.ColocatedDir
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
	// ProviderBindingFault judges one workspace provider binding — its
	// capability, and the plugin identity its uri names, empty for a
	// capability-only binding — whether or not any Recipe consults it, and
	// answers through the same capability resolution ProviderPlugin reads. It
	// returns nil when there is nothing to report; an error wrapping the
	// binding-unsatisfied condition when the binding names a plugin this catalog
	// holds that does not serve the capability, which halts; and an error
	// wrapping the unserved-capability advisory when a capability-only binding
	// names a capability no provider-rendering plugin this catalog holds serves,
	// which is reported and never halts. May be nil in catalog-less paths, which
	// judges nothing.
	ProviderBindingFault func(capability, uri string) error
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
	// Recipe returns the catalog Recipe a Manifest mapping value names, plus a
	// found flag. The reference is the mapping value verbatim, in whichever of
	// the accepted reference shapes the author wrote it. May be nil, and a nil
	// field finds nothing — which a rule over a mapping entry reads as an entry
	// whose recipe does not resolve, the same answer it reads for a reference the
	// catalog holds no entry for, and which it passes over rather than reports.
	Recipe func(reference string) (types.Recipe, bool)
	// FeatureRecipe is the FeatureRecipe analogue of Recipe, for a mapping value
	// written under the features tier. It is a separate field rather than one
	// lookup spanning both, because the two are separate catalog kinds reached
	// through separate lookups, and one field would have to pick a kind and would
	// answer for the wrong one wherever the other applied. May be nil, on the same
	// terms.
	FeatureRecipe func(reference string) (types.FeatureRecipe, bool)
	// ComponentAncestry reports the extends ancestry of a component type: the
	// mapping key of every base it descends from, nearest parent first. A type the
	// resolver finds no entry for has no ancestry and no error, because "the type
	// is absent from the catalog" belongs to the path that resolves types; a chain
	// that cannot be walked returns the chain's own fault, so a caller reports the
	// chain rather than a mapping.
	//
	// It is supplied per caller rather than derived from the Component lookup
	// beside it, and that is load-bearing. A catalog shell assembled over a
	// backing whose Component index is empty by construction resolves no ancestry
	// at all, and a gate wired to such a shell's own resolver would refuse every
	// mapping onto a base's Recipe — silently, and only on that path. So the
	// backing that holds the Components supplies the resolver. May be nil, which
	// declares that the caller's catalog resolves no ancestry.
	ComponentAncestry func(componentType string) ([]string, error)
	// NodeRecipeResolution reports what the Manifest's mapping table and this
	// catalog together say about one component instance's Recipe: the reference
	// the mapping names, whether the catalog holds a Recipe at it, the keys a
	// version-tolerant match could not choose between, and the Recipes that
	// implement the instance's type.
	//
	// It takes the Manifest because the answer is a joint one. The mapping table
	// alone says which reference an instance's type names and the catalog alone
	// says which references resolve; neither answers "did this instance end up
	// with a Recipe", which is the question. The two are joined by whichever
	// backing holds the recipe index, so the join happens once and every reader
	// reads that one answer.
	//
	// It is a derivation the compiler already performs rather than a new reach:
	// the same resolution decides which Recipe a node renders through.
	//
	// May be nil, and a nil field resolves nothing — every instance comes back
	// mapped to no reference, which is the same answer a caller reads for an
	// instance the Manifest's mapping does not reach. That is the reading the
	// catalog-presence lookups beside it already take, and it is the honest one
	// here for the same reason: a surface holding no recipe index has resolved no
	// instance to a Recipe, and reporting otherwise would claim a resolution
	// nothing performed.
	NodeRecipeResolution func(
		manifest *types.Manifest, instanceName, componentType string,
	) NodeRecipeResolution
	// LocalSourcePins returns the pins the pin step would record now for the local
	// source of the Recipe or FeatureRecipe declared in the document at
	// documentPath: one per file the source contributes to what the entity
	// carries, digested as it stands. A build compares them with the pins the
	// lock beside the document records before a provider reads the source.
	//
	// It is a catalog read because which files a source contributes is the
	// catalog's answer — the same one a publication packs and the pin step pins —
	// and the render reaches it through this field rather than enumerating the
	// source a second way.
	//
	// May be nil, which declares that the caller can say nothing about a local
	// source. A render that meets recorded local source pins with no way to
	// compute the current ones refuses rather than admitting the source unchecked.
	LocalSourcePins func(documentPath string) ([]types.LocalSourceDigest, error)
}

// NodeRecipeResolution is what one component instance's Recipe resolution came
// to: the four coordinates a reader needs to tell the outcomes apart.
//
// The outcomes are held apart rather than collapsed into a reference-or-not
// answer, because they have different remedies and one of them is not the
// instance's fault at all. An instance the Manifest maps to a reference the
// catalog holds is resolved. An instance no mapping key reaches needs a mapping
// entry written. An instance whose mapping names a reference the catalog does
// not hold needs the reference corrected or the Recipe published. And an
// instance whose type matches several version-distinct mapping keys is a defect
// in the mapping table rather than in the instance — the lookup cannot choose
// without guessing, so it refuses.
//
// Collapsing any pair of those would send an author to the wrong file, which is
// why this carries four coordinates rather than a reference and a flag.
type NodeRecipeResolution struct {
	// Reference is the Recipe reference the Manifest's mapping names for this
	// instance, verbatim as the author wrote it. Empty when Mapped is false.
	Reference string

	// Mapped reports whether the Manifest's mapping reached this instance at
	// all, under the name-wins-over-type precedence the mapping table carries.
	// It is false both for an instance nothing maps and for one whose lookup
	// refused, which is why AmbiguousKeys is read first by anything that has to
	// tell those two apart.
	Mapped bool

	// Present reports whether the catalog holds a Recipe at Reference. It is
	// false whenever Mapped is false, because there is no reference to resolve.
	Present bool

	// AmbiguousKeys are the version-distinct mapping keys this instance's type
	// matched when no key matched it exactly and more than one matched its base.
	// Empty when the lookup settled on one key or on none.
	//
	// The keys travel rather than a bare flag because they are the remedy: an
	// author told which keys collide can pin the authored type's version or
	// remove a duplicate key, and told only that the lookup was ambiguous can do
	// neither.
	AmbiguousKeys []string

	// PinCandidates are the catalog Recipes whose declared implements satisfies
	// this instance's type, as identities an author can write into a mapping
	// value, sorted.
	//
	// They are a suggestion and never a resolution: resolution is mapping-only,
	// so a Recipe listed here is a candidate the author may pin and never one
	// the compiler will choose. The set is resolved only for an instance that
	// needs one — an instance the Manifest already maps is offered none, because
	// scanning the recipe index for it would pay for a set nothing reads.
	PinCandidates []string

	// PinCandidatesFromPinsAlone reports that PinCandidates were enumerated from
	// the Recipes the lockfile pins and nothing else: the catalog read no catalog
	// directory, so every Recipe it holds came from a registry by way of a pin. No
	// registry is ever enumerated, so there an empty PinCandidates says only that
	// no pinned Recipe implements the type, never that no Recipe does — a
	// registry may publish several, and a Manifest mapping none of them pins
	// none.
	//
	// It travels with the candidates rather than beside the catalog because it
	// qualifies them: it is read off the same recipe set in the same resolution,
	// so a reader holding the candidates holds the scope they were searched in.
	// It is false wherever PinCandidates is not resolved.
	PinCandidatesFromPinsAlone bool

	// TypeKey is the type-axis mapping key the lookup settled on for this
	// instance, verbatim as the Manifest declares it. Empty when MappedByType is
	// false.
	//
	// It travels because a key written without a version says nothing about
	// which version of the type the Recipe serves. The mapping-consistency rule
	// judges such a key's Recipe against the instance's own version instead, and
	// only the lookup knows which key it settled on: the reference alone cannot
	// be traced back to a key, since several keys may name one reference.
	TypeKey string

	// MappedByType reports whether a type-axis key reached this instance. It is
	// false for an instance a name-axis entry reached, which keys an instance and
	// says nothing about a type, and false whenever Mapped is false.
	MappedByType bool

	// ResolvedType is this instance's component type at the version the build
	// compiles it as. Read through a lookup scoped to a Release's instance
	// versions, it is the type at the version the instance-version table settled
	// on, whatever form the type is written in. Otherwise it is the type as
	// written where it pins a version, the version the catalog's own version
	// selection resolves a bare type to where it pins none, and the type as
	// written where the catalog holds no Component of it.
	//
	// It is the request the mapped Recipe's implements is judged against when an
	// unversioned key reached the instance, and it is resolved by the one
	// resolution the pin-candidate enumeration asks with, so the two cannot
	// disagree about which version a bare type stands for.
	ResolvedType string
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
// Release wrote is not attributable to the Aggregate — and it is why the
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
// own `uses` block, the Release's per-edge `uses`, and the connection set the
// composition produced. Two of the three are in scope here and the third is not,
// so a reading taken at this point would answer for the components the
// Aggregate declares directly and would have to decline for the rest — one
// rule in two mechanisms, each seeing a different half of the input. The rule is
// declared over the composition instead, where all three surfaces have
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
// Returns no findings when the Aggregate document is absent.
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
//
// releaseSource is the workspace-relative file the Release was read from, empty
// when the caller holds none. A rule asked over an entry the Release wrote
// records it, so its finding names the document its locator is a path inside;
// the rules are asked, and positioned, the same either way.
func RunGuards(
	cancelled constraint.Cancellation,
	na types.NormalizedAggregate,
	release *types.Release,
	releaseSource string,
	catalog CatalogLookup,
	boundaryInfra map[string]*BoundaryInfra,
	parameters map[string]any,
) ([]*ResolverError, error) {
	agg := na.Aggregate()
	if agg == nil {
		return nil, nil
	}
	contracts := newNormalizedView(na, release, catalog, parameters)

	errs, err := runDeclaredRules(cancelled, contracts, releaseSource,
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
// correction is on the declaration: an Aggregate author handed a component
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

// releaseDocumentName renders the Release document a finding names. A build
// with no Release document names the kind alone; nothing it could be asked
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
// rule reading what the Release wrote names the Release, so an operator
// reading the diagnostic is sent to the file they wrote in rather than to the
// Aggregate's. A rule whose axis spans both documents takes neither for the whole
// set: each of its findings names the document its own subject was written in.
//
// A rule may also name NEITHER, and that binding is not a special case of
// the two: it is the answer for a rule reporting a divergence inside the
// compiler, which no author wrote and no author can correct. Such a rule's
// findings carry no document and no locator, because the only thing a subject
// could do there is send a reader to a key that is not at fault.
//
// Each traversal call NAMES its rule as a literal argument, and that is a
// constraint of the framework rather than a style: a call written any other way
// makes the rule it invokes underivable. Which rules a caller invokes
// is derived by reading this module's source for a traversal entry called with a
// declared rule named at the call site; a helper taking a rule and calling the
// traversal itself would name a parameter there instead, and the derivation would
// report an invocation it can attribute to no rule — leaving every rule reached
// that way indistinguishable from one nothing invokes at all.
func runDeclaredRules(
	cancelled constraint.Cancellation,
	view *normalizedView,
	releaseSource string,
	developerDocument string,
	operatorDocument string,
) ([]*ResolverError, error) {
	coordinates := newCoordinateSet(view).withReleaseSource(releaseSource)
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
	// The binding for a rule whose axis spans both documents: each finding names
	// the document its own subject was written in, so one rule over a block either
	// author can write sends each author to their own file.
	authoredRule := func(objections []constraint.Objection, err error) error {
		if err != nil {
			return err
		}
		errs = append(errs, findingsAtTheirAuthoringDocument(objections, developerDocument, operatorDocument)...)
		return nil
	}
	// The binding for a rule whose finding belongs to neither document.
	// A rule reporting a divergence inside the compiler has no author to send a
	// reader to, so its findings name no document and sit at no locator; binding
	// it here rather than letting it take the developer document is what keeps
	// the choice of subject beside the choice of rule, where a reader can see
	// that one rule was bound differently and why.
	compilerFaultRule := func(objections []constraint.Objection, err error) error {
		if err != nil {
			return err
		}
		errs = append(errs, findingsWithNoSubject(objections)...)
		return nil
	}

	// Once per component.
	if err := compilerFaultRule(constraint.RunOnNodes(
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

	// Once per connection, and once per entry the Release's per-edge surface
	// writes. The operator's per-edge sizing is judged on the entry the operator
	// wrote, so its findings name that entry in the Release; it keeps its place
	// between the developer's per-edge rule and the next, so the order the two
	// documents' findings reach the author in is unchanged.
	if err := developerRule(constraint.RunOnEdges(
		cancelled, coordinates, facts, connectionValuesConformToTheSocket)); err != nil {
		return nil, err
	}
	if err := operatorRule(constraint.RunOnReleaseEdgeConfigurations(
		cancelled, coordinates, facts, connectionInfraConformsToTheSocket)); err != nil {
		return nil, err
	}
	if err := developerRule(constraint.RunOnEdges(
		cancelled, coordinates, facts, connectionInjectReferenceIsPublished)); err != nil {
		return nil, err
	}
	// The declared-slots rule for a connection's attachments, the developer's half.
	// The operator's half is asked once the composition is flattened, where the
	// Release's per-edge entries are enumerated with the site each was written at.
	if err := developerRule(constraint.RunOnEdges(
		cancelled, coordinates, facts, connectionAttachmentFillsADeclaredSlot)); err != nil {
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
	// The Release's own rule is invoked over the set positioning each attachment
	// at the Release's entry, so its finding names the block the operator wrote;
	// the axis is the same one the developer's rule above walks.
	if err := operatorRule(constraint.RunOnNodeAttachmentSockets(
		cancelled, coordinates, coordinates.inRelease(), coordinates, facts,
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
	if err := developerRule(constraint.RunOnNodeMechanisms(
		cancelled, coordinates, coordinates, facts, selfInjectKeyWrittenOnce)); err != nil {
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

	// Once per delivery mechanism of every injection block either document
	// writes. The shape first, so a block faulting both ways reads the mechanism
	// that delivers nothing ahead of the entries that say two things.
	if err := authoredRule(constraint.RunOnInjectDeliveries(
		cancelled, coordinates, facts, injectDeliveryIsAList)); err != nil {
		return nil, err
	}
	if err := authoredRule(constraint.RunOnInjectDeliveries(
		cancelled, coordinates, facts, injectDeliveryEntryIsUnambiguous)); err != nil {
		return nil, err
	}
	if err := authoredRule(constraint.RunOnInjectDeliveries(
		cancelled, coordinates, facts, attachmentInjectEntryNamesAKey)); err != nil {
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
