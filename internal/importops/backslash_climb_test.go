// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// TestImportRefusesADraftClimbingThroughBackslashes drives the containment
// refusal through the judgement every importer's drafts pass before a write
// places them and before a dry run reports them. A drafted path is joined onto
// the output directory in the host's spelling, and a Windows join reads a
// backslash as a separator, so a path climbing out through backslashes would be
// written outside the output directory there. It is refused on every platform
// with the same condition a climbing slash path is refused with, and nothing is
// written. A backslash between ordinary parts climbs nowhere, passes, and is
// written — the row that keeps the refusal attributable to the climb.
func TestImportRefusesADraftClimbingThroughBackslashes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		path    string
		refused bool
	}{
		{name: "climbs one level", path: `..\out.yaml`, refused: true},
		{name: "climbs two levels", path: `..\..\out.yaml`, refused: true},
		{name: "climbs after descending", path: `a\..\..\out.yaml`, refused: true},
		{name: "climbs only once cleaned", path: `a\b/../..\out.yaml`, refused: true},
		{name: "a backslash between ordinary parts is contained", path: `a\b.yaml`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			files := []abi.ScaffoldFile{{Path: tc.path, Content: "x"}}
			err := checkFiles(out, out, files)
			var escaping *EscapingPathError
			refused := errors.As(err, &escaping)
			if refused != tc.refused {
				t.Fatalf("checkFiles(%q) refused = %v (err %v); want refused = %v", tc.path, refused, err, tc.refused)
			}
			if !tc.refused {
				if err != nil {
					t.Fatalf("checkFiles(%q): %v", tc.path, err)
				}
				if _, err := writeFiles(out, files); err != nil {
					t.Fatalf("writeFiles(%q): %v", tc.path, err)
				}
				if _, statErr := os.Stat(filepath.Join(out, filepath.FromSlash(tc.path))); statErr != nil {
					t.Fatalf("the contained draft %q was not written: %v", tc.path, statErr)
				}
				return
			}
			if escaping.Offending() != tc.path {
				t.Errorf("Offending() = %q; want the path the importer returned, %q", escaping.Offending(), tc.path)
			}
			inside, _ := os.ReadDir(out)
			if len(inside) != 0 {
				t.Errorf("a refused draft must write nothing; the output directory holds %d entry/entries", len(inside))
			}
			// The directory holding the output directory is this test's own, so a
			// file there can only be the one climbing a level.
			p := filepath.Join(filepath.Dir(out), "out.yaml")
			if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
				t.Errorf("%s exists after the draft was refused (stat err = %v)", p, statErr)
			}
		})
	}
}
