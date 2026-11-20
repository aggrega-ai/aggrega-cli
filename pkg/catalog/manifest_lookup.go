// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ErrManifestImplementsMismatch fires when a Manifest mapping entry's
// resolved Recipe (or FeatureRecipe) carries a spec.implements that
// doesn't match the Manifest's component-type / feature-type key for
// that entry. The mismatch is a structural consistency invariant of
// the mapping authoring surface: the Manifest entry IS the Recipe URI;
// the Recipe's spec.implements is a consistency anchor, not a dispatch
// mechanism. Mismatches halt at workspace load.
//
// It is a re-export of the condition the rule that raises it declares. The rule
// reads the catalog through the resolver-side lookup, so it is declared there,
// and this name is kept because it is the one a caller in this package's
// vocabulary matches on — the value is the same, so errors.Is against either
// name matches the same faults.
func ErrManifestImplementsMismatch() error { return resolver.ErrManifestImplementsMismatch() }

// ErrAmbiguousTypeMapping fires when a component/feature type resolves to more
// than one version-distinct Manifest mapping key under version-tolerant
// matching — the lookup cannot pick one without guessing, so it halts loudly
// rather than silently resolving the first.
func ErrAmbiguousTypeMapping() error { return errAmbiguousTypeMapping }

var errAmbiguousTypeMapping = cerrors.Register("ambiguous-type-mapping", cerrors.Meta{
	ShortCode: "E0093",
	Message:   "Manifest maps multiple version-distinct keys to the same base type; the type-to-recipe lookup is ambiguous",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrDuplicateEntityIdentity fires when two catalog entities of one kind share
// the same identity WITHIN A SINGLE catalog directory — an authoring collision,
// surfaced loudly (strict load) or recorded as a skip (tolerant load). It is the
// homogenized rule across every indexed kind (Component, Feature, Recipe,
// FeatureRecipe, Plugin, SchemaLibrary, MappingLibrary, Aggregate): within one
// directory a duplicate identity halts; ACROSS directories the same identity is
// a first-win shadow (the earlier-listed directory wins), never an error.
func ErrDuplicateEntityIdentity() error { return errDuplicateEntityIdentity }

var errDuplicateEntityIdentity = cerrors.Register("duplicate-entity-identity", cerrors.Meta{
	ShortCode: "E0094",
	Message:   "duplicate entity identity within a single catalog directory",
	RulePlace: cerrors.RuleNoStageReaches("A catalog directory load evaluates the rule, as it indexes each entity file and claims that " +
		"entity's identity for the directory it was found in. The load builds the catalog a later build " +
		"resolves references against, so no declared stage of a compilation holds the directory index the " +
		"claim is checked against."),
})

// ErrDuplicateRecipeIdentity is the recipe-specific facet of
// ErrDuplicateEntityIdentity, preserved as an alias so callers matching the
// recipe collision via errors.Is keep working. The Recipe / FeatureRecipe load
// cases wrap it with the provider+name+version context; the wrapped error still
// satisfies errors.Is against both this name and ErrDuplicateEntityIdentity.
func ErrDuplicateRecipeIdentity() error { return ErrDuplicateEntityIdentity() }

// ErrProviderVersionUnsatisfiable fires when a Recipe binds a version-pinned
// provider — "helm:1.0.0" or "helm:^1.0.0" — but no provider-rendering Plugin
// serving that provider satisfies the pin: either the provider has no serving
// Plugin at all, or the serving Plugin's available versions do not satisfy the
// exact/caret constraint. The version pin is an explicit request the compiler
// cannot honor, so the build halts loudly rather than silently ignoring the pin
// and rendering through whatever plugin version happens to serve the provider.
// The wrapped message names the provider and the pinned version so each provider
// yields a specific diagnostic. It is distinct
// from ErrUnsatisfiableEntityVersion, which covers a catalog ENTITY reference's
// version constraint — a Component, Feature, Aggregate, Plugin, Recipe or
// FeatureRecipe named at a version the catalog does not hold — rather than the
// selection of a render engine for a capability.
func ErrProviderVersionUnsatisfiable() error { return errProviderVersionUnsatisfiable }

var errProviderVersionUnsatisfiable = cerrors.Register("provider-version-unsatisfiable", cerrors.Meta{
	ShortCode: "E0101",
	Message:   "no provider-rendering plugin version satisfies the recipe's provider version pin",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrAmbiguousProviderPlugin fires at resolution when a capability is served
// by more than one distinct provider-rendering Plugin and no plugins.providers
// entry disambiguates which one renders it. The compiler never picks arbitrarily
// among competing render engines — a federated catalog where two publishers each
// ship a plugin for the same target is legal (the catalog loads), but a build or
// pull that reaches the target must be told which engine to use. The wrapped
// message names every candidate plugin, sorted, and points the operator at the
// plugins.providers surface that selects one. It is a point-of-use halt: it fires
// at pull (resolution) and at build (render-engine selection), not at catalog
// load, so an ambiguity outside a Release's reach never blocks an unrelated build.
func ErrAmbiguousProviderPlugin() error { return errAmbiguousProviderPlugin }

var errAmbiguousProviderPlugin = cerrors.Register("ambiguous-provider-plugin", cerrors.Meta{
	ShortCode: "E0092",
	Message:   "capability is served by more than one provider plugin; add a plugins.providers entry to select one",
	RulePlace: cerrors.RuleNoStageReaches("A pull's recipe-plugin descent evaluates the rule, as each in-closure Recipe's declared " +
		"capability is matched against the provider-rendering plugins serving it and against the bindings " +
		"the workspace configuration composes — and the same descent records the binding it settles on for " +
		"a later build to read. What decides the rule is the ABSENCE of a binding, which is written in the " +
		"workspace configuration rather than in any document a compilation reads. The descent walks a " +
		"closure to decide what to pin rather than compiling anything, and the build-time re-resolution " +
		"runs while a stage's plugin self-reports are being produced rather than at a point any view is " +
		"complete, so no declared stage of a compilation holds the candidate set and the binding the rule " +
		"weighs against one another."),
})

// ErrProviderBindingUnsatisfied fires at resolution when a plugins.providers
// entry selects a plugin identity that no provider-rendering Plugin serving the
// target matches — a typo in the binding, or a binding pointing at a plugin that
// does not (or no longer) serves the target. The explicit selection is a request
// the catalog cannot honor, so it halts loudly rather than silently falling back
// to another engine.
//
// It is raised on two paths, and they are one condition because they report one
// fault. A Recipe asking for the capability reaches it through the resolution
// that selects the rendering plugin. A binding no Recipe asks for reaches it
// through [Catalog.ProviderBindingFault], which every build and Release-scoped
// validation asks of each binding in the workspace configuration, so a binding
// whose capability is misspelt halts even though nothing consults it.
func ErrProviderBindingUnsatisfied() error { return errProviderBindingUnsatisfied }

var errProviderBindingUnsatisfied = cerrors.Register("provider-binding-unsatisfied", cerrors.Meta{
	ShortCode: "E0100",
	Message:   "plugins.providers entry names a plugin no provider-rendering plugin serving the capability matches",
	RulePlace: cerrors.RuleNoStageReaches("A pull's recipe-plugin descent evaluates the rule, as the plugin identity the workspace " +
		"configuration binds to a capability is matched against the provider-rendering plugins the catalog " +
		"holds for it; a later build re-runs the same match against the identity that descent recorded; and " +
		"every build and Release-scoped validation runs the same match over each binding the workspace " +
		"configuration composes, once the graph is built and before it is analysed, whether or not a Recipe " +
		"asks for its capability. " +
		"The binding is the whole of what this condition reports — without one the same empty candidate set " +
		"is a bare miss the caller resolves — and a binding is written in the workspace configuration rather " +
		"than in any document a compilation reads. The descent walks a closure to decide what to pin, the " +
		"build-time re-run reads a recorded identity while a stage's plugin self-reports are being produced, " +
		"and the per-binding judgement reads the binding from that configuration rather than from any stage's " +
		"facts, so no declared stage of a compilation holds the binding on any arm."),
})

// ErrProviderBindingCapabilityUnserved marks an ADVISORY finding, not a halt: a
// plugins.providers entry that names a capability and no plugin identity, where
// no provider-rendering plugin the build reads serves that capability — the
// shape a misspelt capability takes. Such a binding is consulted only when a
// Recipe asks for its capability, and a binding with no identity selects nothing
// the resolution would not select without it, so nothing reads it and nothing
// would otherwise say so.
//
// It advises rather than halting because the build cannot be sure. Bindings are
// workspace-wide while a build is of one Release, and the plugin serving the
// capability may be one only another Release reaches — a plugin this build does
// not read — so a halt here would refuse a legal workspace on a guess. It exists
// so a front end can recognise the advisory class and render it as a warning.
func ErrProviderBindingCapabilityUnserved() error { return errProviderBindingCapabilityUnserved }

var errProviderBindingCapabilityUnserved = cerrors.Register("provider-binding-capability-unserved", cerrors.Meta{
	ShortCode: "E0313",
	Message:   "plugins.providers entry names a capability no provider-rendering plugin the build reads serves",
	RulePlace: cerrors.RuleNoStageReaches("Every build and Release-scoped validation evaluates the rule over each capability-only " +
		"binding the workspace configuration composes, once the graph is built and before it is analysed, " +
		"asking the capability resolution whether any provider-rendering plugin the build reads serves the " +
		"capability. A binding is written in the workspace configuration rather than in any document a " +
		"compilation reads, and the judgement reads it from that configuration rather than from any " +
		"stage's facts, so no declared stage of a compilation holds it."),
})

// ErrNoRecipeResolved fires when a component/feature instance has no explicit
// Manifest mapping for its type. Resolution is mapping-only — the catalog never
// chooses a recipe by spec.implements, so an unmapped instance halts regardless
// of how many recipes implement its type. The diagnostic names the implementing
// recipes as pin candidates: implements informs the choice, the author makes it,
// and the Manifest mapping records it. Also fires when a mapping value at an
// accepted reference shape names a recipe absent from the catalog (a dangling
// mapping) — including a recipe present under a different version than the one
// the value pins, and one whose bare name two capabilities both offer, since
// neither resolves to a recipe the author can be said to have chosen. A mapping
// value whose shape a reference cannot express is
// [resolver.ErrMalformedRecipeReference] instead, so the absent-from-the-catalog
// claim is only made about a reference that could have resolved.
func ErrNoRecipeResolved() error { return errNoRecipeResolved }

var errNoRecipeResolved = cerrors.Register("no-recipe-resolved", cerrors.Meta{
	ShortCode: "E0099",
	Message:   "no recipe resolved for component/feature instance: add an explicit Manifest mapping for its type",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrNameMappingMatchedNothing fires when a key in a Manifest's
// mappings.components.name or mappings.features.name tier names no component
// instance, or no attachment, in the build. The name tier is matched by exact
// string against the instance a lookup asks about — a component instance's name,
// or an attachment's own name (`<alias>.<slot>` for one written on a connection),
// never the name of the component an attachment sits on — so an entry keyed by
// anything else is never consulted: every instance it was meant for resolves
// through the type tier, and without this halt the build would say nothing about
// the entry it ignored. The diagnostic names the key, the Manifest, the names of
// that kind the build does declare, and the one the author probably meant where
// one is a single edit away or the key is the name of the component carrying it.
func ErrNameMappingMatchedNothing() error { return errNameMappingMatchedNothing }

var errNameMappingMatchedNothing = cerrors.Register("name-mapping-matched-nothing", cerrors.Meta{
	ShortCode: "E0357",
	Message:   "a Manifest name mapping's key names no component instance or attachment in this build, so the entry binds nothing",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// RecipeMappingURI composes the bare "name:version" URI form that Manifest
// mapping values and the closure walker use. A versionless entity keys on its
// bare name. It is the single home for this URI form, so a caller synthesizing
// a Manifest mapping value (for example a standalone-render harness) produces
// the exact string the resolver expects rather than reconstructing it.
func RecipeMappingURI(name, version string) string {
	if version == "" {
		return name
	}
	return name + ":" + version
}

// recipeURIKey composes the bare "name:version" URI form Manifest mapping
// values and the closure walker use. The catalog's identity key wraps this
// with the provider segment — see recipeIdentityKey.
func recipeURIKey(name, version string) string {
	return RecipeMappingURI(name, version)
}

// recipeIdentityKey is the catalog's identity key for a recipe or feature
// recipe: "provider/name:version". The provider segment is the entity's
// spec.capability with any version pin removed. Identity is provider-scoped
// because the catalog tree (recipe/{provider}/{name}/) and the registry
// identity (recipe/{provider}/{name}:{version}) both are: two providers may
// each ship a recipe of the same name and version, and the naming convention
// keeps the provider out of metadata.name. An entity with an empty
// provider keys on the flat "name:version" form, so a
// malformed-but-loading entity keeps a stable identity rather than
// gaining a leading slash.
//
// The pin is removed because it is not part of the capability's name: it
// selects which version of the plugin serving the capability renders the
// document, and nothing else. So a Manifest maps a pinned Recipe by the same
// "capability/name:version" it maps an unpinned one by, changing the pin never
// renames the Recipe, and a pinned and an unpinned declaration of one name and
// version are one identity, which the catalog holds once. The projection is
// [types.BareCapability], the one every other reading of which capability a
// binding names goes through.
func recipeIdentityKey(provider, name, version string) string {
	provider = types.BareCapability(provider)
	if provider == "" {
		return recipeURIKey(name, version)
	}
	return provider + "/" + recipeURIKey(name, version)
}

// RecipeIdentityURI composes the capability-qualified reference a Recipe or
// FeatureRecipe is identified by in the catalog — "capability/name:version",
// the capability's version pin removed — from the capability its spec binds and
// its metadata name and version. It is the exported face of the catalog's own
// identity key, so a caller naming an entity it holds the document of resolves
// it under exactly the identity the catalog indexed it by rather than composing
// the reference by hand.
//
// An empty version yields "capability/name", the version-independent identity
// a registry location is derived from.
func RecipeIdentityURI(capability, name, version string) string {
	return recipeIdentityKey(capability, name, version)
}

// entityIndexKey is the catalog index key for a by-name entity (Component,
// Feature, Aggregate): the bare metadata.name when version-less,
// "name:version" when versioned — the recipe identity key minus the provider
// segment. Keying by name:version lets multiple versions of one entity coexist
// in the index instead of silently overwriting by bare name (the semver-incorrect
// last-wins the bare-name index produced); resolution then selects among the
// coexisting versions via selectEntityVersion.
func entityIndexKey(name, version string) string {
	return recipeURIKey(name, version)
}

// versionTolerantTypeGet resolves a component / feature TYPE to its mapped recipe
// URI in typeMap, tolerant of a version mismatch between the authored type and
// the mapping key. It tries an exact key match first (so an exact-version key
// wins), then falls back to a BASE match: the authored type and a mapping key
// resolve together when their stripVersion bases are equal.
//
// The tolerance is base-equality, NOT semver-range satisfaction — the
// type-to-recipe mapping only needs to find the recipe. The concrete version the
// instance compiles at is the instance-version table's (ResolveInstanceVersions),
// which reads the key this lookup settles on as one constraint among those
// declared on the instance. A caret key (`core/x:^1.0.0`) base-matches like any
// other; a single base match resolves; a base shared by multiple version-distinct
// keys is ambiguous and returns ErrAmbiguousTypeMapping (a Loud Guardrail), never
// a silent first-wins.
//
// It returns the key that matched beside the recipe URI it maps to. The key is
// what the mapping-consistency rule judges a node's Recipe against: a key naming
// no version says nothing about which version of the type the Recipe serves, so
// the instance's own version is judged instead, and only the lookup knows which
// key it settled on.
func versionTolerantTypeGet(typeMap types.OrderedMap[string], lookupType string) (string, string, bool, error) {
	if uri, ok := typeMap.Get(lookupType); ok {
		return lookupType, uri, true, nil
	}
	base := stripVersion(lookupType)
	var matchURI string
	var matchedKeys []string
	for key, uri := range typeMap.All() {
		if stripVersion(key) == base {
			matchURI = uri
			matchedKeys = append(matchedKeys, key)
		}
	}
	switch len(matchedKeys) {
	case 0:
		return "", "", false, nil
	case 1:
		return matchedKeys[0], matchURI, true, nil
	default:
		return "", "", false, &ambiguousTypeMapping{lookupType: lookupType, keys: matchedKeys}
	}
}

// ambiguousTypeMapping is the refusal a version-tolerant match returns when an
// authored type matches no key exactly and matches more than one key's base.
//
// It is a type rather than a formatted error so the keys survive the refusal. A
// reader that has to say WHICH keys collide — the diagnostic an author acts on,
// and the rule that reports the same condition where the node set is complete —
// would otherwise have to recover them by reading the sentence back, which is a
// second parser for a message the first one wrote.
//
// The sentence is composed here and nowhere else, so the two readers report the
// same collision in the same words. It unwraps to the registered condition, so a
// caller matching by identity keeps matching.
type ambiguousTypeMapping struct {
	// lookupType is the authored type whose lookup refused.
	lookupType string

	// keys are the version-distinct mapping keys sharing that type's base, in
	// the order the mapping table declares them.
	keys []string
}

func (e *ambiguousTypeMapping) Error() string {
	return fmt.Sprintf(
		"%s: type %q matches version-distinct keys %v — pin the authored type's version or "+
			"remove the duplicate mapping keys", ErrAmbiguousTypeMapping(), e.lookupType, e.keys)
}

func (e *ambiguousTypeMapping) Unwrap() error { return ErrAmbiguousTypeMapping() }

// ambiguousMappingKeys returns the version-distinct keys a refused lookup could
// not choose between, and whether the failure was that refusal at all.
//
// It reads the refusal through its own type rather than through the registered
// condition, because the condition alone says which rule was broken and this
// needs the values the occurrence carried.
func ambiguousMappingKeys(err error) ([]string, bool) {
	var ambiguous *ambiguousTypeMapping
	if !errors.As(err, &ambiguous) {
		return nil, false
	}
	return ambiguous.keys, true
}

// nameThenType resolves a mapping tier with name-wins-over-type precedence:
// an exact instance-name hit short-circuits all further lookup; otherwise the
// version-tolerant type match applies. This is the single expression of the
// Manifest two-tier precedence per entity kind — the render lookup
// (componentTypeToRecipeWithManifest / featureTypeToFeatureRecipeWithManifest)
// and the pull's source-descent joins (ComponentRecipeURI / FeatureRecipeURI)
// both route through it, so those entry points stay thin forwarders and the
// precedence cannot drift between the two resolution paths. An empty
// instanceName (no per-instance name in scope at a call site) cleanly skips the
// name tier.
//
// The tiers are read through [types.MappingTier.Tiers], the one statement of
// which tiers a mapping carries and in which order, and the lock-time walk that
// pins every realization a mapping binds reads them through it too. That is what
// keeps every reference this lookup can return among the pinned ones: walk and
// lookup cannot come to read different tiers.
func nameThenType(tier *types.MappingTier, instanceName, lookupType string) (string, bool, error) {
	match, ok, err := nameThenTypeEntry(tier, instanceName, lookupType)
	return match.reference, ok, err
}

// mappingMatch is the entry a mapping lookup settled on: the reference it names,
// and, where a type-axis key reached the instance rather than its name, that key.
type mappingMatch struct {
	// reference is the mapping value, verbatim as the author wrote it.
	reference string

	// typeKey is the type-axis key that matched. Empty when the name axis
	// reached the instance, which keys an instance and says nothing about a type.
	typeKey string

	// byType reports whether the type axis reached the instance at all.
	byType bool
}

// nameThenTypeEntry is the lookup nameThenType states, answering with the entry
// it settled on rather than with the reference alone. It is the one body of that
// lookup: nameThenType projects the reference out of it, so a caller reading the
// key and a caller reading the reference read one resolution.
func nameThenTypeEntry(tier *types.MappingTier, instanceName, lookupType string) (mappingMatch, bool, error) {
	for _, t := range tier.Tiers() {
		if t.ByInstanceName {
			if instanceName == "" {
				continue
			}
			if uri, ok := t.Entries.Get(instanceName); ok {
				return mappingMatch{reference: uri}, true, nil
			}
			continue
		}
		key, uri, ok, err := versionTolerantTypeGet(t.Entries, lookupType)
		if err != nil {
			return mappingMatch{}, false, err
		}
		if ok {
			return mappingMatch{reference: uri, typeKey: key, byType: true}, true, nil
		}
	}
	return mappingMatch{}, false, nil
}

// RecipeLookupWithManifest returns a renderer.RecipeLookup whose Recipe
// and FeatureRecipe closures resolve through the Manifest's mappings when a
// Manifest is supplied: a mapping entry under manifest.Spec.Mappings.Components
// or .Features resolves its Recipe URI directly against the catalog by Recipe
// URI, never by spec.implements.
//
// A MAPPING WINS ON EXISTENCE, NOT ON RESOLVABILITY, and that is the whole
// precedence rule — the single home for it, which both consumption paths read.
// A mapping entry is the author's answer for that type, so a value naming a
// recipe the catalog does not hold is a miss here rather than an invitation to
// resolve by some other route. Answering any other way would make this lookup
// disagree with ComponentRecipeURI / FeatureRecipeURI, which the source-descent
// walk and the resolution-completeness pass both read — and a build whose
// rendered output named one recipe while its own reported fault named another
// is a divergence no author could act on.
//
// Resolution is mapping-only: a type the Manifest does not map is a miss here —
// the catalog never chooses a recipe by spec.implements. An unmapped instance
// halts loudly in the pre-render resolution-completeness pass, which names the
// implementing recipes as pin candidates. When manifest is nil, every type is a
// miss.
func (c *Catalog) RecipeLookupWithManifest(manifest *types.Manifest) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe:                c.componentTypeToRecipeWithManifest(manifest),
		FeatureRecipe:         c.featureTypeToFeatureRecipeWithManifest(manifest),
		RecipeDocument:        c.recipeDocument,
		FeatureRecipeDocument: c.featureRecipeDocument,
	}
}

// recipeDocument reports the document a resolved Recipe was parsed at, and
// whether one is recorded (see entityDocument).
//
// It takes the entity the caller already holds rather than an identity, so the
// key formula stays here beside the walk that recorded the path. Spelled at the
// call site it would be a second statement of that formula, free to disagree the
// moment the identity shape moves.
func (c *Catalog) recipeDocument(r *types.Recipe) (string, bool) {
	if r == nil {
		return "", false
	}
	return c.entityDocument("Recipe", r.Spec.Capability, r.Metadata.Name, r.Metadata.Version)
}

// featureRecipeDocument is the FeatureRecipe counterpart of recipeDocument, and
// answers for a FeatureRecipe exactly what that one answers for a Recipe.
//
// The two kinds share an identity formula and an index but not an entity type, so
// what is shared is the lookup below and what is written twice is the projection
// of an entity onto its identity — which is the half that is genuinely per-kind
// and would be a guess if either accessor derived it from the other.
func (c *Catalog) featureRecipeDocument(fr *types.FeatureRecipe) (string, bool) {
	if fr == nil {
		return "", false
	}
	return c.entityDocument("FeatureRecipe", fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)
}

// RecordedPins reports the content pins in force for every pin-bearing entity
// this catalog indexed, keyed by the document each was read from.
//
// It is the render's one source for those pins: the load is where an entity and
// the directory it was read from are both in hand, so it is the only pass that
// can say which lock sits beside which document. A reader keys into it with the
// same document entityDocument reports, so the pins a build compares against and
// the file a diagnostic names come from one reading of the catalog rather than
// two.
//
// An entity resolved from a registry has no entry: its cache slot is not a
// directory anybody authored a lock in, so the load records nothing for it, and a
// reader keying in with the layout document entityDocument reports for it finds
// nothing recorded.
func (c *Catalog) RecordedPins() recipepin.Lookup {
	if c == nil {
		return nil
	}
	return c.recordedPins
}

// entityDocument reports the document the entity of the given kind and identity
// was parsed at, and whether one is recorded.
//
// The key is composed through entityBytesKey because that is the declared
// spelling of a kind-namespaced identity, and both backings already record this
// fact under it.
//
// For an entity read from a catalog directory it is the file the entity was
// indexed from. The lazy backing answers from its one record of where each entity
// came from, which it hands to the shell it builds; the eager load, which folds no
// pinned slot and holds no such record, answers from the identity it claimed as it
// indexed each file. Each backing therefore answers from the reading that
// resolved the entity, never from a second record free to disagree with it.
//
// For a Recipe or FeatureRecipe resolved from a registry it is the entity's
// document in the layout of its cache slot, which is where the load anchored the
// entity's content paths; the render anchors what a source template produces at
// the same document, so a relative result resolves inside what was published
// rather than against the directory the build runs in. That document is not a
// file anybody authored, and nothing that reports where an entity was declared
// reads it: the record distinguishes it from an authored document, and every
// accessor that reports a declaration asks for the authored one.
func (c *Catalog) entityDocument(kind, provider, name, version string) (string, bool) {
	key := entityBytesKey(kind, recipeIdentityKey(provider, name, version))
	if origin, ok := c.origins[key]; ok {
		return origin.anchorDocument()
	}
	if path, ok := c.pathByIdentity[key]; ok && path != "" {
		return path, true
	}
	return "", false
}

func (c *Catalog) componentTypeToRecipeWithManifest(manifest *types.Manifest) func(string, string) (*types.Recipe, bool) {
	return func(instanceName, componentType string) (*types.Recipe, bool) {
		// Name-wins-over-type resolution via componentMappingURI. A
		// version-ambiguous mapping error is swallowed to a miss here; the
		// loud ErrAmbiguousTypeMapping is the pull/source-descent contract
		// (ComponentRecipeURI), where a wrong pick mis-pins a source.
		//
		// A mapping entry is answered whether or not its URI resolves — the
		// precedence rule is stated once, on RecipeLookupWithManifest. The
		// unresolvable case is a miss here, and which stage reports it depends
		// on the caller: one compiling the documents halts at the rule asked over
		// the flattened composition, which names the mapping value the author
		// wrote; one that resolved the Release's dependency closure first never
		// reaches that rule, because that resolution already stopped on the URI no
		// catalog entity matches.
		if uri, ok, err := componentMappingURI(manifest, instanceName, componentType); err == nil && ok {
			r, found := c.RecipeByURI(uri)
			if !found {
				return nil, false
			}
			return &r, true
		}
		// Resolution is mapping-only: an unmapped type is a miss here. The
		// catalog never chooses a recipe by spec.implements — an unmapped
		// instance halts loudly at the rule asked over the flattened composition,
		// which names the implementing recipes as pin candidates.
		return nil, false
	}
}

func (c *Catalog) featureTypeToFeatureRecipeWithManifest(manifest *types.Manifest) func(string, string) (*types.FeatureRecipe, bool) {
	return func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
		// A mapping entry is answered whether or not its URI resolves, exactly
		// as it is for a component: same precedence rule, stated once on
		// RecipeLookupWithManifest.
		if uri, ok, err := featureMappingURI(manifest, instanceName, featureType); err == nil && ok {
			fr, found := c.FeatureRecipeByURI(uri)
			if !found {
				return nil, false
			}
			return &fr, true
		}
		// Mapping-only resolution: an unmapped feature type is a miss here;
		// implements never resolves. Unmapped feature instances halt in the
		// pre-render resolution-completeness pass with pin candidates.
		return nil, false
	}
}

// The shape a Recipe / FeatureRecipe reference accepts has ONE home, and it is
// no longer in this package: it travels with the rule that enforces it, in
// pkg/resolver, which this package depends on. The ladder below reads the
// ceiling through [resolver.RecipeReferenceSegments] and quotes the shape
// statement from [resolver.AcceptedRecipeReferenceShapes]; RecipeByURI's godoc
// points there instead of restating it. Keeping it single-sourced is
// load-bearing rather than tidy: a ladder that grows a reference position beyond
// the capability one widens the accepted shape, and a count or a shape list
// restated here would go quietly false, because no gate reads prose. Whoever
// adds the position edits those two declarations and every reader follows.

// RecipeByURI resolves a Manifest mapping value (a Recipe URI) to its catalog
// Recipe. The catalog is keyed by capability-scoped identity
// ("capability/name:version"), and the URI may name the recipe at any of
// three precision levels, tried in order:
//
//  1. Capability-qualified exact ("terraform/mariadb-rds:1.0.0") — a direct
//     map get on the identity key.
//  2. Capability-qualified ("terraform/mariadb-rds:^1.0.0",
//     "terraform/mariadb-rds") — the segment before the first "/" is matched
//     against each Recipe's Spec.Capability with any version pin removed, and
//     the version is selected among that capability's recipes of that name.
//  3. Bare ("mariadb-rds:1.0.0" or "mariadb-rds") — a scan across every
//     capability, with the version selected within each one.
//
// A Recipe URI therefore carries at most the segments
// [resolver.RecipeReferenceSegments] measures against, whose declaration is the
// single home for the accepted shape: strings.Cut splits on the FIRST "/" only,
// so a segment past the ceiling stays inside the name candidate and matches no
// Metadata.Name.
//
// Version selection is the shared three-form rule every by-name catalog lookup
// applies: an exact pin resolves that version, a caret pin the highest in range,
// an unversioned reference the highest available. A pin the catalog cannot satisfy
// is a miss here — it is never quietly widened to a version the author did not
// ask for. Which stage turns that miss into the loud halt depends on the caller:
// one that loaded the catalog eagerly reports it at the resolution-completeness
// pass, naming the mapping value, exactly as it does for a component type pinned
// at an absent version; one that resolved the Release's dependency closure first
// stopped in that resolution before the pass ran, naming the reference no catalog
// version satisfies and the mapping entry that referenced it.
//
// Ambiguity across CAPABILITIES is a miss, never a silent pick: a bare reference
// two capabilities can both answer carries nothing that selects either, so it
// resolves to neither and surfaces as a loud resolution halt upstream. Qualify
// the reference with its capability to select one. The "/" split cannot misread a
// bare reference: recipe names contain no "/" per the catalog naming convention,
// and a failed capability interpretation falls through to bare resolution anyway.
func (c *Catalog) RecipeByURI(uri string) (types.Recipe, bool) {
	// The unsatisfiable-version sentinel is dropped rather than surfaced: this
	// accessor's (entity, found) shape is the one every consumer of the recipe
	// lookup shares, and an unsatisfied pin is a miss to all of them. The loud
	// halt belongs to whichever stage the caller reaches first — the
	// resolution-completeness pass, naming the mapping value, where the catalog
	// was loaded eagerly; the Release's dependency-closure resolution, naming
	// the reference no version satisfies, where the caller resolved that closure
	// first.
	key, ok, _ := selectCapabilityScopedVersion(c.Recipes, uri, recipeVersionMeta)
	if !ok {
		return types.Recipe{}, false
	}
	return c.Recipes[key], true
}

// FeatureRecipeByURI is the FeatureRecipe analogue of RecipeByURI; the
// resolution ladder, the version rule and the capability-ambiguity refusal are
// identical, over the FeatureRecipe index.
func (c *Catalog) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	key, ok, _ := selectCapabilityScopedVersion(c.FeatureRecipes, uri, featureRecipeVersionMeta)
	if !ok {
		return types.FeatureRecipe{}, false
	}
	return c.FeatureRecipes[key], true
}

// recipeVersionMeta and featureRecipeVersionMeta report the capability, name and
// version a capability-scoped version select reads off a Recipe or a
// FeatureRecipe. Each is written once, so the accessor that resolves a reference
// and the lookup that explains why one did not resolve read an entity's identity
// the same way.
func recipeVersionMeta(r types.Recipe) (string, string, string) {
	return r.Spec.Capability, r.Metadata.Name, r.Metadata.Version
}

func featureRecipeVersionMeta(fr types.FeatureRecipe) (string, string, string) {
	return fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version
}

// PluginByURI returns the Plugin entity whose identity matches uri, plus
// a found flag. A Plugin is keyed by its coordinate — the `<pluginType>/<name>`
// pair — not by a provider-qualified name, so resolution goes through the
// shared three-form version select (exact / caret / unversioned ->
// highest-satisfying) rather than the provider-scan ladder RecipeByURI uses. The
// returned entity's .wasm artifact reference is that same coordinate; resolving
// that reference to a pinned artifact is a separate concern (the closure's
// artifact resolution), not this accessor's job — this serves the entity struct
// only.
//
// Resolution is local-first with an embedded fallback. An explicit entity-URI
// reference resolves against the loaded catalog first, so an exact-version
// reference to a present local plugin (a pipeline step's `mutator/x:1.0.0`, a
// linter's) is honored verbatim — never shadowed by the binary's embedded core,
// whose build version differs. Only when the local catalog cannot resolve the
// reference does a production workspace view fall back to the embedded core
// entities, so a no-local-catalog build can still resolve a core plugin entity by
// URI. (Embedded-wins-over-local is a CAPABILITY-resolution behavior — see
// pluginForCapability — not an entity-URI one: a bare provider name has no
// author-declared version to honor, an explicit URI does.)
//
// Served by both catalog backings: the lazy backing exposes its enumerated
// Plugin set through the recipeCatalog() *Catalog shell, so its
// PluginByURI delegates here against the same matcher (no per-backing
// reimplementation).
func (c *Catalog) PluginByURI(uri string) (types.Plugin, bool) {
	if key, ok, _ := c.resolvePluginKey(uri); ok {
		return c.Plugins[key], true
	}
	return c.embeddedPluginByURI(uri)
}

// embeddedPluginByURI is the embedded half of PluginByURI: the Plugin entity among
// those that ship inside the compiler binary that uri selects, at any plugin type,
// through the shared three-form version select, on a view with embedded resolution
// on. It is the one reading of the binary's entities by reference, so the closure
// lookup's resolution of a pinned workspace-socket attachment
// (catalogLookup.ResolvePinnedSocketEntity) answers from the binary exactly where
// the build's plugin lookup does.
func (c *Catalog) embeddedPluginByURI(uri string) (types.Plugin, bool) {
	if !c.resolveEmbeddedProviders {
		return types.Plugin{}, false
	}
	embeds, _ := embeddedPluginEntities()
	if len(embeds) == 0 {
		return types.Plugin{}, false
	}
	embedMap := make(map[string]types.Plugin, len(embeds))
	for _, p := range embeds {
		embedMap[entityIndexKey(p.Coordinate(), p.Metadata.Version)] = p
	}
	key, ok, _ := selectEntityVersion(embedMap, uri, func(v types.Plugin) (string, string) {
		return v.Coordinate(), v.Metadata.Version
	})
	if !ok {
		return types.Plugin{}, false
	}
	return embedMap[key], true
}

// ProviderPluginForTarget resolves a render-engine capability to the
// provider-rendering Plugin entity that serves it, plus a found flag and an
// error, honoring an explicit plugins.providers selection. The ref is the bare
// capability name ("helm") or a version-pinned form ("helm:1.0.0",
// "helm:^1.0.0") a Recipe binds through spec.capability; the bare name (before
// any pin) is matched against each plugin's served capabilities. A pluginType:
// provider Plugin serves the capabilities listed in its spec.provider block; a
// pipeline-step plugin whose own block happens to list the name is excluded —
// each plugin type keeps its capabilities in its own block precisely because the
// list means a different thing per type.
//
// The bindings map is the workspace-config (at pull) or lockfile-derived (at
// build) provider selections, keyed by bare capability name; each value is a
// plugin's bare identity (a Plugin's metadata.name) with an optional trailing
// ":version". When an entry is present for the capability, the candidate set is
// filtered to plugins whose identity matches that value — so the binding SELECTS
// the plugin repository among competing publishers. A nil
// or empty bindings map disables that filter (every provider plugin serving the
// capability is a candidate). Version coexistence is orthogonal to the binding: the
// binding selects the repository, and the version is chosen by the recipe's pin,
// else the binding's optional version, else the highest — via the same three-form
// version select the other by-name lookups use.
//
// Return contract:
//   - (plugin, true, nil) on a satisfying match;
//   - (_, false, nil) on a bare miss (no serving plugin and no binding, no
//     version pin) — left for the caller to handle (build halts loudly at render);
//   - (_, false, ErrAmbiguousProviderPlugin) when more than one distinct provider
//     plugin serves the target and no binding disambiguates — a loud point-of-use
//     halt naming the candidates;
//   - (_, false, ErrProviderBindingUnsatisfied) when a binding selects a plugin no
//     serving candidate matches — a loud halt on an unfulfillable explicit choice;
//   - (_, false, ErrProviderVersionUnsatisfiable) when a version pin is present but
//     no serving plugin version satisfies it — a loud halt on the pin.
//
// Determinism (byte-identical output): matches collect into a sub-index that the
// shared version select sorts before choosing, and the candidate names sort before
// they enter a diagnostic, so no outcome depends on map-iteration order.
//
// Served by both catalog backings: the lazy backing exposes its enumerated Plugin
// set through the recipeCatalog() *Catalog shell, so its ProviderPluginForTarget
// delegates here against the same matcher (no per-backing reimplementation).
func (c *Catalog) ProviderPluginForTarget(ref string, bindings map[string]string) (types.Plugin, bool, error) {
	pin := types.ParseVersionPin(ref)
	verSeg, hasVer := pin.Pin()
	return c.pluginForCapability("provider", pin.Base(), verSeg, hasVer, bindings, providerCapabilityDiag)
}

// capabilityDiag carries a capability-resolving socket's own diagnostic
// vocabulary, so the shared select emits each socket's phrasing rather than a
// single hardcoded one. noun is woven into the binding-unsatisfied and
// version-no-serving-plugin halts; bindingUnsatisfied and versionUnsatisfiable
// are the sentinels those two halts (and the unsatisfiable-version-select halt)
// wrap; ambiguous builds the more-than-one-serving-plugin halt, whose phrasing
// is socket-specific — the provider names its plugins.providers binding surface,
// a format-routed socket names the format — so it is a closure rather than a
// noun-substituted template.
type capabilityDiag struct {
	noun                 string
	bindingUnsatisfied   error
	versionUnsatisfiable error
	ambiguous            func(capability string, candidates []string) error
}

// providerCapabilityDiag is the provider socket's diagnostic set. Its phrasing
// reproduces the provider wording verbatim, so provider resolution's halts stay
// byte-identical now that the shared select is generalized across sockets.
var providerCapabilityDiag = capabilityDiag{
	noun:                 "provider-rendering plugin",
	bindingUnsatisfied:   ErrProviderBindingUnsatisfied(),
	versionUnsatisfiable: ErrProviderVersionUnsatisfiable(),
	ambiguous: func(capability string, candidates []string) error {
		return fmt.Errorf("%w: capability %q is served by more than one provider plugin (%s); add a plugins.providers entry for %q selecting one", ErrAmbiguousProviderPlugin(), capability, strings.Join(candidates, ", "), capability)
	},
}

// pluginForCapability is the single generalized capability-resolution core the
// per-socket resolvers share. Given the plugin type a socket expects and a
// capability reference (the bare capability name, optionally version-pinned),
// it filters the catalog to plugins of that type serving the capability,
// disambiguates the survivors by an optional binding (bare plugin identity),
// halts loudly on an unresolved ambiguity or an unsatisfiable version pin, and
// otherwise selects the version via the shared three-form version select.
//
// It is the one home for the filter → disambiguate → ambiguity-halt →
// version-pin sequence: ProviderPluginForTarget is a thin pluginType="provider"
// specialization delegating here, so the provider socket's behavior is defined
// by this core rather than a parallel copy. A plugin type whose plugins declare
// no served capabilities matches nothing here — those sockets attach by URI
// (direct attach) instead, so routing them through capability resolution is a
// clean structural miss, not an error.
//
// The candidate set is the render-time plugin set (providerResolutionPlugins):
// the loaded catalog plugins overlaid with the binary's embedded core-provider
// entities, embedded-wins on a shared coordinate. An explicit binding still
// selects among the survivors, so a bound custom provider overrides the embedded
// default while a local core entity of the same coordinate is shadowed by it.
// For an importer the set also holds, on a view that serves them, the importers
// the binary carries without embedding (resolutionPluginsFor), so they compete
// with the catalog's own importers under exactly these rules; the capability and
// the version pinned are handed to that set too, since an importer entity no
// import could run is a candidate only where the pin names exactly its version or
// no importer of its identity that can run serves the capability.
//
// The capability is the already-separated capability string (the provider passes
// the pre-split bare name; a format-routed socket passes its full "prefix:value"
// capability, which may itself carry a colon), and refPin/refHasPin is the
// caller's optional version constraint on the resolved plugin. The diagnostics
// are threaded via diag, so each socket surfaces its own sentinels and phrasing
// rather than a hardcoded provider one — the provider socket passes
// providerCapabilityDiag to keep its halts byte-identical.
func (c *Catalog) pluginForCapability(pluginType, capability, refPin string, refHasPin bool, bindings map[string]string, diag capabilityDiag) (types.Plugin, bool, error) {
	var bindBase, bindVer string
	var bindHasVer, bound bool
	if bv, ok := bindings[capability]; ok && bv != "" {
		bound = true
		bindBase, bindVer, bindHasVer = ociref.SplitVersionTag(bv)
	}

	// The version pin priority: the caller's pin, else the binding's optional
	// version. Threaded into the candidate set, the diagnostic and the version
	// select alike.
	pin := ""
	if refHasPin {
		pin = refPin
	} else if bound && bindHasVer {
		pin = bindVer
	}

	matches := map[string]types.Plugin{}
	distinctNames := map[string]struct{}{}
	for key, p := range c.resolutionPluginsFor(pluginType, capability, pin) {
		if p.Metadata.PluginType != pluginType {
			continue
		}
		if !slices.Contains(p.DeclaredCapabilities(), capability) {
			continue
		}
		// A binding selects the plugin repository by its coordinate identity
		// (<pluginType>/<name>) — the same coordinate the lockfile pins — filtering
		// out every competing publisher's plugin for this target.
		if bound && p.Coordinate() != bindBase {
			continue
		}
		matches[key] = p
		distinctNames[p.Metadata.Name] = struct{}{}
	}

	switch len(distinctNames) {
	case 0:
		if bound {
			return types.Plugin{}, false, fmt.Errorf("%w: capability %q is bound to plugin %q, but no %s serving %q matches that identity%s",
				diag.bindingUnsatisfied, capability, bindBase, diag.noun, capability,
				c.boundPluginServes(pluginType, bindBase, capability, diag.noun))
		}
		if refHasPin {
			if pe, malformed := types.ParseVersionPin(capability + ":" + refPin).Malformed(); malformed {
				return types.Plugin{}, false, pe
			}
			return types.Plugin{}, false, fmt.Errorf("%w: capability %q pins %q but no %s serves it", diag.versionUnsatisfiable, capability, refPin, diag.noun)
		}
		// No serving plugin, no binding, no pin — a bare miss the caller resolves.
		return types.Plugin{}, false, nil
	case 1:
		// Exactly one repository — fall through to version select.
	default:
		names := make([]string, 0, len(distinctNames))
		for n := range distinctNames {
			names = append(names, n)
		}
		sort.Strings(names)
		return types.Plugin{}, false, diag.ambiguous(capability, names)
	}

	var base string
	for n := range distinctNames {
		base = n
	}
	// Thread the version constraint into the shared three-form select by rebuilding
	// the caller URI against the PLUGIN's identity name (which differs from the
	// target name — e.g. target "helm" served by plugin "helm-renderer").
	callerURI := base
	if pin != "" {
		callerURI = base + ":" + pin
	}
	key, ok, err := selectEntityVersion(matches, callerURI, func(v types.Plugin) (string, string) {
		return v.Metadata.Name, v.Metadata.Version
	})
	if err != nil {
		// A malformed pin names no version, so it is reported as the malformation
		// against the capability it was written on rather than as a pin no serving
		// version satisfies. Any other refusal is an unsatisfiable exact/caret
		// constraint, translated to the socket's version halt naming the pin.
		pinnedAt := capability + ":" + pin
		if !refHasPin {
			pinnedAt = bindings[capability]
		}
		if pe, malformed := types.ParseVersionPin(pinnedAt).Malformed(); malformed {
			return types.Plugin{}, false, pe
		}
		return types.Plugin{}, false, fmt.Errorf("%w: capability %q pins %q but no serving plugin version satisfies it", diag.versionUnsatisfiable, capability, pin)
	}
	if !ok {
		return types.Plugin{}, false, nil
	}
	return matches[key], true, nil
}

// capabilitiesServed returns the capabilities the plugins of pluginType in the
// resolution set serve, sorted and without repeats, and whether any plugin of
// that coordinate is in the set at all. An empty coordinate reads every plugin
// of the type, which is the vocabulary a near-miss is paired against; a named
// one reads that plugin alone, across every version the set holds, which is what
// a binding naming it selects.
//
// It reads the same set and the same accessor the resolution core filters on —
// resolutionPluginsFor and each plugin's DeclaredCapabilities — so a name it
// reports as served is one the core would match, and no second statement of
// what a plugin serves exists beside the one on the entity. visible counts a
// plugin of any type at the coordinate, because a binding naming a plugin of
// another type still names something the catalog holds.
func (c *Catalog) capabilitiesServed(pluginType, coordinate string) (served []string, visible bool) {
	seen := map[string]struct{}{}
	for _, p := range c.resolutionPluginsFor(pluginType, "", "") {
		if coordinate != "" && p.Coordinate() != coordinate {
			continue
		}
		visible = true
		if p.Metadata.PluginType != pluginType {
			continue
		}
		for _, name := range p.DeclaredCapabilities() {
			seen[name] = struct{}{}
		}
	}
	for name := range seen {
		served = append(served, name)
	}
	sort.Strings(served)
	return served, visible
}

// boundPluginServes is the clause the binding-unsatisfied halt appends to say
// what the plugin a binding names does serve, and which of those names the
// bound capability sits one edit from. It is empty when no plugin of that
// coordinate is in the resolution set, because then there is nothing true to
// add: the message already says no serving plugin matches the identity.
func (c *Catalog) boundPluginServes(pluginType, coordinate, capability, noun string) string {
	served, visible := c.capabilitiesServed(pluginType, coordinate)
	if !visible {
		return ""
	}
	if len(served) == 0 {
		return fmt.Sprintf("; %q serves no capability as a %s", coordinate, noun)
	}
	clause := fmt.Sprintf("; %q serves %s", coordinate, quotedList(served))
	if near := resolver.NearMissCandidates(capability, served); len(near) > 0 {
		clause += fmt.Sprintf(", and %q is one edit from %s", capability, quotedList(near))
	}
	return clause
}

// quotedList renders names as a comma-separated list of quoted strings, in the
// order given.
func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}

// ProviderBindingFault judges one plugins.providers binding by its shape,
// whether or not a Recipe in the build asks for its capability. capability is
// the binding's capability and uri the plugin identity it names, empty for a
// binding that names none.
//
// A binding is otherwise read only when a Recipe asks for its capability, so a
// misspelt capability would be read by nothing and reported by nothing. What can
// be judged of it depends on what the build holds:
//
//   - A binding naming a plugin: a pull resolves it whether or not a Recipe
//     uses it and refuses one it cannot resolve, so the build holds the plugin
//     and knows what it serves. When the plugin does not serve the binding's
//     capability the binding is certainly wrong, and the answer is the
//     binding-unsatisfied halt the resolution raises, naming what the plugin
//     does serve. When no plugin of that identity is in the set — a binding
//     written after the last pull, which a build has not adopted either — the
//     binding is not judged here, and the pull that adopts it resolves it.
//   - A binding naming no plugin: the plugin serving its capability may be one
//     only another Release reaches, so the build cannot be sure nothing serves
//     it, and such a binding selects nothing the resolution would not select
//     without it. When no provider-rendering plugin in the set serves the
//     capability, the answer is the unserved-capability advisory, naming a
//     served capability one edit away when there is one.
//
// A binding whose capability a plugin in the set serves is legal and yields
// nil, including one for a capability no Recipe in this Release asks for:
// bindings are workspace-wide and a build is of one Release. The version a
// binding pins is not judged here, because the binding selects a plugin
// repository and the version it pins is orthogonal to what that plugin serves.
//
// Both judgements go through ProviderPluginForTarget, the resolution a Recipe's
// capability goes through, so what counts as served here is what counts as
// served when a Recipe asks.
func (c *Catalog) ProviderBindingFault(capability, uri string) error {
	if capability == "" {
		return nil
	}
	if uri == "" {
		// Served by one plugin, or by more than one — the ambiguity is the
		// resolution's to report where a Recipe asks — is served either way.
		if _, served, err := c.ProviderPluginForTarget(capability, nil); served || err != nil {
			return nil
		}
		served, _ := c.capabilitiesServed("provider", "")
		near := ""
		if candidates := resolver.NearMissCandidates(capability, served); len(candidates) > 0 {
			near = fmt.Sprintf(", which is one edit from %s", quotedList(candidates))
		}
		return fmt.Errorf("%w: capability %q%s; the entry names no plugin, so it selects nothing a Recipe would not resolve without it, and none of the plugins this build reads — in the catalog directories, inside the compiler, or among those the lockfile records — serves the capability: correct its spelling, or keep it where the plugin serving it is one only another Release reads",
			ErrProviderBindingCapabilityUnserved(), capability, near)
	}
	coordinate, _, _ := ociref.SplitVersionTag(uri)
	if _, visible := c.capabilitiesServed("provider", coordinate); !visible {
		return nil
	}
	_, _, err := c.ProviderPluginForTarget(capability, map[string]string{capability: coordinate})
	if errors.Is(err, ErrProviderBindingUnsatisfied()) {
		return err
	}
	return nil
}

// ProviderPluginCandidates returns every provider-rendering Plugin serving the
// bare capability, sorted by metadata name then version, for a diagnostic or
// an interactive disambiguation prompt. It applies no binding filter and no
// version select — it is the raw candidate set the ambiguity halt reports over.
// The sort makes the returned order byte-identical across runs.
func (c *Catalog) ProviderPluginCandidates(target string) []types.Plugin {
	var out []types.Plugin
	for _, p := range c.providerResolutionPlugins() {
		if p.Metadata.PluginType != "provider" {
			continue
		}
		if !slices.Contains(p.DeclaredCapabilities(), target) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Metadata.Name != out[j].Metadata.Name {
			return out[i].Metadata.Name < out[j].Metadata.Name
		}
		return out[i].Metadata.Version < out[j].Metadata.Version
	})
	return out
}

// ProviderPluginByName resolves a capability to its provider-rendering Plugin
// entity with no explicit binding — the zero-config path where a target served by
// exactly one provider plugin resolves to it, and an ambiguously-served target
// halts with ErrAmbiguousProviderPlugin. It is ProviderPluginForTarget with a nil
// bindings map; the binding-aware callers (pull, build render) pass their bindings
// directly. Retained as the by-name accessor for the catalog-wide capability
// checks and the eager CatalogLookup, which resolve without a binding surface.
func (c *Catalog) ProviderPluginByName(ref string) (types.Plugin, bool, error) {
	return c.ProviderPluginForTarget(ref, nil)
}

// ComponentAncestryResolver reports the extends ancestry of a component type:
// the Manifest-key form of every base type it descends from, nearest parent
// first. It is the seam through which a Component index reaches the
// mapping-consistency gate, which holds recipes but not necessarily Components.
//
// A type the resolver cannot find has no ancestry and no error — "the mapped
// type is absent from the catalog" belongs to the resolution path's diagnostics,
// not to a consistency judgement. A malformed chain returns the chain's own
// fault so a caller reports the chain rather than a mapping mismatch.
type ComponentAncestryResolver func(componentType string) ([]string, error)

// componentAncestryOver is the single home for the ancestry both catalog
// backings expose: the eagerly-loaded Component index and the lazy
// Tier-1/2/3 cascade differ only in the lookup they hand it. Each ancestor is
// rendered as the mapping key an author would write for that base type so the
// satisfaction matcher receives ancestry in exactly the form it receives the
// mapped key itself.
//
// sel is the version select over the index lookup reads. A chain link naming a
// parent that index holds at other versions only is reported as the version no
// entry satisfies rather than as a parent nothing provides — see
// [recordExtendsRefusal].
func componentAncestryOver(componentType string, lookup validator.ComponentLookup, sel func(string) (string, bool, error)) ([]string, error) {
	comp, ok := lookup(componentType)
	if !ok {
		return nil, nil
	}
	recording, refusal := recordExtendsRefusal(lookup, sel)
	chain, vErrs := validator.ComponentAncestry(comp, recording)
	if len(vErrs) > 0 {
		return nil, refusal.refine(envelope.KindComponent,
			entityIndexKey(comp.Metadata.Name, comp.Metadata.Version), vErrs)[0]
	}
	keys := make([]string, 0, len(chain))
	for _, anc := range chain {
		// versionedSlotURI is the existing single home for the
		// bare-name-plus-version join, including its double-append guard.
		keys = append(keys, versionedSlotURI(anc.Metadata.Name, anc.Metadata.Version))
	}
	return keys, nil
}

// ComponentAncestry resolves componentType's extends ancestry over this
// catalog's OWN Component index. It is the ComponentAncestryResolver an
// eagerly-loaded catalog passes to its own consistency gate.
//
// A catalog shell assembled over a different backing — one whose Components
// index is empty by construction — must pass that backing's resolver instead.
// Passing this method from such a shell resolves no ancestry at all and the gate
// then refuses every mapping that relies on it, which is why the resolver is an
// argument of the gate rather than a field read off the receiver.
func (c *Catalog) ComponentAncestry(componentType string) ([]string, error) {
	return componentAncestryOver(componentType, c.componentOfType, c.resolveComponentKey)
}

// recipesImplementing returns the sorted, provider-qualified identity keys of
// every catalog recipe whose spec.implements satisfies componentType, the type
// resolved first as [implementsRequest] resolves it through component. It is a
// SUGGESTION provider only — resolution is mapping-only and never consults it.
// Its reader is the no-mapping diagnostic (pin candidates); the import's draft
// Manifest reads the same enumeration through [Catalog.RecipesImplementing].
//
// Deliberately ancestry-agnostic, unlike the mapping-consistency rule: extends
// ancestry is handled in that rule's outer layer rather than inside the matcher,
// so it reaches only the caller that resolves it. Widening the suggestion set to
// recipes implementing a base of the requested type would change which candidates
// a diagnostic lists and which Recipe a drafted Manifest names — user-visible
// output whose shape is a separate choice from whether an operator-written
// mapping is judged consistent.
//
// The matcher is read from the package that declares the mapping-consistency rule
// rather than carried here, so this suggestion surface and that rule cannot come
// to disagree about which recipes satisfy a type.
func recipesImplementing(recipes map[string]types.Recipe, componentType string, component componentResolver) []string {
	matched := implementingRecipes(recipes, componentType, component)
	ids := make([]string, 0, len(matched))
	for _, r := range matched {
		ids = append(ids, recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version))
	}
	return ids
}

// componentResolver resolves a component type reference to the Component a
// catalog view resolves it to, and reports whether it holds one. Each view
// passes its own: an eagerly-loaded catalog its Component index
// ([Catalog.componentOfType]), the lazy lookup its tiered resolution, whose
// recipe shell carries no Component index of its own.
type componentResolver func(typeURI string) (types.Component, bool)

// implementsRequest is the component type the implements enumeration is asked
// with for componentType: the type as written where it pins a version, and,
// where it pins none, the type at the version a build resolves it to — the
// Component component resolves the bare type to, which is the highest version
// the catalog holds, chosen by the catalog's own version selection.
//
// It is the reason an unversioned type finds the Recipes declaring the version
// they implement. The matcher answers a request naming no version only with a
// Recipe whose implements is that same unversioned string, while a Component
// reference naming no version builds as the highest version the catalog holds;
// asking with the version the build resolves to makes the suggestions name the
// Recipes that implement what the build would compile, through the one matcher
// and with no matching rule of the enumeration's own.
//
// A type component resolves to nothing — one the catalog holds no Component of,
// such as a Component an import drafted new — or a nil component is asked as
// written, as is a Component carrying no version.
func implementsRequest(componentType string, component componentResolver) string {
	if component == nil {
		return componentType
	}
	return implementsRequestAt(componentType, func(typeURI string) (string, string, bool) {
		comp, ok := component(typeURI)
		return comp.Metadata.Name, comp.Metadata.Version, ok
	})
}

// featureResolver resolves a feature type reference to the Feature a catalog
// view resolves it to, and reports whether it holds one — the Feature
// counterpart of componentResolver, passed for the same reason: a recipe shell
// over another backing holds no Feature index of its own.
type featureResolver func(typeURI string) (types.Feature, bool)

// featureImplementsRequest is implementsRequest for a feature type: the type as
// written where it pins a version, and otherwise the type at the version the
// view's Feature resolution selects for it, which is the version a build
// compiles the attachment at. A type feature resolves to nothing, or a nil
// feature, is asked as written.
func featureImplementsRequest(featureType string, feature featureResolver) string {
	if feature == nil {
		return featureType
	}
	return implementsRequestAt(featureType, func(typeURI string) (string, string, bool) {
		feat, ok := feature(typeURI)
		return feat.Metadata.Name, feat.Metadata.Version, ok
	})
}

// implementsRequestAt is the one rule both implements requests follow, for any
// entity kind: a reference pinning a version is asked as written, and one
// pinning none is asked at the name and version resolve selects for it, or as
// written where resolve holds nothing. Component and Feature requests each adapt
// their own resolution to it rather than carrying the rule twice, so a bare
// Component type and a bare Feature type are matched against implements
// declarations the same way.
func implementsRequestAt(typeRef string, resolve func(typeURI string) (name, version string, ok bool)) string {
	if _, pinned := types.ParseVersionPin(typeRef).Pin(); pinned {
		return typeRef
	}
	name, version, ok := resolve(typeRef)
	if !ok {
		return typeRef
	}
	return versionedSlotURI(name, version)
}

// implementingRecipes is the one enumeration of the recipes whose spec.implements
// satisfies componentType, ordered by their identity keys. recipesImplementing
// projects it onto those keys and [Catalog.RecipesImplementing] hands it on whole,
// so the pin-candidate list and the import's draft Manifest read one set in one
// order.
//
// The type is resolved through implementsRequest and through nothing else, so
// every reader asks with the same version for the same bare type: a reader
// resolving it on its own would be a second resolution free to disagree, and the
// disagreement would be a drafted Manifest naming a Recipe the halt does not
// offer, or the reverse.
func implementingRecipes(recipes map[string]types.Recipe, componentType string, component componentResolver) []types.Recipe {
	requested := implementsRequest(componentType, component)
	var keys []string
	for key, r := range recipes {
		if resolver.ImplementsSatisfies(r.Spec.Implements, requested) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := recipes[keys[i]], recipes[keys[j]]
		return recipeIdentityKey(a.Spec.Capability, a.Metadata.Name, a.Metadata.Version) <
			recipeIdentityKey(b.Spec.Capability, b.Metadata.Name, b.Metadata.Version)
	})
	out := make([]types.Recipe, 0, len(keys))
	for _, key := range keys {
		out = append(out, recipes[key])
	}
	return out
}

