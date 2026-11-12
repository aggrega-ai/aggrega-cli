// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package envelope_field_write attempts to reshape a diagnostic a constructor
// legitimately built, which would let a caller change what a reader is told
// after the identity decision was made — and, in particular, hang a category on
// an occurrence that names no condition. It must not compile.
package envelope_field_write

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// Repoint takes a diagnostic built through the proper path and overwrites what
// it says.
func Repoint(ce *cerrors.CompilerError) {
	ce.Category = cerrors.DeveloperError
	ce.Message = "something else entirely"
}
