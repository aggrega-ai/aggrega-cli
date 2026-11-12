// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package editops

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/windowshost"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// TestTheEditedDocumentIsPlacedByTheOneWriteJudgement holds the document an edit
// reads and writes back to the judgement every write of a name takes: a document
// whose path holds a segment Windows cannot hold is refused with the one condition
// where the process runs on Windows, before anything is read, and resolved as it
// always was anywhere else; a path climbing out of the workspace is refused as
// before on both.
//
// Not parallel: it states the platform the process answers it runs on.
func TestTheEditedDocumentIsPlacedByTheOneWriteJudgement(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"aux.yaml", "releases/nul/release.yaml", filepath.Join(root, "report.")} {
		t.Run(file, func(t *testing.T) {
			t.Cleanup(windowshost.SetForTest(true))
			if _, _, err := resolveInside(root, file); !errors.Is(err, pathscope.ErrNameWindowsCannotHold()) {
				t.Fatalf("on Windows resolveInside(%q) = %v; want the halt naming a name Windows cannot hold", file, err)
			}
			windowshost.SetForTest(false)
			abs, rel, err := resolveInside(root, file)
			if err != nil {
				t.Fatalf("off Windows resolveInside(%q) refused a document it always resolved: %v", file, err)
			}
			wantRel := filepath.ToSlash(file)
			if filepath.IsAbs(file) {
				wantRel = "report."
			}
			if rel != wantRel || abs != filepath.Join(root, filepath.FromSlash(wantRel)) {
				t.Errorf("off Windows resolveInside(%q) = (%q, %q); want (%q, %q)", file, abs, rel,
					filepath.Join(root, filepath.FromSlash(wantRel)), wantRel)
			}
		})
	}
	for _, windows := range []bool{false, true} {
		t.Cleanup(windowshost.SetForTest(windows))
		if _, _, err := resolveInside(root, "../nul.yaml"); !errors.Is(err, errFileOutsideWorkspace) {
			t.Errorf("windows=%v: resolveInside of a path climbing out refused with %v; want the refusal naming "+
				"a path outside the workspace", windows, err)
		}
	}
}
