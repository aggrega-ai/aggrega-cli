// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	Message:   "manifest maps multiple version-distinct keys to the same base type; the type-to-recipe lookup is ambiguous",
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
		"configuration composes, before the graph is built, whether or not a Recipe asks for its capability. " +
		"The binding is the whole of what this condition reports — without one the same empty candidate set " +
		"is a bare miss the caller resolves — and a binding is written in the workspace configuration rather " +
		"than in any document a compilation reads. The descent walks a closure to decide what to pin, the " +
		"build-time re-run reads a recorded identity while a stage's plugin self-reports are being produced, " +
		"and the per-binding judgement reads the configuration ahead of the first stage, so no declared stage " +
		"of a compilation holds the binding on any arm."),
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
		"binding the workspace configuration composes, before the graph is built, asking the capability " +
		"resolution whether any provider-rendering plugin the build reads serves the capability. A binding " +
		"is written in the workspace configuration rather than in any document a compilation reads, and " +
		"the judgement reads it ahead of the first stage, so no declared stage of a compilation holds it."),
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
// spec.capability. Identity is provider-scoped because the catalog tree
// (recipe/{provider}/{name}/) and the registry identity
// (recipe/{provider}/{name}:{version}) both are: two providers may each
// ship a recipe of the same name and version, and the naming convention
// keeps the provider out of metadata.name. An entity with an empty
// provider keys on the flat "name:version" form, so a
// malformed-but-loading entity keeps a stable identity rather than
// gaining a leading slash.
func recipeIdentityKey(provider, name, version string) string {
	if provider == "" {
		return recipeURIKey(name, version)
	}
	return provider + "/" + recipeURIKey(name, version)
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
// type-to-recipe mapping only needs to find the recipe; the concrete version is
// pinned downstream by the closure's minimal-version selection. A caret key
// (`core/x:^1.0.0`) base-matches like any other; a single base match resolves; a
// base shared by multiple version-distinct keys is ambiguous and returns
// ErrAmbiguousTypeMapping (a Loud Guardrail), never a silent first-wins.
func versionTolerantTypeGet(typeMap types.OrderedMap[string], lookupType string) (string, bool, error) {
	if uri, ok := typeMap.Get(lookupType); ok {
		return uri, true, nil
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
		return "", false, nil
	case 1:
		return matchURI, true, nil
	default:
		return "", false, &ambiguousTypeMapping{lookupType: lookupType, keys: matchedKeys}
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
// Manifest two-tier precedence per entity kind — the render lookup and the
// pull/source-descent lookup both route through it, so those entry points stay
// dumb forwarders and the precedence cannot drift between the two resolution
// paths. An empty instanceName
// (no per-instance name in scope at a call site) cleanly skips the name tier.
func nameThenType(tier *types.MappingTier, instanceName, lookupType string) (string, bool, error) {
	if tier == nil {
		return "", false, nil
	}
	if instanceName != "" {
		if uri, ok := tier.Name.Get(instanceName); ok {
			return uri, true, nil
		}
	}
	return versionTolerantTypeGet(tier.Type, lookupType)
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
//     against each Recipe's Spec.Capability, and the version is selected among
//     that capability's recipes of that name.
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
// stopped in that resolution before the pass ran, naming the URI no catalog
// entity matches and the mapping entry that referenced it.
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
	// the unmatched URI, where the caller resolved that closure first.
	key, ok, _ := selectCapabilityScopedVersion(c.Recipes, uri, func(r types.Recipe) (string, string, string) {
		return r.Spec.Capability, r.Metadata.Name, r.Metadata.Version
	})
	if !ok {
		return types.Recipe{}, false
	}
	return c.Recipes[key], true
}

// FeatureRecipeByURI is the FeatureRecipe analogue of RecipeByURI; the
// resolution ladder, the version rule and the capability-ambiguity refusal are
// identical, over the FeatureRecipe index.
func (c *Catalog) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	key, ok, _ := selectCapabilityScopedVersion(c.FeatureRecipes, uri, func(fr types.FeatureRecipe) (string, string, string) {
		return fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version
	})
	if !ok {
		return types.FeatureRecipe{}, false
	}
	return c.FeatureRecipes[key], true
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

	matches := map[string]types.Plugin{}
	distinctNames := map[string]struct{}{}
	for key, p := range c.providerResolutionPlugins() {
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

	// The version pin priority: the caller's pin, else the binding's optional
	// version. Threaded into the diagnostic and the version select alike.
	pin := ""
	if refHasPin {
		pin = refPin
	} else if bound && bindHasVer {
		pin = bindVer
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
// providerResolutionPlugins and each plugin's DeclaredCapabilities — so a name it
// reports as served is one the core would match, and no second statement of
// what a plugin serves exists beside the one on the entity. visible counts a
// plugin of any type at the coordinate, because a binding naming a plugin of
// another type still names something the catalog holds.
func (c *Catalog) capabilitiesServed(pluginType, coordinate string) (served []string, visible bool) {
	seen := map[string]struct{}{}
	for _, p := range c.providerResolutionPlugins() {
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
func componentAncestryOver(componentType string, lookup validator.ComponentLookup) ([]string, error) {
	comp, ok := lookup(componentType)
	if !ok {
		return nil, nil
	}
	chain, vErrs := validator.ComponentAncestry(comp, lookup)
	if len(vErrs) > 0 {
		return nil, vErrs[0]
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
	return componentAncestryOver(componentType, func(uri string) (types.Component, bool) {
		key, ok, _ := c.resolveComponentKey(uri)
		if !ok {
			return types.Component{}, false
		}
		return c.Components[key], true
	})
}

// recipesImplementing returns the sorted, provider-qualified identity keys of
// every catalog recipe whose spec.implements satisfies componentType. It is a
// SUGGESTION provider only — resolution is mapping-only and never consults it.
// Its readers are the no-mapping diagnostic (pin candidates) and the Manifest
// recipe-reference completion (implements-aware ranking).
//
// Deliberately ancestry-blind, unlike the mapping-consistency rule: extends
// ancestry is handled in that rule's outer layer rather than inside the matcher,
// so it reaches only the caller that resolves it. Widening the suggestion set to
// recipes implementing a base of the requested type would change which candidates
// a diagnostic lists and how completion ranks them — user-visible output whose
// shape is a separate choice from whether an operator-written mapping is judged
// consistent.
//
// The matcher is read from the package that declares the mapping-consistency rule
// rather than carried here, so this suggestion surface and that rule cannot come
// to disagree about which recipes satisfy a type.
func recipesImplementing(recipes map[string]types.Recipe, componentType string) []string {
	var ids []string
	for _, r := range recipes {
		if resolver.ImplementsSatisfies(r.Spec.Implements, componentType) {
			ids = append(ids, recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version))
		}
	}
	sort.Strings(ids)
	return ids
}

// featureRecipesImplementing is the FeatureRecipe analogue of
// recipesImplementing, ancestry-blind for the same reason: it provides
// suggestions, and extends ancestry reaches only the mapping-consistency rule
// that resolves it. Feature extends carries its own composition semantics, which
// this surface takes no position on.
func featureRecipesImplementing(frs map[string]types.FeatureRecipe, featureType string) []string {
	var ids []string
	for _, fr := range frs {
		if resolver.ImplementsSatisfies(fr.Spec.Implements, featureType) {
			ids = append(ids, recipeIdentityKey(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version))
		}
	}
	sort.Strings(ids)
	return ids
}

// componentRecipeReferenceShapeForVerify reports a component instance whose
// Manifest mapping names a reference no Recipe reference shape can express.
//
// It is what remains here of the per-component resolution reading. Whether an
// instance resolves to a Recipe at all — unmapped, mapped at a reference the
// catalog does not hold, or matching several version-distinct mapping keys — is
// a rule declared over the flattened composition and asked where the node set is
// complete, which is before this runs. Asking it again here would report one
// violation twice.
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
// and calling it absent sends the author looking for the wrong thing. A
// reference of an accepted shape whose version pin is malformed is reported as
// that malformation for the same reason — the rule above passes over it. The
// reading runs only after the lookup misses, so it never changes which
// references resolve. nil means there is no shape or pin fault to report,
// whether the instance resolved or was reported by the rule above.
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
	n, tooMany := resolver.RecipeReferenceSegments(uri)
	if !tooMany {
		return mappedRecipePinFault(uri, "component", instanceName, componentType)
	}
	return fmt.Errorf("%w: component %q (type %q): the Manifest maps it to %q, which carries %d slash-separated segments; a recipe reference accepts %s; correct the mapping value in the Manifest", resolver.ErrMalformedRecipeReference(), instanceName, componentType, uri, n, resolver.AcceptedRecipeReferenceShapes)
}

// mappedRecipePinFault reports a Manifest mapping value that resolved to no
// recipe and whose version pin is malformed, rooted at the instance whose
// resolution missed — the second reading beside the shape one above, for the
// same reason: the recipe the value names may well be in the catalog, and the
// fault is the pin's text rather than an absent recipe. nil when the pin is well
// formed or absent, which leaves an absent recipe to the reading that owns it.
func mappedRecipePinFault(uri, instanceKind, instanceName, instanceType string) error {
	pe, malformed := types.ParseVersionPin(uri).Malformed()
	if !malformed {
		return nil
	}
	return pe.At(fmt.Sprintf("the Manifest maps %s %q (type %q) to it", instanceKind, instanceName, instanceType))
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
func (c *Catalog) resolveFeatureRecipeForVerify(manifest *types.Manifest, instanceName, featureType string) error {
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
			if fault := mappedRecipePinFault(uri, "feature", instanceName, featureType); fault != nil {
				return fault
			}
			return fmt.Errorf("%w: feature %q (type %q): the Manifest maps it to feature recipe %q, which is not in the catalog", ErrNoRecipeResolved(), instanceName, featureType, uri)
		}
		return nil
	}
	if candidates := featureRecipesImplementing(c.FeatureRecipes, featureType); len(candidates) > 0 {
		return fmt.Errorf("%w: feature %q (type %q); feature recipes implementing it: %v; pin one in the Manifest", ErrNoRecipeResolved(), instanceName, featureType, candidates)
	}
	return fmt.Errorf("%w: feature %q (type %q); no feature recipe implements this type", ErrNoRecipeResolved(), instanceName, featureType)
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
// The component axis is walked here for two conditions only: a mapping value whose
// SHAPE no recipe reference can express, and one whose version pin is malformed —
// the two the rule over the composition passes over. Whether a component instance resolves to
// a recipe at all is a rule declared over the flattened composition, asked where
// the node set is complete — which is earlier than this, and is the only point at
// which a component a sub-aggregate contributed is in the set being walked. A
// build carrying such a fault therefore halts before this runs, and asking the
// same question here would report one violation twice.
func (c *Catalog) ResolutionFindings(manifest *types.Manifest, graph *types.IRGraph) []error {
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
		_ = nodeID
		for instName, entry := range feats.All() {
			if err := c.resolveFeatureRecipeForVerify(manifest, instName, entry.Type); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for bp, feats := range graph.Spec.BoundaryFeatures.All() {
		_ = bp
		for instName, bf := range feats.All() {
			if err := c.resolveFeatureRecipeForVerify(manifest, instName, bf.Type); err != nil {
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
// pipeline.Options.RecipeResolutionVerifier.
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
	if manifest == nil || manifest.Spec.Mappings.Components == nil {
		return "", false, nil
	}
	return nameThenType(manifest.Spec.Mappings.Components, instanceName, componentType)
}

// featureMappingURI is the Feature analogue of componentMappingURI:
// name-wins-over-type resolution of a feature instance against the features
// mapping.
func featureMappingURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Features == nil {
		return "", false, nil
	}
	return nameThenType(manifest.Spec.Mappings.Features, instanceName, featureType)
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
