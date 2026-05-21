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

// targetRefPattern validates a target reference that may cross nested
// aggregate boundaries (e.g., a uses.target field referring to a component
// inside a sub-aggregate). The reference is a dot-separated path of
// segments, each of which must match instanceNamePattern.
//
// The pattern structure forbids leading dots, trailing dots, and consecutive
// dots — every segment is required to be non-empty. A bare component name
// (no dots) is accepted as a single-segment path.
var targetRefPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)*$`)

// ValidateInstanceNames walks every user-authored dictionary key in the
// Aggregate (top-level components, top-level aggregates, top-level
// subsystems, then recursively into each subsystem's components, features,
// secrets, and nested subsystems) and checks each against
// instanceNamePattern. Returns every violation found.
//
// Returned errors are sorted by (Entity, Field) for byte-identical output
// across runs of the same input.
//
// Boundary names live on the Release rather than the Aggregate and are
// validated against the same pattern by the Release JSON Schema, not by
// this function.
//
// Returns nil when agg is nil.
func ValidateInstanceNames(agg *types.Aggregate) []*ResolverError {
	if agg == nil {
		return nil
	}
	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	// Top-level components
	for name, comp := range agg.Spec.Components.All() {
		if !instanceNamePattern.MatchString(name) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf(".spec.components[%q]", name),
				Message: fmt.Sprintf("instance name %q is invalid: must match %s (dots are not allowed)", name, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName,
			})
		}
		// Validate feature and secret names within the component
		errs = append(errs, validateComponentInstanceNames(entity, name, comp)...)
	}

	// Top-level aggregates (nested modules)
	for name := range agg.Spec.Aggregates.All() {
		if !instanceNamePattern.MatchString(name) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf(".spec.aggregates[%q]", name),
				Message: fmt.Sprintf("instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName,
			})
		}
	}

	// Subsystems (recursive)
	for name, sub := range agg.Spec.Subsystems.All() {
		if !instanceNamePattern.MatchString(name) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf(".spec.subsystems[%q]", name),
				Message: fmt.Sprintf("instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName,
			})
		}
		errs = append(errs, validateSubsystemNames(entity, ".spec.subsystems."+name, sub)...)
	}

	sortResolverErrors(errs)
	return errs
}

// ValidateTargetRef checks that a target reference — typically a uses.target
// value or a Feature appliesTo type field that may cross nested aggregate
// boundaries — matches targetRefPattern. The entity and field arguments
// are attached to the returned ResolverError so callers can identify the
// offending location.
//
// Returns nil for a valid reference.
//
// No production call sites today; intended for future uses-target
// validation wiring.
func ValidateTargetRef(ref, entity, field string) *ResolverError {
	if !targetRefPattern.MatchString(ref) {
		return &ResolverError{
			Entity:  entity,
			Field:   field,
			Message: fmt.Sprintf("target reference %q is invalid: each segment must match %s, separated by dots", ref, instanceNamePattern.String()),
			Err:     ErrInvalidTargetRef,
		}
	}
	return nil
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
				Err:     ErrInvalidInstanceName,
			})
		}
	}

	for sname := range comp.Secrets.All() {
		if !instanceNamePattern.MatchString(sname) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s.secrets[%q]", basePath, sname),
				Message: fmt.Sprintf("secret instance name %q is invalid: must match %s", sname, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName,
			})
		}
	}

	return errs
}

func validateSubsystemNames(entity, path string, sub types.Subsystem) []*ResolverError {
	var errs []*ResolverError

	for name, comp := range sub.Components.All() {
		if !instanceNamePattern.MatchString(name) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s.components[%q]", path, name),
				Message: fmt.Sprintf("instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName,
			})
		}
		errs = append(errs, validateComponentInstanceNames(entity, name, comp)...)
	}

	for name := range sub.Features.All() {
		if !instanceNamePattern.MatchString(name) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s.features[%q]", path, name),
				Message: fmt.Sprintf("feature instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName,
			})
		}
	}

	for name, nested := range sub.Subsystems.All() {
		if !instanceNamePattern.MatchString(name) {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s.subsystems[%q]", path, name),
				Message: fmt.Sprintf("instance name %q is invalid: must match %s", name, instanceNamePattern.String()),
				Err:     ErrInvalidInstanceName,
			})
		}
		errs = append(errs, validateSubsystemNames(entity, path+".subsystems."+name, nested)...)
	}

	return errs
}
