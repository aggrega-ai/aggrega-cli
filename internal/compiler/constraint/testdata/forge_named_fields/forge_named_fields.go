// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package forgenamedfields must NOT compile. It attempts to build an objection
// by naming the fields, which is the most direct way a function outside the
// framework would try to halt a build without a subject.
package forgenamedfields

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

var errForged = errors.New("forged")

// Forged names the unexported fields of a type from another package.
var Forged = constraint.Objection{
	sentinel: errForged,
	file:     "aggregate.yaml",
	locator:  ".spec.components.web",
}
