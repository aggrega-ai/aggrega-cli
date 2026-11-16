// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckOutputDirNamesWhereTheTargetLives holds the refusal for a missing
// output directory to the place that directory lies: inside this module, or
// outside it, where codec targets go into the aggrega-abi checkout.
func TestCheckOutputDirNamesWhereTheTargetLives(t *testing.T) {
	if err := checkOutputDir(filepath.Join(t.TempDir(), "present.go")); err != nil {
		t.Fatalf("an existing directory was refused: %v", err)
	}

	inside := filepath.Join("no-such-directory", "target_gen.go")
	err := checkOutputDir(inside)
	if err == nil {
		t.Fatalf("a missing directory inside the module was accepted")
	}
	if msg := err.Error(); !strings.Contains(msg, "inside this module") || strings.Contains(msg, "aggrega-abi") {
		t.Fatalf("a missing directory inside the module was worded as %q", msg)
	}

	outside := filepath.Join(t.TempDir(), "aggrega-abi", "rust", "src", "target_gen.rs")
	err = checkOutputDir(outside)
	if err == nil {
		t.Fatalf("a missing directory outside the module was accepted")
	}
	if msg := err.Error(); !strings.Contains(msg, "outside this module") || !strings.Contains(msg, "check out aggrega-abi") {
		t.Fatalf("a missing directory outside the module was worded as %q", msg)
	}

	for _, p := range []string{inside, outside} {
		if msg := checkOutputDir(p).Error(); strings.HasPrefix(msg, "typegen:") {
			t.Fatalf("the refusal opens with the generator's package name: %q", msg)
		}
	}
}
