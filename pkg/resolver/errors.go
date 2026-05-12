// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
)

// Sentinel errors for errors.Is matching. Every ResolverError wraps one of
// these in its Err field; callers compare via errors.Is rather than string
// equality on the Message text.
var (
	// ErrInvalidParameterRef wraps a Go-template parse or execution error
	// surfaced during parameter interpolation.
	ErrInvalidParameterRef = errors.New("invalid parameter reference")

	// ErrParametersFromLoad wraps a read or unmarshal failure for any path
	// listed in Release.spec.parametersFrom.
	ErrParametersFromLoad = errors.New("parametersFrom load failed")

	// ErrInvalidInstanceName wraps a user-authored dictionary key (component,
	// feature, secret, subsystem, aggregate name) that does not match the
	// instance-name pattern.
	ErrInvalidInstanceName = errors.New("invalid instance name")

	// ErrInvalidTargetRef wraps a target reference (e.g., uses.target) whose
	// dot-separated segments do not all match the instance-name pattern.
	ErrInvalidTargetRef = errors.New("invalid target reference")

	// ErrYAMLLoad wraps a file-read or YAML-unmarshal failure for any of the
	// three source documents (Aggregate, Release, Manifest).
	ErrYAMLLoad = errors.New("YAML load failed")

	// ErrInvalidInjectKey wraps an inject delivery key that does not match
	// the expected per-mechanism format.
	ErrInvalidInjectKey = errors.New("invalid inject key")

	// ErrCredentialLabelPartition wraps a Feature instance that violates the
	// credential-label partition rule (credential and non-credential
	// features must not coexist on a single component without explicit
	// labelling).
	ErrCredentialLabelPartition = errors.New("credential label partition violated")

	// ErrMissingRequiredFeature wraps a component whose declared catalog
	// type lists a requiredFeature that no attached Feature instance satisfies.
	ErrMissingRequiredFeature = errors.New("missing required feature")

	// ErrFeatureMergeConflict wraps a conflict during Release-side Feature
	// attachment merging (e.g., divergent values on the same Feature key).
	ErrFeatureMergeConflict = errors.New("feature merge conflict")

	// ErrUnknownUsesSocket wraps a uses-block socket name that does not
	// exist in the catalog Component's usesSchema.
	ErrUnknownUsesSocket = errors.New("unknown uses socket")

	// ErrSelfInjectForbidden wraps a self-inject declaration on a component
	// whose catalog type does not declare a spec.inject allowlist.
	ErrSelfInjectForbidden = errors.New("self-inject on component without spec.inject allowlist")

	// ErrInjectKeyCollision wraps a delivery key declared on more than one
	// inject source for the same component.
	ErrInjectKeyCollision = errors.New("duplicate delivery key across inject sources")

	// ErrSchemaValidation wraps a pillar validation failure (.values
	// from the Aggregate, .infra from the Release boundary).
	ErrSchemaValidation = errors.New("schema validation failed")

	// ErrMissingRequiredSocket wraps a uses-socket marked required in the
	// Component's usesSchema that no instance populates.
	ErrMissingRequiredSocket = errors.New("required uses socket not populated")
)

// ResolverError carries entity and field context alongside a sentinel error.
// Entity follows the convention "<Kind> <metadata.name>" (e.g.
// "Aggregate my-app", "Release prod"); Field is a JSON-path-style locator
// from the entity root (e.g. ".spec.components[\"web\"].features[\"audit\"]")
// or empty for whole-entity errors. Message is a human-readable description.
// Err carries one of the sentinel errors above so callers can match via
// errors.Is. Error() formats Entity, Field (if non-empty), and Message;
// Unwrap returns Err.
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

// ToCompilerError converts the structured ResolverError into a
// *cerrors.CompilerError carrying the appropriate Category. The category
// is classified by the Entity prefix per cerrors.ClassifyEntityCategory:
// Aggregate → DeveloperError, Release → OpsError, catalog entities →
// CatalogError, unknown → DeveloperError (conservative).
//
// The Source location maps the JSON-path Field into the CompilerError's
// SourceLocation.File. Field is a JSON-path-style locator (e.g.,
// ".spec.components[\"web\"]"), not a real file path; the dual semantics
// is documented in pkg/errors's SourceLocation godoc as "the most
// specific authoring locator available, file path or JSON path."
//
// The inner error chain is preserved via cerrors.Wrap, so errors.Is
// against the resolver's sentinel errors (ErrYAMLLoad, ErrSchemaValidation,
// etc.) continues to match through the converted CompilerError.
func (e *ResolverError) ToCompilerError() *cerrors.CompilerError {
	cat := cerrors.ClassifyEntityCategory(e.Entity)
	source := cerrors.SourceLocation{File: e.Field}
	msg := fmt.Sprintf("%s: %s", e.Entity, e.Message)
	return cerrors.Wrap(e, cat, source, msg)
}
