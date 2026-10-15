// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
)

// TestModuleRootIsTheModuleHoldingThisPackage pins what every in-repository
// fixture path is joined onto: the directory whose module file declares the
// module this package belongs to, holding this package's own source where its
// import path says it is.
func TestModuleRootIsTheModuleHoldingThisPackage(t *testing.T) {
	t.Parallel()

	root := ModuleRoot(t)

	_, modulePath, err := projectionparity.ModuleRoot(root)
	if err != nil {
		t.Fatalf("read the module file at %s: %v", root, err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("ModuleRoot returned %s, which holds no module file: %v", root, err)
	}

	rel, ok := strings.CutPrefix(ownImportPath, modulePath+"/")
	if !ok {
		t.Fatalf("ModuleRoot returned %s, whose module %s does not hold this package (%s)", root, modulePath, ownImportPath)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel), "build.go")); err != nil {
		t.Errorf("ModuleRoot returned %s, but this package's source is not at %s under it: %v", root, rel, err)
	}
}

// TestModuleRootAbove covers the starting points a test can resolve from. A
// working directory deep inside the module still reaches its root, and one
// inside a different module is refused by name rather than answered with that
// module's root.
func TestModuleRootAbove(t *testing.T) {
	t.Parallel()

	root := ModuleRoot(t)

	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "go.mod"), []byte("module example.com/elsewhere\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write the foreign module file: %v", err)
	}
	foreignNested := filepath.Join(foreign, "a", "b")
	if err := os.MkdirAll(foreignNested, 0o755); err != nil {
		t.Fatalf("make a directory inside the foreign module: %v", err)
	}

	cases := []struct {
		name     string
		start    string
		wantRoot string
		wantErr  string
	}{
		{
			name:     "from this module's root",
			start:    root,
			wantRoot: root,
		},
		{
			name:     "from a directory deep inside this module",
			start:    filepath.Join(root, "internal", "wasm", "testdata"),
			wantRoot: root,
		},
		{
			name:    "from a directory inside a different module",
			start:   foreignNested,
			wantErr: "example.com/elsewhere",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := moduleRootAbove(tc.start)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("moduleRootAbove(%s) = %s, want a refusal naming %s", tc.start, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("moduleRootAbove(%s) refused with %q, want it to name %s", tc.start, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("moduleRootAbove(%s): %v", tc.start, err)
			}
			if got != tc.wantRoot {
				t.Errorf("moduleRootAbove(%s) = %s, want %s", tc.start, got, tc.wantRoot)
			}
		})
	}
}
