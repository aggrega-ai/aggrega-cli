// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
)

// heldOpenFiles is what a removal left in place because another process held it
// open: how many files, and the bytes they hold.
type heldOpenFiles struct {
	Files int
	Bytes int64
}

// removeAllLeavingHeldOpen removes path and everything beneath it, as
// os.RemoveAll does, except the files another process holds open on a platform
// that refuses to remove an open file. It reports what it had to leave.
//
// Where nothing refuses that way — which on unix is always, because there an open
// file is removed and its holder keeps reading — this is exactly one os.RemoveAll
// call returning that call's own error, so every outcome there is what it would
// be without this function.
//
// Only when that removal was refused because a file is held open does it look at
// what remains. A removal stops reporting at its first refusal, so the survivors
// are each removed again: a file still held open is counted and left, a file that
// has meanwhile been released goes, and any other refusal is a genuine failure and
// is returned. A directory is then removed unless a file left in place is beneath
// it, in which case it stays because it is not empty.
func removeAllLeavingHeldOpen(path string) (heldOpenFiles, error) {
	err := os.RemoveAll(path)
	if err == nil || !filelock.HeldOpen(err) {
		return heldOpenFiles{}, err
	}

	var held heldOpenFiles
	var heldPaths, dirs []string
	walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		info, infoErr := d.Info()
		rmErr := os.Remove(p)
		switch {
		case rmErr == nil, errors.Is(rmErr, fs.ErrNotExist):
		case filelock.HeldOpen(rmErr):
			held.Files++
			if infoErr == nil {
				held.Bytes += info.Size()
			}
			heldPaths = append(heldPaths, p)
		default:
			return rmErr
		}
		return nil
	})
	if walkErr != nil {
		return held, walkErr
	}

	// The walk visits a directory before what it holds, so the reverse order
	// removes every directory after its contents.
	for i := len(dirs) - 1; i >= 0; i-- {
		rmErr := os.Remove(dirs[i])
		if rmErr == nil || errors.Is(rmErr, fs.ErrNotExist) || holdsAny(dirs[i], heldPaths) {
			continue
		}
		return held, rmErr
	}
	return held, nil
}

// holdsAny reports whether any of paths lies beneath dir. Both sides come from
// one walk, so they share one spelling and a prefix ending in the separator
// decides containment exactly.
func holdsAny(dir string, paths []string) bool {
	prefix := dir + string(filepath.Separator)
	for _, p := range paths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}
