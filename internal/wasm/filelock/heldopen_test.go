// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package filelock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestHeldOpenNamesTheRefusalToRemoveAnOpenFile drives the one situation
// HeldOpen exists for — removing a file while another handle has it open — and
// asserts each platform's own answer: Windows refuses the removal and HeldOpen
// recognises that refusal; everywhere else the removal succeeds, so there is no
// refusal to recognise.
//
// The open handle is this process's own. That shows the same refusal another
// process's handle would, because the refusal is a property of how the handle
// was opened, not of who holds it; the handle is opened exactly the way the
// standard library opens every file a reader reads.
func TestHeldOpenNamesTheRefusalToRemoveAnOpenFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "entry")
	if err := os.WriteFile(p, []byte("bytes another process is reading"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	reader, err := os.Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	rmErr := os.Remove(p)
	if runtime.GOOS == "windows" {
		if rmErr == nil {
			t.Fatal("removing a file another handle holds open succeeded; this platform was expected to refuse it")
		}
		if !HeldOpen(rmErr) {
			t.Fatalf("the refusal to remove an open file is not recognised as one: %v (%#v)", rmErr, errors.Unwrap(rmErr))
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := os.Remove(p); err != nil {
			t.Fatalf("the file could not be removed after its last handle closed: %v", err)
		}
	} else {
		if rmErr != nil {
			t.Fatalf("removing a file another handle holds open failed here, where it should succeed: %v", rmErr)
		}
		if HeldOpen(rmErr) {
			t.Fatal("HeldOpen recognised a successful removal as a refusal")
		}
	}

	// The refusals that are not about an open file stay what they are, so a
	// caller asking this question never mistakes a genuine failure for one.
	_, missingErr := os.Stat(filepath.Join(t.TempDir(), "absent"))
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"not exist", fs.ErrNotExist},
		{"permission", fs.ErrPermission},
		{"a real missing path", missingErr},
	} {
		if HeldOpen(tc.err) {
			t.Errorf("HeldOpen recognised %s (%v) as a file held open", tc.name, tc.err)
		}
	}
}
