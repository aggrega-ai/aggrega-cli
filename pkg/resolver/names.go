// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"regexp"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// instanceNamePattern validates user-authored dictionary keys (component names,
// feature names, secret names, subsystem names, aggregate names).
// The dot character is banned — it is reserved as the nested aggregate separator.
// Rule 16.22: Instance Naming Conventions & The Dot Separator Rule.
var instanceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// targetRefPattern validates target references that may cross nested aggregate
// boundaries. These use dots as path separators (e.g., "nested-agg.component-id").
// Each segment must match the instance name pattern.
var targetRefPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)*$`)

// ValidateInstanceNames checks all user-authored dictionary keys in the Aggregate
// against the instance name pattern. Returns all violations found.
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

// ValidateTargetRef checks that a target reference (in uses blocks, type fields)
// matches the target reference pattern (dot-separated segments of valid instance names).
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
