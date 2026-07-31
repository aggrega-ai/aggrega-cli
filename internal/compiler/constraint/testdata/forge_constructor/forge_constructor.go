// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package forgeconstructor must NOT compile. It reaches for the constructor an
// author would expect a value type to have, and pins that the framework exposes
// none: the accessors are exported so a caller can read an objection, and
// nothing exported can build one.
package forgeconstructor

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

var errForged = errors.New("forged")

// Forged calls a constructor that must not exist.
var Forged = constraint.NewObjection(errForged, "aggregate.yaml", ".spec.components.web")
