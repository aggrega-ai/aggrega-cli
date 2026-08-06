// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// logMagic is the first line of every test log the toolchain writes.
//
// It is checked rather than skipped. A log that does not start with it is a
// truncated, overwritten or empty file, and reading such a file as "this package
// read nothing" is the one failure this census must never make: it would report
// a confident zero for a package it did not actually measure.
const logMagic = "# test log"

// errNoMagic reports a file that is not a test log.
var errNoMagic = errors.New("not a test log: the leading marker line is missing")

// classified is what one test package's log says about where its inputs live.
//
// The two escaping populations are kept apart, and the split is the census's one
// substantive judgement, so it is argued here rather than left implicit.
//
// SharedTree holds inputs outside the module root but still inside the
// repository tree: a committed file in a sibling module or at the tree root,
// read across a module boundary. Those are the inputs the exposure is about.
// They are versioned, they change when the tree changes, and the toolchain keeps
// them out of the cache key — so a cached pass genuinely can be a pass for bytes
// nothing looked at.
//
// OutsideTree counts inputs outside the repository tree altogether, and those
// are NOT the same class. Every one observed is one of four things: a directory
// the test itself created for this run and read back; the toolchain or the
// operating system; a lookup along the executable search path; or a path a
// negative test deliberately points at nothing. None of them is a committed
// input that could have changed since the last run, so none can produce the
// stale pass being watched for. Counting them would also make the number grow
// every time somebody writes a test that works in a fresh temporary directory,
// which is the hermetic shape tests are meant to have — so the count would
// penalise the practice it should encourage. It is reported rather than ignored,
// so no reader has to take that argument on trust.
type classified struct {
	// SharedTree holds the resolved absolute inputs outside the module root but
	// inside the tree, de-duplicated and sorted.
	SharedTree []string
	// OutsideTree counts the distinct resolved inputs outside the tree.
	OutsideTree int
	// Inputs counts every input entry the log records, escaping or not, so a
	// report can say what fraction escaped rather than only how many did.
	Inputs int
}

// classify reads the test log at logPath and reports which of the inputs it
// records lie outside moduleRoot.
//
// The walk is a transcription of how the toolchain itself reads the same file
// when it decides whether a cached test result is still valid, and that is the
// whole reason this census reads a test log instead of scanning source:
//
//   - the vocabulary is exactly getenv, chdir, stat and open, and any other verb
//     is a malformed log rather than something to skip — a future toolchain that
//     records a new kind of input must make this census fail loudly instead of
//     silently under-counting;
//   - the working directory starts at the package directory and a chdir entry
//     rebases it, so a test that changes directory changes what its later
//     relative entries mean;
//   - a relative name is joined onto that working directory LEXICALLY, so ".."
//     segments cancel path elements textually and no symbolic link is followed
//     at this stage;
//   - an input is inside the module when the joined path lies inside the module
//     root, which is the same test the toolchain uses to decide which inputs it
//     may leave out of a cache key.
//
// Because the log records the file the binary actually opened, a path assembled
// at run time — concatenated, computed, or read from a constant declared
// elsewhere — is recorded exactly like a literal one. That is the class a source
// scan structurally cannot see.
//
// treeRoot is the repository tree the module sits in, and it decides which of the
// two escaping populations an input falls into; see classified.
func classify(logPath, pkgDir, moduleRoot, treeRoot string) (classified, error) {
	var out classified

	if !filepath.IsAbs(pkgDir) || !filepath.IsAbs(moduleRoot) || !filepath.IsAbs(treeRoot) {
		return out, fmt.Errorf("package directory, module root and tree root must be absolute, got %q, %q and %q", pkgDir, moduleRoot, treeRoot)
	}
	if !hasFilePathPrefix(filepath.Clean(moduleRoot), filepath.Clean(treeRoot)) {
		// Without this the two populations would silently merge: every input
		// outside the module would also be outside the tree, and the gated
		// count would collapse to zero while still reading as a measurement.
		return out, fmt.Errorf("module root %q is not inside tree root %q, so the two escaping populations cannot be told apart", moduleRoot, treeRoot)
	}

	f, err := os.Open(logPath)
	if err != nil {
		return out, err
	}
	defer f.Close()

	seen := make(map[string]bool)
	pwd := filepath.Clean(pkgDir)
	root := filepath.Clean(moduleRoot)
	tree := filepath.Clean(treeRoot)

	sc := bufio.NewScanner(f)
	// A single log line is a path, so the default 64 KiB token limit is ample,
	// but a pathological line must fail loudly rather than truncate a path into
	// a different one.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if line != logMagic {
				return out, errNoMagic
			}
			continue
		}
		if line == "" {
			continue
		}
		op, name, found := strings.Cut(line, " ")
		if !found {
			return out, fmt.Errorf("malformed test log entry %q in %s", line, logPath)
		}
		switch op {
		case "getenv":
			// An environment variable is an input, but it is not a file and so
			// has no location to be inside or outside the module.
		case "chdir":
			// The toolchain documents this name as always absolute; it is
			// cleaned rather than trusted so a later join cannot go wrong.
			pwd = filepath.Clean(name)
		case "stat", "open":
			out.Inputs++
			resolved := name
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(pwd, resolved)
			}
			if inDir(resolved, root) {
				continue
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			if inDir(resolved, tree) {
				out.SharedTree = append(out.SharedTree, resolved)
			} else {
				out.OutsideTree++
			}
		default:
			return out, fmt.Errorf("unknown test log verb %q in %s: this census must be taught what it records before its count can be trusted", op, logPath)
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	if first {
		return out, errNoMagic
	}
	// Sorted, not in the order the log recorded them. Parallel tests reach their
	// reads in whatever order they happen to run, so first-occurrence order varies
	// between runs of an unchanged tree, and a report that names a different
	// example each time reads as though something moved when nothing did.
	sort.Strings(out.SharedTree)
	return out, nil
}

// inDir reports whether path lies inside dir, by the same rule the toolchain
// applies to decide whether an input is inside the module root: a lexical,
// component-wise prefix test, retried with symbolic links resolved on either
// side. The retries matter for the census's honesty in one direction only —
// without them a path reached through a link would be reported as escaping when
// the toolchain considers it inside, inflating the count.
func inDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	if hasFilePathPrefix(path, dir) {
		return true
	}
	xpath, err := filepath.EvalSymlinks(path)
	if err != nil || xpath == path {
		xpath = ""
	} else if hasFilePathPrefix(xpath, dir) {
		return true
	}
	if xdir, err := filepath.EvalSymlinks(dir); err == nil && xdir != dir {
		if hasFilePathPrefix(path, xdir) {
			return true
		}
		if xpath != "" && hasFilePathPrefix(xpath, xdir) {
			return true
		}
	}
	return false
}

// hasFilePathPrefix reports whether s begins with prefix on a path-component
// boundary. Equal paths count as inside, matching the toolchain, which treats a
// read of the module root directory itself as an in-module read.
func hasFilePathPrefix(s, prefix string) bool {
	sv := filepath.VolumeName(s)
	pv := filepath.VolumeName(prefix)
	s = s[len(sv):]
	prefix = prefix[len(pv):]
	if sv != pv {
		if !strings.EqualFold(sv, pv) {
			return false
		}
	}
	switch {
	case len(s) == len(prefix):
		return s == prefix
	case len(s) > len(prefix):
		if prefix != "" && prefix[len(prefix)-1] == filepath.Separator {
			return strings.HasPrefix(s, prefix)
		}
		return s[len(prefix)] == filepath.Separator && s[:len(prefix)] == prefix
	default:
		return false
	}
}
