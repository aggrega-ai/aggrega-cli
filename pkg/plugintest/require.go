// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package plugintest

import "testing"

// RequireValid fails t when bytes do not validate against declaredType. It is
// test sugar over ValidateGuest for the common binary-only case: a plugin
// author drops a built .wasm and its socket type into a Go test and gets the
// same gate the publish flow applies. It marks itself a test helper so a
// failure is reported at the caller's line.
func RequireValid(t testing.TB, bytes []byte, declaredType string) {
	t.Helper()
	if _, err := ValidateGuest(bytes, Options{DeclaredType: declaredType, Identity: declaredType}); err != nil {
		t.Fatalf("plugintest: guest declared type %q did not validate: %v", declaredType, err)
	}
}
