// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package thirdparty

import (
	"regexp"
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
	// A dependency block opens with its own "<name> <version>" line.
	if !regexp.MustCompile(`(?m)^cel [0-9]+\.[0-9]+\.[0-9]+\S*$`).MatchString(got) {
		t.Error("Notices() has no dependency block named cel")
	}
}
