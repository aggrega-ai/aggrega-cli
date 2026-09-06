// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EntityView is the catalog surface a by-name resolution reads.
//
// It names the accessors and nothing else, so a caller supplies whichever
// catalog backing it already holds. Each accessor stays the backing's own — the
// tier cascade, the extends merge and the version selection are implemented
// once, where the backing implements them — and what this package adds is the
// order they are tried in.
//
// The interface is the reason that order can have one home. Two callers holding
// different backings could otherwise share the order only by copying it, and a
// copy of an ordering agrees with its original until one of them learns about a
// kind the other did not.
type EntityView interface {
	Component(uri string) (types.Component, bool)
	PluginByURI(uri string) (types.Plugin, bool)
	Feature(uri string) (types.Feature, bool)
	RecipeByURI(uri string) (types.Recipe, bool)
	FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool)
	AggregateLookup() resolver.AggregateLookup
	SchemaLibrary(uri string) (types.SchemaLibrary, bool)
	MappingLibrary(uri string) (types.MappingLibrary, bool)
	WorkloadLibrary(uri string) (types.WorkloadLibrary, bool)
	ArtifactsLibrary(uri string) (types.ArtifactsLibrary, bool)
}

// Entity is what a name resolved to: the kind that matched, the identity the
// matched entity declares for itself, and the entity as the backing resolved it.
//
// Name and Version are read off the resolved entity rather than off the request,
// so a name carrying a version suffix answers with the identity the entity
// declares. A caller with several resolutions in flight therefore attributes an
// answer from the answer itself.
type Entity struct {
	Kind        string
	Name        string
	Version     string
	Description string

	// Scope is the segment an entity's published location is scoped by: a Recipe's
	// or FeatureRecipe's capability, a Plugin's plugin type. It is empty for every
	// kind that carries none.
	//
	// It is read from the typed entity at the branch that matched, which is the one
	// place holding a value to read it from. Re-deriving it further down from the
	// name a person typed cannot work: a Recipe name is capability-scoped and
	// version-pinnable, so re-parsing would rebuild by hand what the backing already
	// decided, and would rebuild it differently for the kinds whose reference shape
	// and identity shape differ.
	Scope string

	// Definition is the resolved entity in the typed shape its kind declares,
	// carrying whatever merging the backing applied — a Component and a Feature
	// arrive with their inheritance chain already merged.
	Definition any

	// Source and Bytes carry the authored file behind a match, and only an
	// Aggregate match has them. Every other kind is served from the backing's own
	// index, which retains no single authored-file byte source uniformly across
	// kinds, so a caller wanting an authored document for one of those re-encodes
	// the resolved entity rather than reading a file.
	//
	// Source is recorded as the backing records it. A backing that stores a path
	// relative to a workspace root hands back the relative form, so a caller that
	// opens it anchors it against the root the path is relative TO.
	Source string
	Bytes  []byte
}

// entityKind is one rung of the resolution: the kind it matches, and the
// accessor that decides whether a name is one.
type entityKind struct {
	kind    string
	resolve func(EntityView, string) (Entity, bool)
}

