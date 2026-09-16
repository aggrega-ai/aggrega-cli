// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClosureComputeCallSiteCensus guards the reference-shape precondition
// against the one way it can be silently lost.
//
// Judging a Manifest mapping value's shape must happen before anything walks the
// closure, or the walk reports a recipe that is in the catalog as absent. The
// natural home for that check would be inside the closure walk itself, but the
// import direction forbids it: the catalog package that owns the grammar imports
// the closure package, so the closure package cannot call back into it. The check
// therefore sits at each site that computes a closure — the shared resolver here,
// which every front end that goes through it inherits, and the pull path, which
// computes its own.
//
// That arrangement is only as good as the next author remembering it, so this
// test pins the census: a THIRD site computing a closure fails here, and whoever
// adds it has to decide deliberately whether the shape check goes with it. The
// test is a floor, not a proof — it counts call sites, and cannot tell whether an
// existing one still performs the check.
//
// The census matches the call shape alone — the selector, dot-prefixed, and the
// parenthesis that opens the argument list — and names no argument. Naming one
// would tie this scan to an axis it has no stake in, so that reshaping that axis,
// or merely wrapping a call across lines, would leave the scan matching nothing
// while the call sites it exists to count are all still there.
//
// The dot prefix keeps the declaration in pkg/closure out of the count and means
// an import alias cannot hide a call, since the selector is what precedes the
// parenthesis whatever a caller names the package. Dropping the package qualifier
// is safe because the selector alone is unambiguous: the module declares this name
// once. That is not a standing assumption — a second, unrelated declaration of the
// name, called outside the map below, surfaces here as an unlisted site, which is
// a decision to take deliberately rather than a false positive to suppress.
func TestClosureComputeCallSiteCensus(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// path -> the reason that site is entitled to compute a closure.
	expected := map[string]string{
		"internal/compilerops/compilerops.go": "the shared resolver; judges shape immediately before the walk",
		"internal/pullops/pull.go":            "computes its own closure; carries the check itself",
	}

	found := map[string]int{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "testdata", "node_modules", "dist", "target":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if n := strings.Count(string(b), ".ComputeClosure("); n > 0 {
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = n
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Calibration: a census that found nothing would pass every assertion below
	// for the wrong reason.
	if len(found) == 0 {
		t.Fatal("census found no closure.ComputeClosure call site at all; the scan is broken, not the code")
	}

	for path := range found {
		if _, ok := expected[path]; !ok {
			t.Errorf("%s computes a closure and is not in the census.\n"+
				"A closure walk reports a mis-shaped Manifest mapping value as an entity the catalog "+
				"does not hold. Call resolver.ManifestRecipeReferenceFaults on the composed Manifest"+
				"before the walk, then add this path here with the reason it computes its own closure.", path)
		}
	}
	for path, why := range expected {
		if _, ok := found[path]; !ok {
			t.Errorf("%s no longer computes a closure (%s); drop it from the census", path, why)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/compilerops -> module root
	root := filepath.Join(wd, "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	return root
}
