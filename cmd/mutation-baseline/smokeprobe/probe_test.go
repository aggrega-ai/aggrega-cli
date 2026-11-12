// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package smokeprobe

import "testing"

// TestTrueAlways is the killed-target test in the smoke check. If
// probe.go's TrueAlways gets mutated to return false, this test
// fails — that is exactly the smoke check's expected signal.
func TestTrueAlways(t *testing.T) {
	if !TrueAlways() {
		t.Errorf("TrueAlways() = false; want true")
	}
}
