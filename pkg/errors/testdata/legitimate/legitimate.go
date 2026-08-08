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

var errProbe = cerrors.Register("errors-sealing-probe/legitimate", cerrors.Meta{
	Message: "the probe condition was reached",
})

// Match is the shape a consumer writes: it matches the identity through a wrap
// and reads the code back off the chain.
func Match(err error) (string, bool) {
	if !stdIs(err, ErrProbe()) {
		return "", false
	}
	return cerrors.CodeOf(err)
}
