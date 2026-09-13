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

// RaiseProbe is the shape a raise path writes when the failure names a
// registered condition: the identity the Register call is bound to goes to the
// constructor that requires one.
//
// It is part of the calibration rather than decoration. Every refusal recorded
// under testdata for the envelope's seal is only meaningful if the legitimate
// construction path still compiles, and a control that exercises the identity
// alone would leave the constructors unproven.
func RaiseProbe(nodeID string) error {
	return cerrors.Raise(errProbe, cerrors.CatalogError,
		cerrors.SourceLocation{File: nodeID}, "the probe condition was reached here", nil)
}

// RaiseUnidentified is the shape a raise path writes when the failure names no
// registered condition, and it reads the envelope back through the accessors —
// so the calibration covers the reading side of the seal as well as the writing
// side.
func RaiseUnidentified(nodeID string, cause error) (cerrors.Category, string) {
	ce := cerrors.Unidentified(cerrors.CatalogError,
		cerrors.SourceLocation{File: nodeID}, "no condition names this failure", nil, cause)
	return ce.Category(), ce.Message()
}
