// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package legitimate declares a halting condition exactly the way a consuming
// package does. It is the calibration for the sealing gate: it must compile, so
// that a refusal recorded for any other program under testdata is the sealing
// being enforced rather than the errors package having stopped building.
package legitimate

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrProbe fires when the probe condition is reached.
func ErrProbe() error { return errProbe }

var errProbe = cerrors.Register("legitimate", cerrors.Meta{
	// E0000 is the illustrative short code: short codes are handed out from
	// E0001 upward, so no real condition ever holds it. A fixture is not an
	// allocation and must not read like one.
	ShortCode: "E0000",
	Message:   "the probe condition was reached",
})

// Match is the shape a consumer writes: it matches the identity through a wrap
// and reads the code back off the chain.
func Match(err error) (string, bool) {
	if !stdIs(err, ErrProbe()) {
		return "", false
	}
	return cerrors.CodeOf(err)
}
