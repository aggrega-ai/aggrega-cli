// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package smokeprobe is a tiny self-contained probe used by the mutation
// baseline binary's startup smoke check. It carries one tested function and
// one untested function so the smoke check can validate the binary's
// classification mechanism against known-killed and known-survived mutations
// before the main baseline run begins.
//
// The package lives alongside the mutation-baseline binary so the smoke
// targets are stable across refactors of the rest of the codebase: if a
// future change to the production code accidentally drops coverage on a
// previously-tested function (or vice versa), the smoke check is unaffected.
package smokeprobe

// TrueAlways is exercised by TestTrueAlways. Mutating its return literal
// from true to false should be killed by the test.
func TrueAlways() bool {
	return true
}
