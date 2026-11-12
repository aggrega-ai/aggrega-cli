// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsersocket_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
)

// TestFormatForExtension pins the fixed extension→format fallback: every mapped
// extension resolves to its format, the compound .tf.json wins over the shorter
// suffixes it contains, and an unmapped extension yields no format (the host
// never guesses).
func TestFormatForExtension(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path       string
		wantFormat string
		wantOK     bool
	}{
		{"main.tf", "hcl", true},
		{"main.tf.json", "json", true},
		{"config.toml", "toml", true},
		{"values.yaml", "yaml", true},
		{"values.yml", "yaml", true},
		{"data.json", "json", true},
		{"README.md", "", false},
		{"noextension", "", false},
		{"archive.tar.gz", "", false},
	}
	for _, tc := range cases {
		gotFormat, gotOK := parsersocket.FormatForExtension(tc.path)
		if gotFormat != tc.wantFormat || gotOK != tc.wantOK {
			t.Errorf("FormatForExtension(%q) = (%q, %v); want (%q, %v)",
				tc.path, gotFormat, gotOK, tc.wantFormat, tc.wantOK)
		}
	}
}

// TestSelectFormat pins the two declarative selection paths and their
// precedence: an explicit format wins outright (even against a path whose
// extension maps elsewhere); an empty explicit format falls back to the
// extension map; and neither yielding a format is a clean miss the caller
// resolves.
func TestSelectFormat(t *testing.T) {
	t.Parallel()

	// Explicit wins, even over a conflicting extension.
	if got, ok := parsersocket.SelectFormat("hcl", "values.yaml"); !ok || got != "hcl" {
		t.Errorf("explicit format: got (%q, %v); want (hcl, true)", got, ok)
	}

	// No explicit format → extension fallback.
	if got, ok := parsersocket.SelectFormat("", "main.tf"); !ok || got != "hcl" {
		t.Errorf("extension fallback: got (%q, %v); want (hcl, true)", got, ok)
	}

	// Neither path yields a format.
	if got, ok := parsersocket.SelectFormat("", "README.md"); ok || got != "" {
		t.Errorf("no format: got (%q, %v); want (\"\", false)", got, ok)
	}
}
