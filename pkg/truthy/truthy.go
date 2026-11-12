// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package truthy owns the single rule that turns a template-rendered string into
// a Go boolean. It is a stdlib-only leaf so every surface that gates on a
// rendered template boolean — schema predicate evaluation and pipeline-step
// gating alike — shares one rule without an import cycle and without a second
// spelling of "what counts as true".
package truthy

import (
	"strconv"
	"strings"
)

// Bool reports whether a template-rendered string is truthy under the shared
// convention for boolean-like template output. It trims surrounding whitespace,
// then applies:
//
//   - "true"/"True"/"TRUE" is true; "false"/"False"/"FALSE" and the empty
//     string are false.
//   - a numeric output is truthy when it is nonzero.
//   - any other non-empty string is truthy.
func Bool(rendered string) bool {
	out := strings.TrimSpace(rendered)
	switch out {
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE", "":
		return false
	}
	if n, err := strconv.ParseFloat(out, 64); err == nil {
		return n != 0
	}
	return true
}
