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
// walk does not descend; the pinned-axis descent and the workspace-socket
// reconstruction rebuild the axes ComputeClosure leaves empty. A surface that runs
// one half and not the other produces a closure that is neither the compute-only
// one nor the build's, and a drift verify against it reports entries that are not
// drift.
//
// Each form is the call shape and nothing else: the selector, dot-prefixed, and
// the parenthesis that opens the argument list. Both halves earn their place, and
// so does what the form deliberately leaves out.
//
// The dot prefix keeps the declarations in pkg/closure out of the census and means
// an import alias cannot hide a call — whatever a caller names the package, the
// selector is what precedes the parenthesis. The trailing parenthesis holds the
// match to call syntax rather than a bare mention, which is the coverage bound
// declared on the test below.
//
// No argument is named, and that omission is what keeps a form within reach of the
// thing it guards. The subject here is WHICH FILES reconstruct a closure — a
// property of the call, not of anything passed to it. A form that also pinned an
// argument would couple this guard to an axis it has no stake in, such as how a
// cancellation signal is obtained, and reshaping that axis anywhere in the module
// would stop the census matching. Two costs follow, and the second is the
// dangerous one. At a listed home the want-side check below still fails, but it
// reports that the home moved and so invites an edit to this map — a repair to the
// wrong thing. At an unlisted home there is nothing to fail: a surface that
// reconstructs a closure outside the shared computation is simply not seen, which
// is the one direction a guard must not fail in.
//
// Line wrapping is a second, independent way an argument-bearing form loses reach.
// Pinning an argument means pinning the separator after it, and a call wrapped
// across lines puts a newline where such a form demands a space — so the same call,
// reformatted and otherwise untouched, stops matching.
//
// Rejected, recorded so it is not re-proposed: censusing on the cancellation
// argument the sites share. It reads as the more precise pattern and is the same
// defect one signature change later, because it again names something this guard
// has no stake in; the next parameter rename or wrapper puts the census out of
// reach with nothing reporting why.
//
// The map is the specification, not a record of what happens to be there. A new
// entry is a deliberate decision that a further surface may reconstruct a
// closure, taken by editing this file — which is the point: the edit is what puts
// the decision in front of a reviewer.
var hermeticReconstructionHomes = map[string][]string{
	// The pinned-axis descent has three homes, and they are one computation run over
	// two lookups rather than a duplicate: the pull descends the axes it is about to
	// pin over its reconciling lookup, and the build descends the same axes over its
	// hermetic one to hold the lockfile to them. Everything that differs between the
	// callers is an argument of the call, so each further caller is a further place
	// that could pass a different one.
	//
	// The third is a validation, and it reconstructs nothing. It descends over a
	// copy of the entity set with no loader for either plugin pass, only so that a
	// reference a module makes is resolved — and refused as the author's fault when
	// nothing resolves it — before the checks that assume every reference resolved;
	// the copy is discarded, so the closure the validation reports on is the one the
	// walk computed.
	".DescendPinnedAxes(": {
		"internal/compilerops/preview.go",
		"internal/compilerops/validate.go",
		"internal/pullops/pull.go",
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
// onto its hermetic footing, and that its pinned axes are descended, only where
// this file says either may be.
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
// That second axis is also what calibrates the scan, and it calibrates it only for
// a form that names at least one home. A form naming none has nothing on the want
// side to go missing: it reports the same clean verdict whether its pattern still
// matches the call or has never matched anything in the module, which makes it
// unfalsifiable — the one property a guard may not have. The two ways a list can be
// empty are not equally silent, and the difference is what decides where the check
// goes. Emptying a LISTED form's homes is caught by the comparison below, because
// the call sites are still there to be reported as unlisted. A form that names no
// home and matches nothing passes clean. The silent state is therefore exactly the
// state a no-home form is meant to be in, so the check has to be on the declaration
// rather than on the result.
//
// The loop below refuses such a form instead of censusing it. That arm's job is to
// hold the precondition under which the want-side comparison means anything, not to
// add a second calibration beside it: once every form names a home, a scan that
// stopped reaching the module fails on every form at once.
//
// The consequence, recorded so it is not read as an oversight: this map cannot
// express a prohibition — "no non-test file may perform this call". Such a form has
// by construction no non-test occurrence to calibrate against, so holding one
// honestly needs a positive control this map has nowhere to put. Declare a
// prohibition somewhere that carries one; do not empty a list here to mean it.
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

		if len(want) == 0 {
			t.Errorf(""+
				"%s is declared in hermeticReconstructionHomes and names no home.\n"+
				"A form with no home reports the same clean verdict whether its pattern still matches the call or matches nothing at all, so it guards nothing and a typo in it is indistinguishable from a correct one.\n"+
				"List the files permitted to perform the call, or drop the form.",
				form)
			continue
		}

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
