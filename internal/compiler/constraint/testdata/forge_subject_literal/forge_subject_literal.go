// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package forgesubjectliteral must NOT compile. Since an objection can only
// come from a subject, this attempts to conjure the subject instead — the
// zero-value shortcut that works for any exported struct.
//
// It fails because a subject kind is an interface, so there is no zero value to
// take: the only value of one an author outside the framework can name is nil,
// which raises nothing.
package forgesubjectliteral

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

// Conjured takes the zero value of a subject kind.
var Conjured = constraint.DocumentSubject{}
