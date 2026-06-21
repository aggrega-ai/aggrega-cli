// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// parityCase mirrors one entry of testdata/discovery_parity_cases.json — the
// single shared source of workspace-discovery cases. The extension's discovery
// test materializes the SAME file, so adding a case (or a new marker) here
// updates both languages at once.
type parityCase struct {
	Name  string `json:"name"`
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Dir     bool   `json:"dir"`
	} `json:"files"`
	Start  string  `json:"start"`
	Anchor *string `json:"anchor"`
	Marker string  `json:"marker"`
}

// TestDiscoveryParityCases asserts the Go resolver's discovery matches the
// shared cross-language manifest. The extension runs the same cases through its
// TypeScript discovery; together they prove the two agree. The Go side checks
// the resolved root and marker token; the manifest's anchor (the marker path)
// is what the TS side checks — here it pins the root as the anchor's parent
// directory (or, for no-marker cases, the start directory via CWD fallback).
func TestDiscoveryParityCases(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "discovery_parity_cases.json"))
	if err != nil {
		t.Fatalf("read parity cases: %v", err)
	}
	var doc struct {
		Cases []parityCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse parity cases: %v", err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no parity cases found")
	}

	for _, tc := range doc.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			fixture := t.TempDir()
			for _, f := range tc.Files {
				full := filepath.Join(fixture, f.Path)
				if f.Dir {
					if err := os.MkdirAll(full, 0o755); err != nil {
						t.Fatalf("mkdir %s: %v", full, err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatalf("mkdir parent of %s: %v", full, err)
				}
				if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
					t.Fatalf("write %s: %v", full, err)
				}
			}
			start := filepath.Join(fixture, tc.Start)
			if err := os.MkdirAll(start, 0o755); err != nil {
				t.Fatalf("mkdir start %s: %v", start, err)
			}

			rw, err := workspaceconfig.Resolve(start, "")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got := rw.Marker().String(); got != tc.Marker {
				t.Errorf("marker: got %q; want %q", got, tc.Marker)
			}
			var wantRoot string
			if tc.Anchor == nil {
				// No marker → CWD fallback: the root is the start directory.
				wantRoot = start
			} else {
				// The marker path's parent directory is the resolved root.
				wantRoot = filepath.Dir(filepath.Join(fixture, *tc.Anchor))
			}
			if rw.Root() != wantRoot {
				t.Errorf("root: got %q; want %q", rw.Root(), wantRoot)
			}
		})
	}
}
