// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package envelope_literal attempts to build a categorised diagnostic by
// writing the envelope's fields in a composite literal, which is how a
// diagnostic would come into existence without any constructor deciding whether
// it names a halting condition. It must not compile.
package envelope_literal

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// The literal renders exactly like a diagnostic that names a registered
// condition — bracketed category, source pointer, message — while naming none.
var forged = &cerrors.CompilerError{
	Category: cerrors.CatalogError,
	Source:   cerrors.SourceLocation{File: "recipes/api.yaml", Line: 12},
	Message:  "the recipe is at fault",
}

// Forged hands the envelope out as an error the way a raise path would.
func Forged() error { return forged }
