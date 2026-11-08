// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"context"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// ExtendsSource reads the Component or the Feature a closure entity reference
// resolves to. LibrarySource embeds it, so every source the library discovery
// reads a closure through serves the extends descent too, and the two read an
// entity's document the same way.
type ExtendsSource interface {
	// ClosureComponent and ClosureFeature read the Component or Feature a closure
	// entity reference resolves to. They take the reference whole rather than its
	// URI, because where the walk collected it says which kind it can be, and an
	// implementation that would ask a registry asks only for that kind. They take
	// the caller's context because such a read can leave the process, and a
	// discovery whose caller has withdrawn must not wait on it.
	ClosureComponent(ctx context.Context, ref ResolvedRef) (types.Component, bool)
	ClosureFeature(ctx context.Context, ref ResolvedRef) (types.Feature, bool)
}

// DescendExtendsBases adds to c the base every Component and every Feature in c
// extends, and the base each of those extends in turn, along every chain. A
// Component or a Feature that extends another inherits from it, so a build reads
// the base wherever it reads the entity; the walk records only what a Release, its
// Aggregate and its Manifest name, so a base nothing names directly would otherwise
// be pinned by no lockfile row, fetched by no pull, and missing from the build that
// needs it.
//
// Each entity's document is read through src, and each base its `spec.extends`
// names is resolved through lookup by the same resolution the walk gives every
// reference it collects, under the kind of the entity that names it — a Component
// extends a Component and a Feature a Feature. A lookup that fails halts the
// descent, naming the base, as it halts the walk. A base no source holds is left
// out rather than refused here: the extends resolution that merges the chain is the
// one place a missing parent is reported, in the same words on every surface.
//
// A base c already holds — mapped by a Manifest, say, or reached by another chain —
// is resolved already: the walk resolved it through the same lookup. So it is not
// asked for again; the extending entity is recorded as one more site that
// references it, and its row is kept as the walk wrote it.
//
// It runs before the library discovery, so the libraries a base's schema references
// are discovered and pinned with it. A pull and every computation that holds a
// lockfile to a Release reach it through AugmentClosure, so the rows a pull writes
// for the bases are the rows a build reconstructs.
//
// # How far it follows a chain
//
// A chain is followed as far as the extends rules admit one and no further: a
// chain may reach validator.ExtendsChainBound() entities counting the one it starts
// from, so the descent follows at most one fewer generation of bases from any
// entity c held when it was called. That is one value governing both, and a chain
// running past it is refused by those rules wherever it is merged, so pinning more
// of it would fetch bases nothing can use. A chain that loops back on itself stops
// sooner, at the first reference it meets again, because every reference is read
// once; the loop is reported where the chain is merged.
//
// An entity whose document src cannot read contributes no base: what it extends is
// unknown here, and the extends resolution that merges the chain reports the entity
// it could not read.
//
// Entities are read, and bases resolved, in identity order, and the closure is
// re-canonicalised before return, so the result does not depend on the order
// anything was met in.
func DescendExtendsBases(ctx context.Context, c *Closure, src ExtendsSource, lookup Lookup) error {
	if c == nil || src == nil {
		return nil
	}
	index := entityIndexOf(c)
	read := map[EntityKey]struct{}{}
	level := append([]ResolvedRef(nil), c.Entities...)
	for generation := 1; len(level) > 0 && generation < validator.ExtendsChainBound(); generation++ {
		sort.SliceStable(level, func(i, j int) bool { return level[i].Key().Less(level[j].Key()) })
		bases := newWalkState()
		for _, e := range level {
			if _, done := read[e.Key()]; done {
				continue
			}
			read[e.Key()] = struct{}{}
			if base, site, kind := extendsBaseOf(ctx, src, e); base != "" {
				bases.addEntityKind(base, site, kind)
			}
		}
		keys := make([]EntityKey, 0, len(bases.entities))
		for key := range bases.entities {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].Less(keys[j]) })
		level = level[:0:0]
		for _, key := range keys {
			pending := bases.entities[key]
			if _, held := index[key]; held {
				mergeEntity(c, index, ResolvedRef{URI: key.URI, KindHint: pending.kindHint, ReferencedFrom: pending.sites()})
				continue
			}
			res, err := pending.resolve(ctx, lookup, key.URI)
			if err != nil {
				return err
			}
			if res.NotFound {
				continue
			}
			pending.resolution = &res
			ref := pending.toResolvedRef(key.URI)
			level = append(level, ref)
			mergeEntity(c, index, ref)
		}
	}
	canonicaliseClosure(c)
	return nil
}

// extendsBaseOf returns the base the document e resolves to extends, the site the
// extending entity records it at, and the kind the base is collected as — or an
// empty base where e extends nothing, or src reads no Component or Feature for it.
// e is read as a Component first and then as a Feature, as the library discovery
// reads it, and only as a kind the walk collected it as (ResolvedRef.ReadableAs).
func extendsBaseOf(ctx context.Context, src ExtendsSource, e ResolvedRef) (base, site, kind string) {
	if e.ReadableAs(KindComponent) {
		if comp, ok := src.ClosureComponent(ctx, e); ok {
			return comp.Spec.Extends, extendsSite(KindComponent, comp.Metadata.Name, comp.Metadata.Version), KindComponent
		}
	}
	if e.ReadableAs(KindFeature) {
		if feat, ok := src.ClosureFeature(ctx, e); ok {
			return feat.Spec.Extends, extendsSite(KindFeature, feat.Metadata.Name, feat.Metadata.Version), KindFeature
		}
	}
	return "", "", ""
}

// extendsSite is the site a base is recorded as referenced from: the extending
// entity, named by its kind and its identity, and the field that names the base —
// `component(local/datastore/postgres:0.1.0).spec.extends`. It names the entity
// rather than the reference the walk met it under, so one entity the walk met under
// two spellings records one site.
func extendsSite(kind, name, version string) string {
	identity := name
	if version != "" {
		identity = name + ":" + version
	}
	return kind + "(" + identity + ").spec.extends"
}
