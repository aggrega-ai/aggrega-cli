// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package siblingrepo locates a repository checked out beside this module's
// checkout, and tells a repository that is not checked out from one that is
// there and cannot be read.
package siblingrepo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// absentError is the error Dir returns when the repository is not checked out
// beside this one, the one case a test skips on. Its text is the reason alone.
type absentError struct{ reason string }

func (e *absentError) Error() string { return e.reason }

// IsAbsent reports whether err, as [Dir] returned it, says the repository is not
// checked out.
func IsAbsent(err error) bool {
	var absent *absentError
	return errors.As(err, &absent)
}

// Dir returns the directory of the repository repo beside moduleRoot. When it is
// not checked out there — no such directory, or an empty one, which is what a
// declared but uninitialised checkout leaves behind — the error satisfies
// [IsAbsent]. Any other failure to list the directory is returned as it is: the
// path is there and cannot be read, which is not an absent checkout.
func Dir(moduleRoot, repo string) (string, error) {
	dir := filepath.Join(filepath.Dir(moduleRoot), repo)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return dir, &absentError{reason: err.Error()}
	}
	if err != nil {
		return dir, err
	}
	if len(entries) == 0 {
		return dir, &absentError{reason: dir + " is empty"}
	}
	return dir, nil
}
