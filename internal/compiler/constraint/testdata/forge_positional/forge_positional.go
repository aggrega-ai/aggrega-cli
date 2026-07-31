// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package forgepositional must NOT compile. Naming the fields fails, so this is
// the next attempt: a positional composite literal, which needs no field names
// at all.
package forgepositional

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

var errForged = errors.New("forged")

// Forged assigns to unexported fields positionally.
var Forged = constraint.Objection{errForged, "aggregate.yaml", ".spec.components.web", nil}
