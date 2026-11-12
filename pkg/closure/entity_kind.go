// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The kinds a pinned catalog entity is recorded under, read from the position the
// walk collected its reference at. They are the one vocabulary of entity kinds the
// walk, the lockfile row and the drift comparison share: a lockfile row's kind is
// one of these words, and a collection-site hint names one of them, or names the
// Release's own Aggregate (ReleaseAggregateKindHint), which is an Aggregate.
const (
	// KindAggregate is an Aggregate: the Release's own, or one an Aggregate
	// includes under spec.aggregates.
	KindAggregate = "aggregate"
	// KindComponent is a Component type an Aggregate's component instance or a
	// Manifest's component mapping key names.
	KindComponent = "component"
	// KindFeature is a Feature type an attachment or a Manifest's feature mapping
	// key names.
	KindFeature = "feature"
	// KindFeatureRecipe is a FeatureRecipe a Manifest's feature mapping binds.
	KindFeatureRecipe = "feature_recipe"
	// KindManifest is a Manifest: the Release's own, and the one it extends.
	KindManifest = "manifest"
	// KindPlugin is a Plugin entity.
	KindPlugin = "plugin"
	// KindRecipe is a Recipe a Manifest's component mapping binds.
	KindRecipe = "recipe"
)

// entityKinds is the declared set of entity kinds, in the order the lockfile
// schema enumerates them. It is the one list: EntityKindOf admits exactly these
// words, and a test holds the lockfile schema's enumeration of a row's kind to it.
var entityKinds = []string{
	KindAggregate, KindComponent, KindFeature, KindFeatureRecipe, KindManifest, KindPlugin, KindRecipe,
}

// documentKinds pairs each declared entity kind with the kind an author writes at
// the head of that entity's document. It is the one translation of the walk's word
// for a kind into the author's: a test holds it to cover every declared kind.
var documentKinds = map[string]string{
	KindAggregate:     "Aggregate",
	KindComponent:     "Component",
	KindFeature:       "Feature",
	KindFeatureRecipe: "FeatureRecipe",
	KindManifest:      "Manifest",
	KindPlugin:        "Plugin",
	KindRecipe:        "Recipe",
}

// DocumentKind returns the kind an author writes at the head of the document of an
// entity of kind — "Component" for KindComponent, "Manifest" for KindManifest — and
// the empty string for a word that names no declared kind. A surface reporting a
// closure names each entity's kind this way, so the kind it shows is one the reader
// typed rather than the walk's own word.
func DocumentKind(kind string) string {
	return documentKinds[kind]
}

// ReleaseExternals returns the identities of the entities rel names itself — its own
// Aggregate and its own Manifest — which a resolution reads from the bytes its caller
// registers for them rather than resolving them as dependencies. Each carries the
// kind it is recorded as, so the two stay apart where they share a name, and they
// are listed in the order a reference whose kind its caller does not know is looked
// up among them.
//
// It is the one statement of which entities those are: the registration keys them
// by it (lockfile.ExternalEntitiesFromBytes) and the lookup reads its kinds
// (ReleaseExternalKinds).
func ReleaseExternals(rel *types.Release) []EntityKey {
	return []EntityKey{
		{Kind: KindAggregate, URI: rel.Spec.Aggregate},
		{Kind: KindManifest, URI: rel.Spec.Manifest},
	}
}

// ReleaseExternalKinds returns the kinds ReleaseExternals records, in its order.
func ReleaseExternalKinds() []string {
	externals := ReleaseExternals(&types.Release{})
	kinds := make([]string, len(externals))
	for i, e := range externals {
		kinds[i] = e.Kind
	}
	return kinds
}

// DeclaredEntityKinds returns the declared entity kinds — every word a lockfile row's kind
// may be. The slice is a copy, so a caller cannot change the set.
func DeclaredEntityKinds() []string {
	return append([]string(nil), entityKinds...)
}