// entityKinds is the resolution ladder, in the order a name is tried against it.
//
// The order decides only an ambiguity — one name declared by two kinds — which is
// a workspace nobody should author, but which has to resolve to something rather
// than to whichever kind happened to be tried first on the day.
//
// It is a table rather than a chain of conditions so that the kinds a by-name
// resolution reaches can be enumerated from the same declaration that performs
// the resolution. A list of kind names written beside a chain would be a second
// statement of the same set, and the second statement is the one that stops being
// true when a kind is added.
var entityKinds = []entityKind{
	{"Component", func(v EntityView, n string) (Entity, bool) {
		c, ok := v.Component(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "Component", Name: c.Metadata.Name, Version: c.Metadata.Version,
			Description: c.Metadata.Description, Definition: c}, true
	}},
	{"Plugin", func(v EntityView, n string) (Entity, bool) {
		p, ok := v.PluginByURI(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "Plugin", Name: p.Metadata.Name, Version: p.Metadata.Version,
			Description: p.Metadata.Description, Scope: p.Metadata.PluginType, Definition: p}, true
	}},
	{"Feature", func(v EntityView, n string) (Entity, bool) {
		f, ok := v.Feature(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "Feature", Name: f.Metadata.Name, Version: f.Metadata.Version,
			Description: f.Metadata.Description, Definition: f}, true
	}},
	{"Recipe", func(v EntityView, n string) (Entity, bool) {
		r, ok := v.RecipeByURI(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "Recipe", Name: r.Metadata.Name, Version: r.Metadata.Version,
			Description: r.Metadata.Description, Scope: r.Spec.Capability, Definition: r}, true
	}},
	{"FeatureRecipe", func(v EntityView, n string) (Entity, bool) {
		fr, ok := v.FeatureRecipeByURI(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "FeatureRecipe", Name: fr.Metadata.Name, Version: fr.Metadata.Version,
			Description: fr.Metadata.Description, Scope: fr.Spec.Capability, Definition: fr}, true
	}},
	{"Aggregate", func(v EntityView, n string) (Entity, bool) {
		a, ok := v.AggregateLookup()(n)
		if !ok || a.Aggregate == nil {
			return Entity{}, false
		}
		return Entity{Kind: "Aggregate", Name: a.Aggregate.Metadata.Name, Version: a.Aggregate.Metadata.Version,
			Description: a.Aggregate.Metadata.Description, Definition: a.Aggregate,
			Source: a.Path, Bytes: a.Bytes}, true
	}},
	{"SchemaLibrary", func(v EntityView, n string) (Entity, bool) {
		lib, ok := v.SchemaLibrary(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "SchemaLibrary", Name: lib.Metadata.Name, Version: lib.Metadata.Version,
			Description: lib.Metadata.Description, Definition: lib}, true
	}},
	{"MappingLibrary", func(v EntityView, n string) (Entity, bool) {
		lib, ok := v.MappingLibrary(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "MappingLibrary", Name: lib.Metadata.Name, Version: lib.Metadata.Version,
			Description: lib.Metadata.Description, Definition: lib}, true
	}},
	{"WorkloadLibrary", func(v EntityView, n string) (Entity, bool) {
		lib, ok := v.WorkloadLibrary(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "WorkloadLibrary", Name: lib.Metadata.Name, Version: lib.Metadata.Version,
			Description: lib.Metadata.Description, Definition: lib}, true
	}},
	{"ArtifactsLibrary", func(v EntityView, n string) (Entity, bool) {
		lib, ok := v.ArtifactsLibrary(n)
		if !ok {
			return Entity{}, false
		}
		return Entity{Kind: "ArtifactsLibrary", Name: lib.Metadata.Name, Version: lib.Metadata.Version,
			Description: lib.Metadata.Description, Definition: lib}, true
	}},
}

// ResolveEntity resolves a name against a catalog backing across every kind the
// ladder holds, and returns the first match.
//
// A name that matches nothing reports ok false and no entity. That is a refusal
// for the caller to render, never an empty answer: an empty result reports a
// mistyped name as an entity with nothing to say about itself, which is the
// reading every by-name capability here refuses.
func ResolveEntity(view EntityView, name string) (Entity, bool) {
	if view == nil {
		return Entity{}, false
	}
	for _, k := range entityKinds {
		if e, ok := k.resolve(view, name); ok {
			return e, true
		}
	}
	return Entity{}, false
}

// EntityKinds returns the kinds a by-name resolution reaches, in the order it
// tries them.
//
// It reads the same table the resolution walks, so a surface that tells a caller
// which kinds a name can resolve to promises exactly what the resolution does.
func EntityKinds() []string {
	out := make([]string, 0, len(entityKinds))
	for _, k := range entityKinds {
		out = append(out, k.kind)
	}
	return out
}

// EntityKindForHint renders the kind a closure entry was collected under as the
// kind an author writes, or the empty string when the collection site recorded
// none.
//
// The translation is deliberate rather than incidental. The recorded hint is a
// note the walk keeps for itself about where a reference came from; the kind a
// reader recognises is the one they type at the head of a document. A surface
// showing the hint verbatim would be showing a word that appears nowhere in
// anything the author wrote.
//
// It lives here because every surface that reports a closure faces the same
// question, and a per-surface translation would have some of them answering in
// the author's vocabulary and the rest in the walk's.
func EntityKindForHint(hint string) string {
	switch hint {
	case "recipe":
		return "Recipe"
	case "feature_recipe":
		return "FeatureRecipe"
	case "aggregate":
		return "Aggregate"
	}
	return ""
}
