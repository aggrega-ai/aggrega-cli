// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package reassign_accessor attempts to replace a published accessor. It must
// not compile.
package reassign_accessor

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrProbe fires when the probe condition is reached.
func ErrProbe() error { return errProbe }

var errProbe = cerrors.Register("reassign-accessor", cerrors.Meta{
	ShortCode: "E0000",
	Message:   "the probe condition was reached",
})

// Reassigning the accessor would silently stop every match against it, which is
// exactly what publishing the identity through a function rather than a
// variable is meant to prevent.
func Replace() {
	ErrProbe = func() error { return nil }
}
