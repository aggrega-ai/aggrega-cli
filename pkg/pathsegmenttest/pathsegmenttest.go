// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package pathsegmenttest holds the assertion every site that names a file or
// directory after a value it did not choose is held to: each name the site
// produced is one Windows, Linux and macOS all accept, judged by the one
// predicate the spelling is written against (pathsegment.Valid).
//
// It lives here rather than beside each site's test because every such site
// asserts the same property, and a walk or a predicate copied per site would
// agree with its twin on the day it was copied and drift afterwards.
//
// The assertions are made over what a site WROTE or RETURNED — a path on disk, a
// tree it laid out — never over the spelling in isolation, so a site that builds
// a name some other way fails here however sound the spelling itself is.
package pathsegmenttest

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// RequireValidBeneath fails tb for each name in path below root that a platform
// refuses, and fails it when path does not lie below root. root is the part of
// the path the test chose itself — a temporary directory and the fixed directory
// names above the site's own — so only what the site derived is judged.
func RequireValidBeneath(tb testing.TB, root, path string) {
	tb.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		tb.Fatalf("%s does not lie below %s", path, root)
	}
	for _, name := range strings.Split(filepath.ToSlash(rel), "/") {
		if !pathsegment.Valid(name) {
			tb.Errorf("%q, a name in %s, is one a platform refuses", name, path)
		}
	}
}

// RequireTreeValid walks root and fails tb for every file or directory beneath it
// whose name a platform refuses. It returns how many entries it judged, and fails
// tb when there are none, so a site that wrote nothing cannot pass by having
// nothing to judge.
func RequireTreeValid(tb testing.TB, root string) int {
	tb.Helper()
	judged := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		judged++
		if !pathsegment.Valid(d.Name()) {
			tb.Errorf("%q, the name of %s, is one a platform refuses", d.Name(), p)
		}
		return nil
	})
	if err != nil {
		tb.Fatalf("walk %s: %v", root, err)
	}
	if judged == 0 {
		tb.Fatalf("%s holds nothing, so there was no name to judge", root)
	}
	return judged
}
