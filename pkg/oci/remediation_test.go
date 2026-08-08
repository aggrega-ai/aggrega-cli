// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"fmt"
	"strings"
	"testing"
)

// TestRemediationFor_Unauthorized guards the user-facing remediation string for
// an unauthorized registry — the message a developer sees when a pull is refused.
// The networked auth-refuse smoke proves the refusal end to end, but oras
// short-circuits an in-process Basic challenge before the round-trip, so this
// unit test is what keeps the docker-login remediation (and its host
// interpolation) from regressing in CI without a live registry.
func TestRemediationFor_Unauthorized(t *testing.T) {
	host := "registry.example.com:5000"
	got := RemediationFor(fmt.Errorf("pull failed: %w", ErrFetchUnauthorized()), host)
	if !strings.Contains(got, "docker login "+host) {
		t.Errorf("unauthorized remediation = %q; want it to contain %q", got, "docker login "+host)
	}

	// Empty host falls back to a placeholder rather than an empty command.
	if blank := RemediationFor(ErrFetchUnauthorized(), ""); !strings.Contains(blank, "docker login <registry>") {
		t.Errorf("empty-host remediation = %q; want the <registry> placeholder", blank)
	}

	// A non-sentinel error yields no remediation.
	if none := RemediationFor(fmt.Errorf("some transport blip"), host); none != "" {
		t.Errorf("non-sentinel remediation = %q; want empty", none)
	}
}
