// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"regexp"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// instanceNamePattern validates every user-authored dictionary key the
// developer or operator writes: component names, feature names, secret
// names, subsystem names, aggregate (sub-aggregate) names, boundary names,
// uses-socket names and aliases, and Manifest provider section names.
//
// The pattern allows alphanumerics, underscore, and hyphen — both
// kebab-case and snake_case are valid. The period is reserved as the
// compiler-generated separator for cross-aggregate component references
// (e.g. data-layer.main-db is the flattened ID of component main-db
// inside sub-aggregate data-layer), so authored names cannot contain it.
var instanceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateInstanceNames walks every user-authored dictionary key in the
// Aggregate (top-level aggregates, top-level subsystems, then recursively into
// each subsystem's components, features, secrets, and nested subsystems) and
// checks each against instanceNamePattern. Returns every violation found.
//
// Components authored at the top-level `.spec.components` surface are reached
// through the synthesized default subsystem, whose components validate at the
// root field shape the author typed.
//
// Returned errors are sorted by (Entity, Field) for byte-identical output
// across runs of the same input.
//
// Boundary names live on the Release rather than the Aggregate and are
// validated against the same pattern by the Release JSON Schema, not by
// this function.
//
// Returns nil when agg is nil.
func ValidateInstanceNames(na types.NormalizedAggregate) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}
	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	// Top-level aggregates (nested modules)
	for name := range agg.Spec.Aggregates.All() {
		if !instanceNamePattern.MatchString(name) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf(".spec.aggregates[%q]", name),
				Message: fmt.Sprintf("instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName(),
			})
		}
	}

	// Subsystems (recursive). The accumulator is the enclosing scope's authoring
	// path, so a subsystem's own name diagnostic reports the scope that declares
	// it while its contents report its own scope.
	_ = types.WalkInstanceTree(agg.Spec.Subsystems, types.SubsystemTree(), ".spec", ".spec",
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, string]{
			// The synthesized default holds components authored at the top-level
			// `.spec.components` surface, so its contents validate at the root shape
			// the author typed rather than under the generated scope name. Keys on the
			// provenance flag, not the name string — a user-declared subsystem named
			// "default" is validated as the qualified scope it is.
			Path: func(parentPath, name string, synthesized bool) string {
				if synthesized {
					return parentPath
				}
				return parentPath + ".subsystems." + name
			},
			Enter: func(sub types.Subsystem, name, path, parentPath string) string {
				// A synthesized scope's name is compiler-generated, not author-typed,
				// so it is not held to the authored-name pattern.
				if !sub.Synthesized && !instanceNamePattern.MatchString(name) {
					errs = append(errs, &ResolverError{
						Entity:  entity,
						Field:   fmt.Sprintf("%s.subsystems[%q]", parentPath, name),
						Message: fmt.Sprintf("instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
						Err:     ErrInvalidInstanceName(),
					})
				}
				return path
			},
			Visit: func(comp types.ComponentInstance, name, path string, _ bool, _ string) error {
				if !instanceNamePattern.MatchString(name) {
					errs = append(errs, &ResolverError{
						Entity:  entity,
						Field:   fmt.Sprintf("%s.components[%q]", path, name),
						Message: fmt.Sprintf("instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
						Err:     ErrInvalidInstanceName(),
					})
				}
				errs = append(errs, validateComponentInstanceNames(entity, name, comp)...)
				return nil
			},
			Leave: func(sub types.Subsystem, _, path, _ string) {
				for name := range sub.Features.All() {
					if !instanceNamePattern.MatchString(name) {
						errs = append(errs, &ResolverError{
							Entity:  entity,
							Field:   fmt.Sprintf("%s.features[%q]", path, name),
							Message: fmt.Sprintf("feature instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
							Err:     ErrInvalidInstanceName(),
						})
					}
				}
			},
		})

	sortResolverErrors(errs)
	return errs
}

func validateComponentInstanceNames(entity, compName string, comp types.ComponentInstance) []*ResolverError {
	var errs []*ResolverError
	basePath := fmt.Sprintf(".spec.components[%q]", compName)

	for fname := range comp.Features.All() {
		if !instanceNamePattern.MatchString(fname) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s.features[%q]", basePath, fname),
				Message: fmt.Sprintf("feature instance name %q is invalid: must match %s", fname, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName(),
			})
		}
	}

	return errs
}
