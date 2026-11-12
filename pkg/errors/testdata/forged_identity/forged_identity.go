// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package forged_identity attempts the deliberate version of the same forgery:
// it declares every method the identity interface names, the unexported marker
// included, and hands the result to the constructor. Declaring a method by that
// name here is legal Go and still satisfies nothing — an unexported method
// belongs to the package that declared it, so this route is refused. It
// must not compile.
package forged_identity

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

type forged struct{}

func (forged) Error() string          { return "the forged condition was reached" }
func (forged) ErrorCode() string      { return "forged-condition" }
func (forged) ErrorShortCode() string { return "E0000" }

// sealedIdentity is spelled exactly as the interface's marker. It is a
// different method: this one is forged_identity.sealedIdentity, and the
// interface names errors.sealedIdentity.
func (forged) sealedIdentity() {}

// Raised is the diagnostic the forgery would produce.
func Raised() error {
	return cerrors.Raise(forged{}, cerrors.CatalogError,
		cerrors.SourceLocation{File: "recipes/api.yaml"}, "the recipe is at fault", nil)
}
