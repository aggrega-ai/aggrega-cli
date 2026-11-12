// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package smokeprobe

// UncoveredFunc is deliberately not exercised by any test in this
// package. Mutating its return literal from false to true should
// survive — the smoke check uses this surviving outcome to
// validate the binary's "survived" classification.
//
// Do not add a test against this function; doing so defeats the
// smoke check's "known-survived" target.
func UncoveredFunc() bool {
	return false
}
