// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WithHeldBackDetail returns scoped answering HeldBackDetail from table: for a
// component reference the table holds and an authored property of one of its
// pillars, the highest held version of the Component that declares the property
// among those the instance's constraints exclude, and the constraints that
// exclude it. The contracts compared are those of unscoped, the lookup the table
// was resolved over, which answers every version rather than the one selected.
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
		versions := view.ComponentVersions(base)
		// The highest excluded version that declares the property: the versions
		// are ascending, so the walk runs from the end.
		for _, version := range slices.Backward(versions) {
			if version == "" || version == selected {
				continue
			}
			excluding := excludingConstraints(componentTier, base, version, declared, view)
			if len(excluding) == 0 {
				continue
			}
			reference := ReferenceAt(componentType, version)
			schema, resolved := resolver.CompileComponentSchema(unscoped, reference)
			if !resolved || !resolver.PillarDeclaresProperty(schema, pillar, path) {
				continue
			}
			detail := []constraint.HeldBackNote{
				note("declared by", reference),
				note("held at", selected)}
			for _, c := range excluding {
				if c.Kind == ConstraintType {
					detail = append(detail, note("type", c.Value), note("written in", c.File))
					continue
				}
				detail = append(detail, mappingConstraintNotes(componentTier, c)...)
			}
			return detail
		}
		return nil
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