// IsEntityKind reports whether kind is one of the declared entity kinds.
func IsEntityKind(kind string) bool {
	for _, k := range entityKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// EntityKey identifies one pinned catalog entity: the kind it was collected as and
// the reference it was collected under. It is the identity of a closure entity and
// of the lockfile row that pins it, so two entities of different kinds that share
// a name — an Aggregate and a Manifest of one name, a Component and a Recipe of one
// name — are two entities, each resolved, pinned and compared on its own.
type EntityKey struct {
	Kind string
	URI  string
}

// Less orders keys by reference, then by kind: the order every closure and every
// lockfile lists its entities in, so two entities sharing a reference sit next to
// each other in a fixed order.
func (k EntityKey) Less(o EntityKey) bool {
	if k.URI != o.URI {
		return k.URI < o.URI
	}
	return k.Kind < o.Kind
}

// Quoted names k in a report: the reference, quoted, followed by the kind where k
// records one — `"core/cache" (component)` — so two entities of one reference are
// told apart wherever a report names one of them.
func (k EntityKey) Quoted() string {
	if k.Kind == "" {
		return fmt.Sprintf("%q", k.URI)
	}
	return fmt.Sprintf("%q (%s)", k.URI, k.Kind)
}

// EntityKindOf returns the kind a collection-site hint records an entity as, and
// the empty string for a hint that names no kind — the empty hint of a caller that
// did not know where a reference was collected.
//
// It is the one mapping from a hint to a kind. The two differ in exactly one word:
// the Release's own Aggregate carries a hint of its own, because the walk resolves
// and descends it differently from an Aggregate another one includes, and it is an
// Aggregate all the same, so an Aggregate the Release deploys and one an Aggregate
// includes under the same name are one entity.
func EntityKindOf(hint string) string {
	if hint == ReleaseAggregateKindHint {
		return KindAggregate
	}
	if IsEntityKind(hint) {
		return hint
	}
	return ""
}

// Key returns the identity of the entity r pins: the kind its collection-site hint
// records and its reference.
func (r ResolvedRef) Key() EntityKey {
	return EntityKey{Kind: r.Kind(), URI: r.URI}
}

// Kind returns the kind of the entity r pins, read from its collection-site hint
// through EntityKindOf. It is derived rather than stored, so the kind a lockfile
// row records and the hint the walk resolved the entity under cannot disagree.
func (r ResolvedRef) Kind() string {
	return EntityKindOf(r.KindHint)
}

// ReadableAs reports whether the entity r pins may be read as an entity of kind,
// by the kind the walk collected it as (KindReadableAs).
func (r ResolvedRef) ReadableAs(kind string) bool {
	return KindReadableAs(r.Kind(), kind)
}

// KindReadableAs reports whether a reference collected as the entity kind
// collected may be read as an entity of kind: where it was collected as that kind,
// or with no kind — the empty kind of a caller that did not know where the
// reference was collected. A reference collected as another kind names that other
// entity, so a document of kind sharing its reference is never read for it — a
// Recipe's reference does not reach a Component of the same name the walk never
// collected.
//
// It is the one statement of that rule, for every pass that reads an entity by
// kind: a closure entity's document (ResolvedRef.ReadableAs), and a resolution
// that asks a source holding one kind whether it may answer a reference.
func KindReadableAs(collected, kind string) bool {
	return collected == "" || collected == kind
}

// KindReadFromCacheSlot reports whether an entity of kind is ever read back from a
// content-cache slot: false for a declared entity kind whose document no registry
// publishes (oci.IsPublishableKind), and true otherwise. It is the one statement of
// which kinds a slot can hold, so the published kind a reference's slot is kept
// under is named only for a kind it admits.
//
// A Manifest is the kind it refuses: it is read from beside the Release that names
// it, from the file, and never fetched from a registry, so no slot holds one and no
// pin a cache read is held to names one. A projection of pins for the cache to
// verify against leaves Manifest rows out by this test, and a fact a pull learned
// about another artifact's manifest is never joined onto a Manifest's row by that
// manifest.
//
// A word naming no declared entity kind is not refused here: the rows of a
// lockfile section holding one kind record none, and a slot serves them; a caller
// that needs the slot's kind refuses a word that names none when it asks for it.
func KindReadFromCacheSlot(kind string) bool {
	return !IsEntityKind(kind) || oci.IsPublishableKind(DocumentKind(kind))
}

// entityIndexOf indexes c's entities by identity, for a pass that merges entities it
// resolves into a closure that already holds some (mergeEntity): an entity it meets
// again under the same kind and reference accumulates onto the one present, and one
// it meets under another kind is an entity of its own.
func entityIndexOf(c *Closure) map[EntityKey]int {
	index := make(map[EntityKey]int, len(c.Entities))
	for i, e := range c.Entities {
		index[e.Key()] = i
	}
	return index
}

// mergeEntity adds ref to c, or, where c already holds an entity of ref's identity,
// accumulates ref's referenced-from sites onto that entity and keeps the hint
// keptKindHint keeps for the two. index is c's index by identity (entityIndexOf),
// and is kept current here.
//
// It is the one merge of an entity a pass resolves into a closure that may already
// hold it, and it meets an identity a second time by the rule the walk's own
// accumulator does, so which pass met an entity first decides nothing about it.
func mergeEntity(c *Closure, index map[EntityKey]int, ref ResolvedRef) {
	if i, held := index[ref.Key()]; held {
		c.Entities[i].ReferencedFrom = mergeReferencedFrom(c.Entities[i].ReferencedFrom, ref.ReferencedFrom)
		c.Entities[i].KindHint = keptKindHint(c.Entities[i].KindHint, ref.KindHint)
		return
	}
	c.Entities = append(c.Entities, ref)
	index[ref.Key()] = len(c.Entities) - 1
}

// keptKindHint returns the hint an entity recorded under current keeps when the
// same identity is met again under incoming. Two hints name one kind — the Release's
// own Aggregate and an Aggregate another one includes — and the Release's own yields
// to the other, so an Aggregate an Aggregate includes is descended as one whichever
// site met it first. Every other hint is its kind's own word, so for one identity
// the hint first recorded stays; an empty incoming hint changes nothing.
func keptKindHint(current, incoming string) string {
	if incoming != "" && (current == "" || current == ReleaseAggregateKindHint) {
		return incoming
	}
	return current
}
