// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package unregistered_condition attempts to raise a diagnostic against a
// condition that reports a code without ever having registered one. The type
// below satisfies the package's MATCHING interface, which is open by design, and
// the attempt is the move a well-meaning author makes: the matching interface
// names the same reads a condition does, so it looks like the key to the
// constructor. It must not compile.
package unregistered_condition

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// invented reports a code and a short code it allocated for itself. Nothing
// registered them, so no reference names them and no other package can match on
// them.
type invented struct{}

func (invented) Error() string          { return "the invented condition was reached" }
func (invented) ErrorCode() string      { return "invented-condition" }
func (invented) ErrorShortCode() string { return "E0000" }

// Raised is the diagnostic an author would get: a head carrying a category and a
// short code that names nothing.
func Raised() error {
	return cerrors.Raise(invented{}, cerrors.CatalogError,
		cerrors.SourceLocation{File: "recipes/api.yaml"}, "the recipe is at fault", nil)
}
