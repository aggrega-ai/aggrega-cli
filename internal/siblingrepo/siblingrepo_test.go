// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package siblingrepo

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestDirTellsAPresentCheckoutFromAMissingOne lays a module checkout beside a
// populated companion repository, an empty directory (what a declared but
// uninitialised checkout leaves) and no directory at all, and checks that only
// the populated one is present.
//
// 🔴 PLANTED DEFECT: in Dir (siblingrepo.go), drop the empty-directory check:
// the empty directory reads as a present checkout, and the case fails on it.
func TestDirTellsAPresentCheckoutFromAMissingOne(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	for _, dir := range []string{moduleRoot, filepath.Join(parent, "populated"), filepath.Join(parent, "empty")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(parent, "populated", "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("populate: %v", err)
	}

	if dir, err := Dir(moduleRoot, "populated"); err != nil || dir != filepath.Join(parent, "populated") {
		t.Errorf("Dir(populated) = (%s, %v), want (%s, nil)", dir, err, filepath.Join(parent, "populated"))
	}
	for _, repo := range []string{"empty", "absent"} {
		_, err := Dir(moduleRoot, repo)
		if err == nil {
			t.Errorf("Dir(%s) found a checkout; want it missing", repo)
			continue
		}
		if !IsAbsent(err) {
			t.Errorf("Dir(%s) = %v; want an absent checkout", repo, err)
		}
	}
}

// TestDirOverAFileIsNotAnAbsentCheckout lays a regular file where a companion
// repository's checkout would be and checks that Dir returns the not-a-directory
// error rather than an absent checkout.
//
// 🔴 PLANTED DEFECT: in Dir (siblingrepo.go), return every listing error as an
// *absentError: the file reads as an absent checkout, and the case fails on it.
func TestDirOverAFileIsNotAnAbsentCheckout(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", moduleRoot, err)
	}
	if err := os.WriteFile(filepath.Join(parent, "hub"), []byte("not a checkout"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := Dir(moduleRoot, "hub")
	if !errors.Is(err, syscall.ENOTDIR) || IsAbsent(err) {
		t.Fatalf("Dir over a file = %v; want a not-a-directory error that is not an absent checkout", err)
	}
}
