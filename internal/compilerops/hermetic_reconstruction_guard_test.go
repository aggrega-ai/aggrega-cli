// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// hermeticReconstructionHomes maps each call form that brings a resolved closure
// onto its hermetic footing to the complete set of non-test files permitted to
// use it, relative to the module root.
//
// Three forms rather than one, because the footing has two halves that are only
// correct together. The library fold resolves the cross-document axes the entity
// walk does not descend; the two reconstruction calls rebuild the axes
// ComputeClosure leaves empty. A surface that runs one half and not the other
// produces a closure that is neither the compute-only one nor the build's, and a
// drift verify against it reports entries that are not drift.
//
// Each form is searched dot-prefixed, so the declarations in pkg/closure are not
// matches and an import alias cannot hide a call — whatever a caller names the
// package, the selector is what precedes the parenthesis.
//
// The map is the specification, not a record of what happens to be there. A new
// entry is a deliberate decision that a further surface may reconstruct a
// closure, taken by editing this file — which is the point: the edit is what puts
// the decision in front of a reviewer.
var hermeticReconstructionHomes = map[string][]string{
	".ReconstructClosureAxes(": {
		"internal/compilerops/preview.go",
	},
	".ReconstructWorkspaceSocketEntities(": {
		"internal/compilerops/preview.go",
	},
	// The fold has two homes and they are different operations rather than a
	// duplicate: the lock-time descent folds the axes it is about to pin, and the
	// resolution folds them for a caller that asked for an augmented closure.
	".AugmentClosure(": {
		"internal/compilerops/compilerops.go",
		"internal/pullops/pull.go",
	},
}

// TestHermeticClosureReconstructionHasOneHome asserts that a closure is brought
// onto its hermetic footing only where this file says it may be.
//
// The failure it exists to catch produces no symptom at the call site. A surface
// that assembles the fold and the two descents for itself gets a closure — so
// nothing errors, nothing warns, and the code reads as though it went through the
// shared computation. What differs is what the closure CONTAINS, and the
// difference only shows through a lockfile: an axis the shared path reconstructs
// and a private copy does not is reported as "no longer used" against a lockfile
// that pins it correctly, so the surface publishes drift that is not drift. The
// reverse is quieter still — the two agreeing today is exactly the condition
// under which nothing reports the day one of them gains a step.
//
// The check is exact on both axes. An unlisted file means a surface reconstructed
// a closure outside the shared computation. A LISTED file that no longer appears
// means the home moved and this guard did not follow it — equally a failure,
// because a guard whose corpus silently stopped containing what it guards reports
// a clean verdict over nothing at all.
//
// 🔴 COVERAGE BOUND. This matches CALL SYNTAX — a textual selector in call
// position. A call reached through a function VALUE (a variable, a struct field, a
// dispatch-table entry, an argument), through an interface method, or through
// reflection is invisible to it, because none of those name the function at the
// call site. So this guard bounds the direct route and no other; it is not, and
// cannot be, a proof that nothing else reconstructs a closure.
//
// Test files are exempt, deliberately. A test legitimately drives one descent in
// isolation to compare it against the lock-time path, which is a comparison it
// could not make through a surface that runs the whole sequence.
func TestHermeticClosureReconstructionHasOneHome(t *testing.T) {
	moduleRoot := moduleRootFromTestDir(t)
	if _, err := os.Stat(filepath.Join(moduleRoot, "go.mod")); err != nil {
		t.Fatalf("module root %q carries no go.mod; the guard is looking in the wrong place, so a clean result here would mean nothing: %v", moduleRoot, err)
	}

	// form -> file -> occurrence count.
	found := map[string]map[string]int{}
	for form := range hermeticReconstructionHomes {
		found[form] = map[string]int{}
	}

	walkErr := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "node_modules", "target", ".git", "dist":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(moduleRoot, path)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		for form := range hermeticReconstructionHomes {
			if n := strings.Count(string(body), form); n > 0 {
				found[form][slash] += n
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk module: %v", walkErr)
	}

	for _, form := range sortedFormKeys(hermeticReconstructionHomes) {
		want := append([]string(nil), hermeticReconstructionHomes[form]...)
		sort.Strings(want)

		got := make([]string, 0, len(found[form]))
		for file := range found[form] {
			got = append(got, file)
		}
		sort.Strings(got)

		if !equalStringSlices(want, got) {
			t.Errorf(""+
				"%s must appear only in the files declared in hermeticReconstructionHomes.\n"+
				"  want: %v\n"+
				"  got:  %v\n"+
				"A file present in got and absent from want reconstructs a closure outside the shared computation: route it through compilerops instead.\n"+
				"A file present in want and absent from got means the home moved and this guard did not follow it: update hermeticReconstructionHomes in the same change.",
				form, want, got)
		}
	}
}

func sortedFormKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
