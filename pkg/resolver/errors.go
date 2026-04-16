// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
)

// Sentinel errors for errors.Is matching.
var (
	ErrInvalidParameterRef      = errors.New("invalid parameter reference")
	ErrParametersFromLoad       = errors.New("parametersFrom load failed")
	ErrInvalidInstanceName      = errors.New("invalid instance name")
	ErrInvalidTargetRef         = errors.New("invalid target reference")
	ErrYAMLLoad                 = errors.New("YAML load failed")
	ErrInvalidInjectKey         = errors.New("invalid inject key")
	ErrCredentialLabelPartition = errors.New("credential label partition violated")
	ErrMissingRequiredFeature   = errors.New("missing required feature")
	ErrFeatureMergeConflict     = errors.New("feature merge conflict")
	ErrUnknownUsesSocket        = errors.New("unknown uses socket")
	ErrSelfInjectForbidden      = errors.New("self-inject on component without spec.inject allowlist")
	ErrInjectKeyCollision       = errors.New("duplicate delivery key across inject sources")
	ErrSchemaValidation         = errors.New("cueSchema validation failed")
)

// ResolverError is a structured error with entity and field context.
type ResolverError struct {
	Entity  string // e.g. "Aggregate my-app"
	Field   string // e.g. ".spec.components.bad name"
	Message string // human-readable description
	Err     error  // sentinel for errors.Is
}

func (e *ResolverError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s at %s: %s", e.Entity, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Entity, e.Message)
}

func (e *ResolverError) Unwrap() error {
	return e.Err
}
