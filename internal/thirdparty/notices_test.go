// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package thirdparty

import (
	"strings"
	"testing"
)

// TestNoticesNamesEmbeddedDependencies holds the embedded text to naming a Rust
// guest dependency, a Go dependency and a guest's own line.
func TestNoticesNamesEmbeddedDependencies(t *testing.T) {
	got := Notices()
	if got == "" {
		t.Fatal("Notices() is empty")
	}
	for _, want := range []string{"regorus", "github.com/hashicorp/hcl/v2"} {
		if !strings.Contains(got, want) {
			t.Errorf("Notices() does not contain %q", want)
		}
	}
	hasCel := false
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "cel ") {
			hasCel = true
		}
	}
	if !hasCel {
		t.Error("Notices() has no line naming the cel crate")
	}
}