// RecipesImplementing returns every Recipe this catalog holds whose
// spec.implements satisfies componentType, ordered by identity
// (capability/name:version), every version of a Recipe included. A componentType
// naming no version is asked at the version this catalog resolves it to — the
// highest version of the Component it holds — so it finds the Recipes declaring
// that version; one this catalog holds no Component of is asked as written.
//
// It is the enumeration the no-mapping diagnostic lists its pin candidates from,
// handed on whole rather than as identity keys, so a reader needing a Recipe's
// capability and name reads them off the entity rather than parsing them back out
// of a key. It suggests and never resolves: a build resolves a Recipe from the
// Manifest's mapping alone, so a reader drafting a mapping from this answer drafts
// a file the operator reviews, and the build reads only what that file says.
func (c *Catalog) RecipesImplementing(componentType string) []types.Recipe {
	if c == nil {
		return nil
	}
	return implementingRecipes(c.Recipes, componentType, c.componentOfType)
}

// RecipeCapabilities returns the capabilities this catalog's Recipes bind to:
// the bare spec.capability of each, without the version pin a Recipe may write
// after it, each once and sorted. A Recipe naming no capability contributes
// none.
func (c *Catalog) RecipeCapabilities() []string {
	if c == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, r := range c.Recipes {
		if r.Spec.Capability == "" {
			continue
		}
		seen[types.ParseVersionPin(r.Spec.Capability).Base()] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for capability := range seen {
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

// implementingFeatureRecipes is the one enumeration of the FeatureRecipes whose
// spec.implements satisfies featureType, ordered by their identity keys — the
// FeatureRecipe analogue of implementingRecipes. It also returns the request it
// asked with, so a reader naming what was searched for names the version the
// enumeration used rather than resolving the type a second time.
//
// The type is resolved through featureImplementsRequest and through nothing
// else, for the reason implementingRecipes resolves through implementsRequest: a
// reader resolving it on its own would be a second resolution free to disagree.
// A feature type naming no version is therefore asked at the version feature
// resolves it to: the matcher answers a request naming no version only with an
// implements naming none either, while catalog FeatureRecipes declare the
// version they implement, so a bare type asked as written would find none of
// them.
//
// Ancestry-agnostic, as implementingRecipes is: it provides suggestions, and
// extends ancestry reaches only the mapping-consistency rule that resolves it.
// Feature extends carries its own composition semantics, which this surface
// takes no position on.
func implementingFeatureRecipes(frs map[string]types.FeatureRecipe, featureType string, feature featureResolver) (string, []types.FeatureRecipe) {
	requested := featureImplementsRequest(featureType, feature)
	var out []types.FeatureRecipe
	for _, fr := range frs {
		if resolver.ImplementsSatisfies(fr.Spec.Implements, requested) {
			out = append(out, fr)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return featureRecipeIdentity(out[i]) < featureRecipeIdentity(out[j])
	})
	return requested, out
}

// FeatureRecipesImplementing returns every FeatureRecipe this catalog holds whose
// spec.implements satisfies featureType, ordered by identity
// (capability/name:version), every version of a FeatureRecipe included. A
// featureType naming no version is asked at the version this catalog's Feature
// resolution selects for it; one this catalog holds no Feature of is asked as
// written.
//
// It is the enumeration the unmapped-feature halt lists its pin candidates from,
// handed on whole, as [Catalog.RecipesImplementing] is for Recipes: it suggests
// and never resolves, so a reader drafting a feature mapping from this answer
// drafts a file the operator reviews, and the build reads only what that file
// says.
func (c *Catalog) FeatureRecipesImplementing(featureType string) []types.FeatureRecipe {
	if c == nil {
		return nil
	}
	_, out := implementingFeatureRecipes(c.FeatureRecipes, featureType, c.featureOfType)
	return out
}

// featureRecipeIdentity is a FeatureRecipe's catalog identity key.
func featureRecipeIdentity(fr types.FeatureRecipe) string {
	return recipeIdentityKey(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)
}

// featureRecipeIdentities projects FeatureRecipes onto their identity keys, in
// order.
func featureRecipeIdentities(frs []types.FeatureRecipe) []string {
	ids := make([]string, 0, len(frs))
	for _, fr := range frs {
		ids = append(ids, featureRecipeIdentity(fr))
	}
	return ids
}

// hostTargetCapability is the capability the component instance nodeID renders
// for: the capability, its version pin removed, that the Recipe the instance
// renders through binds to. An attachment on the instance whose FeatureRecipe
// binds to another capability still renders, as its own output beside the
// instance's rather than into it, so this capability tells the attachment's
// candidates apart without ruling any out. The Recipe is the one the render's own
// lookup answers for the instance (componentTypeToRecipeWithManifest, which
// RecipeLookupWithManifest hands the renderer), so the capability named is the
// one the build renders the instance for. It reads that Recipe's declared
// spec.capability and infers nothing from the Recipe's content.
//
// "" where the target cannot be told here: the instance is not in the graph, or
// the render's lookup answers no Recipe for it, or one declaring no capability.
// Each of those is reported by the reading that owns it, and an attachment on
// such an instance is offered every implementer.
func (c *Catalog) hostTargetCapability(manifest *types.Manifest, graph *types.IRGraph, nodeID string) string {
	node, ok := graph.Spec.Nodes.Get(nodeID)
	if !ok {
		return ""
	}
	r, found := c.componentTypeToRecipeWithManifest(manifest)(nodeID, node.Component)
	if !found {
		return ""
	}
	return types.BareCapability(r.Spec.Capability)
}

// componentRecipeReferenceShapeForVerify reports a component instance whose
// Manifest mapping names a reference no Recipe reference shape can express.
//
// It is what remains here of the per-component resolution reading. Whether an
// instance resolves to a Recipe at all — unmapped, mapped at a reference the
// catalog does not hold, mapped at a reference whose version pin is malformed,
// or matching several version-distinct mapping keys — is a rule declared over the
// flattened composition and asked where the node set is complete, which is before
// this runs. Asking it again here would report one violation twice.
//
// The shape reading stays because it is a different condition with a different
// root, and one this function has always been a DELIBERATE second reading of
// rather than the only one: resolver.ManifestRecipeReferenceFaults judges the
// same shape over the authored mapping entry and names the key the author wrote,
// while this is rooted at the component instance whose resolution missed and
// names the instance and its type — the coordinates an instance-keyed reader has
// and a mapping-keyed one does not. Do not "unify" the two: the grammar itself
// is single-sourced in pkg/resolver, which is what keeps them from disagreeing.
//
// A reference the shape cannot express is reported as a shape fault rather than
// as an absent recipe, because the recipe it names may well be in the catalog
// and calling it absent sends the author looking for the wrong thing. The
// reading runs only after the lookup misses, so it never changes which
// references resolve. nil means there is no shape fault to report: the instance
// resolved, or a rule above reports it — a reference of an accepted shape whose
// version pin is malformed included.
func (c *Catalog) componentRecipeReferenceShapeForVerify(manifest *types.Manifest, instanceName, componentType string) error {
	uri, ok, err := ComponentRecipeURI(manifest, instanceName, componentType)
	if err != nil || !ok {
		// A refused lookup names no reference, so there is no shape to judge; the
		// refusal itself is the rule's above.
		return nil
	}
	if _, found := c.RecipeByURI(uri); found {
		return nil
	}
	if n, tooMany := resolver.RecipeReferenceSegments(uri); tooMany {
		return fmt.Errorf("%w: component %q (type %q): the Manifest maps it to %q, which carries %d slash-separated segments; a recipe reference accepts %s; correct the mapping value in the Manifest", resolver.ErrMalformedRecipeReference(), instanceName, componentType, uri, n, resolver.AcceptedRecipeReferenceShapes)
	}
	if segment, pinned := resolver.RecipeReferenceCapabilityPin(uri); pinned {
		return resolver.PinnedCapabilityReferenceFault(
			fmt.Sprintf("component %q (type %q): the Manifest maps it to", instanceName, componentType),
			uri, segment, "recipe reference")
	}
	return nil
}

// mappedRecipePinFault reports a Manifest mapping value that resolved to no
// feature recipe and whose version pin is malformed, rooted at the feature
// instance whose resolution missed: the pin names no version, so the fault is
// the pin's text rather than an absent feature recipe. nil when the pin is well
// formed or absent, which leaves an absent feature recipe to the reading that
// owns it.
//
// It is read on the feature attachment axes only. A component instance mapped
// to a malformed pin is a rule declared over the flattened composition,
// NodeMappedRecipePinIsWellFormed, because every fact that rule reads is keyed
// by a node. No fact carries the feature recipe reference a feature
// attachment's mapping names, or whether the catalog holds it — the mapping
// facts over the composition are keyed by node alone — so there is nothing a
// rule over the attachments could read, and the reading stays here.
func mappedRecipePinFault(uri, instanceName, featureType string) error {
	pe, malformed := types.ParseVersionPin(uri).Malformed()
	if !malformed {
		return nil
	}
	return pe.At(fmt.Sprintf("the Manifest maps feature %q (type %q) to it", instanceName, featureType))
}

// resolveFeatureRecipeForVerify verifies that one feature instance resolves
// through an explicit Manifest mapping. Resolution is mapping-only: a mapped
// instance whose feature recipe is in the catalog resolves cleanly; a mapping to
// an absent feature recipe (dangling) or no mapping at all halts loudly. An
// unmapped instance always halts regardless of how many feature recipes
// implement its type — the diagnostic lists those as pin candidates (implements
// suggests; the author chooses; the Manifest records). nil means the instance
// resolves cleanly.
//
// It still carries the whole reading, where the component side carries only the
// reference-shape half: the two attachment axes it is walked over are not the
// node axis, so the rule asked over the flattened composition — which is keyed by
// node — is not asked about them and nothing here would be reported twice.
//
// An unmapped instance's candidates are the FeatureRecipes implementing the type
// at the version feature resolves it to, so a type the Aggregate writes with no
// version is offered the FeatureRecipes declaring the version the build compiles
// it at; unmappedFeatureFault words the refusal for the case the instance is in,
// target being the capability the instance's component renders for, or "" where
// that is not known here.
func (c *Catalog) resolveFeatureRecipeForVerify(manifest *types.Manifest, instanceName, featureType string, feature featureResolver, target string) error {
	uri, ok, err := FeatureRecipeURI(manifest, instanceName, featureType)
	if err != nil {
		return err
	}
	if ok {
		if _, found := c.FeatureRecipeByURI(uri); !found {
			// Deliberate second reading for the reason the component branch records:
			// this one is rooted at the feature instance where the earlier reading is
			// rooted at the authored mapping key, and the grammar is single-sourced
			// either way.
			if n, tooMany := resolver.RecipeReferenceSegments(uri); tooMany {
				return fmt.Errorf("%w: feature %q (type %q): the Manifest maps it to %q, which carries %d slash-separated segments; a feature recipe reference accepts %s; correct the mapping value in the Manifest", resolver.ErrMalformedRecipeReference(), instanceName, featureType, uri, n, resolver.AcceptedRecipeReferenceShapes)
			}
			if segment, pinned := resolver.RecipeReferenceCapabilityPin(uri); pinned {
				return resolver.PinnedCapabilityReferenceFault(
					fmt.Sprintf("feature %q (type %q): the Manifest maps it to", instanceName, featureType),
					uri, segment, "feature recipe reference")
			}
			if fault := mappedRecipePinFault(uri, instanceName, featureType); fault != nil {
				return fault
			}
			return fmt.Errorf("%w: feature %q (type %q): the Manifest maps it to feature recipe %q, which is not in the catalog", ErrNoRecipeResolved(), instanceName, featureType, uri)
		}
		return nil
	}
	requested, implementers := implementingFeatureRecipes(c.FeatureRecipes, featureType, feature)
	return unmappedFeatureFault(instanceName, featureType, requested, implementers, target, c.recipeSetsFromPinsAlone)
}

// unmappedFeatureFault is the refusal for a feature instance the Manifest maps to
// no FeatureRecipe, worded for the case it is in. requested is the type at the
// version the catalog was searched for, implementers the FeatureRecipes
// implementing it, and target the capability the instance's component renders
// for, "" where that is not known:
//
//   - no FeatureRecipe implements requested: it names requested, which is what
//     the catalog was searched for;
//   - target is known and some implementers bind to it: it names those, to pin
//     one;
//   - target is known and every implementer binds to another capability: it says
//     none implements requested for target, names the ones that do, and offers
//     both remedies — pin one of those, which renders as its own output beside
//     the component's rather than into it, or write one for target;
//   - target is "": it names every implementer.
//
// An attachment naming no type — one the Release writes with no Aggregate
// attachment of its name to configure — is none of those: there is no type to
// search for, so the refusal says the attachment names none and where to write it.
//
// Every case is the one condition, no-recipe-resolved: what is missing in each is
// a Manifest mapping naming a FeatureRecipe that implements the type. One binding
// to a capability other than the component's still renders, as its own output
// beside the component's, which is why case three offers those as well.
//
// pinsAlone says the implementers were enumerated from the FeatureRecipes the
// lockfile pins and nothing else — a view reading no catalog directory, whose
// FeatureRecipes all come from a registry by way of a pin. A registry is never
// enumerated, and a Manifest that maps no FeatureRecipe for the type pins none, so
// in that view the search finds none by construction while a registry may publish
// several. Where an absence is claimed there, it is claimed of the pins rather
// than of "the catalog", and the message says that a registry's FeatureRecipes
// were not searched:
//
//   - no implementer: it names target, where known, as case three does, and
//     offers pinning one a registry publishes beside writing one, since a registry
//     is where one would be found;
//   - implementers for other capabilities alone: case three, its claim made of
//     the pins.
func unmappedFeatureFault(instanceName, featureType, requested string, implementers []types.FeatureRecipe, target string, pinsAlone bool) error {
	if featureType == "" {
		// An attachment naming no type has no Feature to search a realization for,
		// so naming the search would print an empty type twice. The Aggregate's
		// schema requires a type on every attachment it declares, so what reaches
		// here is an attachment the Release writes without one and with no
		// Aggregate attachment of that name for it to configure.
		return fmt.Errorf("%w: feature %q is attached with no type, so no feature recipe can be chosen for it: an "+
			"attachment the Release writes names its Feature under type, unless the Aggregate declares an "+
			"attachment of the same name for it to configure; name the Feature under the attachment's type in the "+
			"Release, or declare the attachment in the Aggregate and configure it from the Release",
			ErrNoRecipeResolved(), instanceName)
	}
	searched, scope := "in the catalog", ""
	if pinsAlone {
		searched = "the lockfile pins"
		scope = "; with no catalog directory, the feature recipes searched are only those the lockfile pins, not a registry's"
	}
	if len(implementers) == 0 {
		switch {
		case !pinsAlone:
			return fmt.Errorf("%w: feature %q (type %q); no feature recipe in the catalog implements %s; write one, then map the type to it", ErrNoRecipeResolved(), instanceName, featureType, requested)
		case target == "":
			return fmt.Errorf("%w: feature %q (type %q); no feature recipe %s implements %s%s; pin one a registry publishes in the Manifest, or write one, then map the type to it", ErrNoRecipeResolved(), instanceName, featureType, searched, requested, scope)
		default:
			return fmt.Errorf("%w: feature %q (type %q); no feature recipe %s implements %s for capability %q, the capability its component's Recipe binds to%s; pin one a registry publishes in the Manifest, where one for another capability renders as its own output beside the component's, or write one for %q, then map the type to it", ErrNoRecipeResolved(), instanceName, featureType, searched, requested, target, scope, target)
		}
	}
	if target == "" {
		return fmt.Errorf("%w: feature %q (type %q); feature recipes implementing it: %v; pin one in the Manifest", ErrNoRecipeResolved(), instanceName, featureType, featureRecipeIdentities(implementers))
	}
	var forTarget, elsewhere []string
	for _, fr := range implementers {
		if types.BareCapability(fr.Spec.Capability) == target {
			forTarget = append(forTarget, featureRecipeIdentity(fr))
		} else {
			elsewhere = append(elsewhere, featureRecipeIdentity(fr))
		}
	}
	if len(forTarget) > 0 {
		return fmt.Errorf("%w: feature %q (type %q); feature recipes implementing it for capability %q, the capability its component's Recipe binds to: %v; pin one in the Manifest", ErrNoRecipeResolved(), instanceName, featureType, target, forTarget)
	}
	return fmt.Errorf("%w: feature %q (type %q); no feature recipe %s implements %s for capability %q, the capability its component's Recipe binds to%s; feature recipes implementing it for other capabilities: %v; pin one of those in the Manifest, and it renders as its own output beside the component's, or write one for %q, then map the type to it", ErrNoRecipeResolved(), instanceName, featureType, searched, requested, target, scope, elsewhere, target)
}

// ResolutionFindings is the shared resolution-completeness producer over the
// axes no rule keyed by a node covers: it walks every feature instance in the
// IR, on both attachment axes, and returns one error per instance that does NOT
// resolve through an explicit Manifest mapping (unmapped — naming the
// implementing feature recipes as pin candidates — or mapped to a feature recipe
// absent from the catalog). The slice is in IR author order; an empty slice means
// every instance it covers resolves cleanly. Both surfaces consume this single
// routine so they cannot drift: the CLI build joins the slice into a loud halt
// (VerifyRecipeResolution); the serve daemon maps each finding to a resilient
// diagnostic. The mapping-only cascade matches the renderer's — the catalog
// never chooses a recipe by spec.implements.
//
// The component axis is walked here for one condition only: a mapping value whose
// SHAPE no recipe reference can express, which the rules over the composition pass
// over. Whether a component instance resolves to a recipe at all — a malformed
// version pin on the value it is mapped to included — is a rule declared over the
// flattened composition, asked where the node set is complete — which is earlier
// than this, and is the only point at which a component a sub-aggregate
// contributed is in the set being walked. A build carrying such a fault therefore
// halts before this runs, and asking the same question here would report one
// violation twice.
//
// A Manifest name-tier key naming nothing in the build is not reported here
// either: that is a rule over the Manifest's own mapping entries, declared as
// ComponentNameMappingNamesAComponent and FeatureNameMappingNamesAnAttachment and
// asked once the graph is final, so every surface reading this reads those
// findings beside it rather than through it.
//
// It resolves feature types through this catalog's own Feature index. A recipe
// shell assembled over another backing holds no Features of its own, so such a
// shell's owner calls resolutionFindings with the backing's Feature resolution
// instead of calling this.
func (c *Catalog) ResolutionFindings(manifest *types.Manifest, graph *types.IRGraph) []error {
	return c.resolutionFindings(manifest, graph, c.featureOfType)
}

// resolutionFindings is ResolutionFindings with the Feature resolution an
// unmapped feature instance's candidates are enumerated through handed in: this
// catalog's own index for an eagerly-loaded catalog, the backing's own
// resolution for a recipe shell over another backing, whose Feature index is
// empty by construction.
//
// It resolves each attachment's type afresh rather than reading the version the
// graph records on a per-component attachment (IRFeatureEntry.ResolvedVersion):
// a boundary attachment (IRBoundaryFeature) records no resolved version, and the
// component side's pin candidates re-resolve a bare type the same way, so both
// axes and both kinds are asked through one resolution.
//
// A per-component attachment's candidates are told apart by the capability its
// component renders for (hostTargetCapability). A boundary attachment's are not:
// the graph records no single capability for a boundary, whose components may
// render through several, so it is offered every implementer.
func (c *Catalog) resolutionFindings(manifest *types.Manifest, graph *types.IRGraph, feature featureResolver) []error {
	if graph == nil {
		return nil
	}
	var errs []error
	for _, nodeID := range graph.Spec.Nodes.Keys() {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		if err := c.componentRecipeReferenceShapeForVerify(manifest, nodeID, node.Component); err != nil {
			errs = append(errs, err)
		}
	}
	for nodeID, feats := range graph.Spec.FeatureValuesMapping.All() {
		target := c.hostTargetCapability(manifest, graph, nodeID)
		for instName, entry := range feats.All() {
			if err := c.resolveFeatureRecipeForVerify(manifest, instName, entry.Type, feature, target); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for bp, feats := range graph.Spec.BoundaryFeatures.All() {
		_ = bp
		for instName, bf := range feats.All() {
			if err := c.resolveFeatureRecipeForVerify(manifest, instName, bf.Type, feature, ""); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

// VerifyRecipeResolution is the CLI build's pre-render resolution-completeness
// halt: it joins ResolutionFindings into a single error so an unmapped instance
// halts the build loudly rather than silently rendering nothing. Returns nil
// when every instance resolves cleanly. Signature matches
// pipeline.Options.RecipeResolutionVerifier. It reads ResolutionFindings, so a
// recipe shell over another backing joins resolutionFindings with the backing's
// Feature resolution instead, for the reason ResolutionFindings gives.
func (c *Catalog) VerifyRecipeResolution(manifest *types.Manifest, graph *types.IRGraph) error {
	return errors.Join(c.ResolutionFindings(manifest, graph)...)
}

// VerifyManifestImplementsConsistency verifies, for every mapping entry the
// Manifest declares on the type axis, that the resolved Recipe / FeatureRecipe's
// spec.implements matches the Manifest's component-type / feature-type key for
// that entry. Mismatches produce ErrManifestImplementsMismatch carrying the
// Manifest file and the entry's authored site; all violations are collected
// rather than short-circuiting on the first.
//
// The rule itself is declared beside the matcher it is, in the package this one
// reads the catalog through. This method presents the catalog to it: the two
// recipe lookups it needs, and the ancestry resolver the caller supplied. What
// stays here is the presentation, so neither production call site's signature
// moves and neither path loses the gate.
//
// A component mapping is also consistent when the recipe implements a base the
// mapped type extends, at any depth of the chain: a Recipe written against a base
// serves everything extending that base, so a specialization needs a mapping
// entry but no Recipe of its own. The ancestry arrives through the
// ComponentAncestryResolver argument, which each backing supplies over the index
// its own Components live in — this catalog's own index via
// (*Catalog).ComponentAncestry, or the entity cascade a lazily-enumerated view
// reads. A nil resolver declares that the caller's catalog resolves no ancestry,
// which is honest for a surface holding no Components and wrong for one that
// does. Ancestry is resolved only after the direct comparison fails, so a
// mapping that matches outright costs no component lookup, and a chain that
// cannot be walked is reported as the chain fault it is rather than as a mapping
// mismatch.
//
// Feature mappings are judged on the direct comparison alone. Feature extends
// composes its pillars rather than inheriting them, so whether a FeatureRecipe
// written against a base serves an extending Feature is a separate question this
// surface does not answer.
//
// A mapping whose recipe URI does not resolve in the catalog is skipped, not
// reported: a missing/unresolvable recipe is a resolution concern owned by the
// recipe-resolution path (VerifyRecipeResolution and the catalog lookup), which
// surfaces it with richer context — including, for an entity skipped at tolerant
// load, a replay of WHY it went missing. Reporting "not found" here too would
// preempt that better diagnostic. This is therefore the found-but-mismatched
// reading only.
//
// Returns nil when manifest is nil, when no mappings are populated, or when
// every resolved mapping is consistent.
func (c *Catalog) VerifyManifestImplementsConsistency(manifest *types.Manifest, manifestPath string, ancestry ComponentAncestryResolver) []error {
	return resolver.ManifestImplementsConsistencyFaults(manifestPath, manifest, resolver.CatalogLookup{
		Recipe:        c.RecipeByURI,
		FeatureRecipe: c.FeatureRecipeByURI,
		// The ancestry resolver is the caller's and not this receiver's, which is
		// the whole reason it is an argument: a catalog shell assembled over a
		// backing whose Component index is empty by construction would resolve no
		// ancestry at all, and the gate would then refuse every mapping onto a
		// base's Recipe — silently, and only on that path.
		ComponentAncestry: ancestry,
	})
}

// AssertManifestImplementsConsistency runs VerifyManifestImplementsConsistency
// and joins any violations into a single error (nil when consistent). It is the
// production halt surface: every caller that gates a build (the CLI build and
// pull commands, and the build-faithful pipeline harness) calls this so the
// halt semantics — collect-all-then-join — are identical across all of them.
// The check is mapping-table-level (mapped type vs the resolved recipe's
// spec.implements) and runs before IR construction, complementing the post-IR
// per-node VerifyRecipeResolution.
//
// ancestry carries the same contract it carries on
// VerifyManifestImplementsConsistency: it is the caller's Component index, and a
// caller whose catalog holds Components passes one so a mapping onto a base's
// Recipe is judged consistent.
func (c *Catalog) AssertManifestImplementsConsistency(manifest *types.Manifest, manifestPath string, ancestry ComponentAncestryResolver) error {
	errs := c.VerifyManifestImplementsConsistency(manifest, manifestPath, ancestry)
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// componentMappingURI returns the Recipe URI the Manifest maps the given
// component instance to, when the components mapping is populated. Resolution
// is name-wins-over-type: a `name:` override on instanceName takes precedence
// over the (version-tolerant) `type:` match on componentType.
func componentMappingURI(manifest *types.Manifest, instanceName, componentType string) (string, bool, error) {
	match, ok, err := componentMappingEntry(manifest, instanceName, componentType)
	return match.reference, ok, err
}

// componentMappingEntry is componentMappingURI answering with the entry the
// lookup settled on, so a reader that also needs the type-axis key reads the
// same resolution the reference came from.
func componentMappingEntry(manifest *types.Manifest, instanceName, componentType string) (mappingMatch, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Components == nil {
		return mappingMatch{}, false, nil
	}
	return nameThenTypeEntry(manifest.Spec.Mappings.Components, instanceName, componentType)
}

// featureMappingURI is the Feature analogue of componentMappingURI:
// name-wins-over-type resolution of a feature instance against the features
// mapping.
func featureMappingURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	match, ok, err := featureMappingEntry(manifest, instanceName, featureType)
	return match.reference, ok, err
}

// featureMappingEntry is featureMappingURI answering with the entry the lookup
// settled on, so a reader that also needs the type-axis key reads the same
// resolution the reference came from.
func featureMappingEntry(manifest *types.Manifest, instanceName, featureType string) (mappingMatch, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Features == nil {
		return mappingMatch{}, false, nil
	}
	return nameThenTypeEntry(manifest.Spec.Mappings.Features, instanceName, featureType)
}

// ComponentRecipeURI returns the Recipe URI the Manifest maps componentType
// to. The returned URI is the same verbatim string the dependency-closure
// walker records as a source-descent entity, so callers keying per-component
// data by this value join cleanly against the closure's entities (regardless of
// whether the author wrote the mapping value in short form or full-path form).
//
// Returns ("", false) when no Manifest mapping names componentType. Resolution
// is mapping-only, so such a component carries no source-descent entity and
// correctly contributes no source artifact: an unmapped instance halts loudly in
// the pre-render resolution-completeness pass rather than resolving by any other
// route.
func ComponentRecipeURI(manifest *types.Manifest, instanceName, componentType string) (string, bool, error) {
	return componentMappingURI(manifest, instanceName, componentType)
}

// FeatureRecipeURI is the FeatureRecipe analogue of ComponentRecipeURI: it
// returns the FeatureRecipe URI the Manifest maps featureType to. The returned
// URI is the same verbatim string the dependency-closure walker records as a
// feature_recipe source-descent entity, so callers keying per-feature-instance
// data by this value join cleanly against the closure's entities.
//
// Returns ("", false) when no Manifest mapping names featureType.
func FeatureRecipeURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	return featureMappingURI(manifest, instanceName, featureType)
}

// stripVersion removes the :version segment from a URI, returning the
// base. Used by the consistency check to compare base-URI portions
// independent of version form.
func stripVersion(uri string) string {
	base, _, _ := resolver.SplitVersionedURI(uri)
	return base
}
