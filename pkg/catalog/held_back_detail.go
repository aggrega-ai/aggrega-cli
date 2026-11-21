// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WithHeldBackDetail returns scoped answering HeldBackDetail from table: for a
// component reference the table holds and an authored property of one of its
// pillars, the highest held version of the Component that declares the property
// among those the instance's constraints exclude, and the constraints that
// exclude it. "Highest" is the version select's, asked for the Component's name
// with that admission predicate, so the version named is the one a reference
// naming no version selects among them, and never a pre-release while a release
// is held. The contracts compared are those of unscoped, the lookup the table was
// resolved over, which answers every version rather than the one selected.
//
// The notes are the version, captioned "declared by"; the version the
// instance compiles at, captioned "held at"; and each constraint that excludes
// the declaring version, in the order the instances declare them and once each:
// the type as written, the Manifest key, or the Recipe the mapping names with its
// spec.implements. A reference the table does not hold, a Feature, and a property
// no excluded version declares answer no detail.
func WithHeldBackDetail(scoped, unscoped resolver.CatalogLookup, table InstanceVersions, view InstanceVersionView) resolver.CatalogLookup {
	scoped.HeldBackDetail = func(componentType, pillar, path string) []constraint.HeldBackNote {
		declared, held := table.Constraints.Get(componentType)
		selected, selectedHeld := table.Selected.Get(componentType)
		if !held || !selectedHeld {
			return nil
		}
		base := types.ParseVersionPin(componentType).Base()
		index := map[string]string{}
		for _, version := range view.ComponentVersions(base) {
			index[entityIndexKey(base, version)] = version
		}
		meta := func(version string) (string, string) { return base, version }
		declaring := func(version string) bool {
			if version == "" || version == selected ||
				len(excludingConstraints(componentTier, base, version, declared, view)) == 0 {
				return false
			}
			schema, resolved := resolver.CompileComponentSchema(unscoped, ReferenceAt(componentType, version))
			return resolved && resolver.PillarDeclaresProperty(schema, pillar, path)
		}
		key, found, _ := selectEntityVersionWhere(index, base, meta, declaring)
		if !found {
			return nil
		}
		version := index[key]
		detail := []constraint.HeldBackNote{
			note("declared by", ReferenceAt(componentType, version)),
			note("held at", selected)}
		excluding := excludingConstraints(componentTier, base, version, declared, view)
		return append(detail, HeldReference{Kind: closure.KindComponent, Holding: excluding}.Notes()...)
	}
	return scoped
}

// note is a HeldBackNote of caption and value.
func note(caption, value string) constraint.HeldBackNote {
	return constraint.HeldBackNote{Caption: caption, Value: value}
}

// excludingConstraints returns the constraints in declared that do not admit the
// version of base, each distinct constraint once, in declaration order.
func excludingConstraints(tier versionTier, base, version string, declared []VersionConstraint, view InstanceVersionView) []VersionConstraint {
	request := versionRequest(base, version)
	var excluding []VersionConstraint
	for _, c := range declared {
		if admitsVersion(tier, c, request, view) {
			continue
		}
		duplicate := slices.ContainsFunc(excluding, func(e VersionConstraint) bool {
			return e.Kind == c.Kind && e.Value == c.Value && e.File == c.File
		})
		if !duplicate {
			excluding = append(excluding, c)
		}
	}
	return excluding
}

// HeldReference is a reference the table selects below the newest version held,
// and what holds it there: Kind is the closure kind of the entity, Reference the
// reference as written, Selected the version the instances compile at, Newest the
// highest version the reference as written admits among the versions the
// selection was made over, and Holding the constraints that do not admit Newest,
// each once, in the order the instances declare them.
type HeldReference struct {
	Kind      string
	Reference string
	Selected  string
	Newest    string
	Holding   []VersionConstraint
}

// HeldBelowNewest returns, in the table's visit order — the component references
// then the feature references — each reference selected below its newest held
// version, with the constraints that exclude that version. A reference selected at
// its newest yields none. view is the view the table was resolved over, which the
// judgement of a Recipe's spec.implements reads.
func (t InstanceVersions) HeldBelowNewest(view InstanceVersionView) []HeldReference {
	var held []HeldReference
	for _, tier := range []versionTier{componentTier, featureTier} {
		selected, newest, constraints := t.Selected, t.Newest, t.Constraints
		if tier.feature {
			selected, newest, constraints = t.FeatureSelected, t.FeatureNewest, t.FeatureConstraints
		}
		for reference, version := range selected.All() {
			highest, _ := newest.Get(reference)
			if highest == "" || highest == version {
				continue
			}
			declared, _ := constraints.Get(reference)
			base := types.ParseVersionPin(reference).Base()
			held = append(held, HeldReference{
				Kind: tier.kind(), Reference: reference, Selected: version, Newest: highest,
				Holding: excludingConstraints(tier, base, highest, declared, view),
			})
		}
	}
	return held
}

// Notes returns the captioned values naming the constraints that hold the
// reference, each constraint as the held-back detail of a validation names it:
// the type as written with the file it is written in, the Manifest key with its
// file, or the Recipe the mapping names with the type it implements.
func (h HeldReference) Notes() []constraint.HeldBackNote {
	tier := componentTier
	if h.Kind == closure.KindFeature {
		tier = featureTier
	}
	var notes []constraint.HeldBackNote
	for _, c := range h.Holding {
		if c.Kind == ConstraintType {
			notes = append(notes, note("type", c.Value), note("written in", c.File))
			continue
		}
		notes = append(notes, mappingConstraintNotes(tier, c)...)
	}
	return notes
}
