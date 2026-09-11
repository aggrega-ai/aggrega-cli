// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// externalInputPrimitiveCallForm is the primitive constructor in its
// package-qualified call form — the only form code outside the store package
// can write it in, since in-package code needs no qualifier. That is also why
// this guard needs no allowlist of permitted files: the form it searches for is
// unwritable inside the package that declares it.
const externalInputPrimitiveCallForm = "externalinputs.NewService("

// externalInputPrimitiveDecl and externalInputCompositionHome are the census's
// calibration. The declaration proves the walk reached the declaring file and
// that the primitive still carries that name; the composition home proves the
// walk reached the file holding the composed postures. Without both, a walk
// that read the wrong tree — a moved module root, a renamed constructor — would
// report a clean chokepoint over a corpus it never opened.
// The composition home is required to construct the primitive at least once
// rather than a fixed number of times, deliberately: a count of the postures
// sitting here would be a second copy of what that file already states, and
// adding a third posture would then fail this guard with a message about a
// missing composition home. At least one construction is what the calibration
// actually needs — it establishes that the file was read and that it is where
// the primitive is reached.
const (
	externalInputPrimitiveDecl     = "func NewService("
	externalInputCompositionHome   = "pkg/externalinputs/composition.go"
	externalInputPrimitiveDeclFile = "pkg/externalinputs/externalinputs.go"
)

// TestExternalInputServiceHasOneCompositionHome keeps the external-input
// service's COMPOSITION single-sourced, which is a different property from the
// primitive being single-sourced — the primitive always was.
//
// Composing a service means deciding three things at once: whether an unpinned
// reference may be acquired, whether there is a transport to acquire it with,
// and what the pinned view is read from. A site that decides them itself
// produces a service that works, so nothing fails and nothing warns; it is the
// next edge case that separates the copies, and by then two postures that agreed
// on the day they were written disagree about whether a reference is fetched,
// refused, or served from cache. The agreement is precisely what hides it, so
// the guard has to hold before the divergence exists rather than after.
//
// Proving that today's composed forms are correct would say nothing about a
// fourth surface added later, which is why this guards the CALL rather than the
// surfaces: a surface that needs a service asks for a named posture, and the
// postures are composed in one place.
//
// Test files are deliberately out of scope. A test legitimately constructs a
// service from arbitrary combinations — that is what the primitive stays
// exported for — and holding a test to the composed postures would remove
// coverage rather than protect anything.
func TestExternalInputServiceHasOneCompositionHome(t *testing.T) {
	moduleRoot := moduleRootFromTestDir(t)

	found := map[string]int{}
	sawDecl := false
	sawCompositionHome := 0

	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
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
		text := string(body)

		if slash == externalInputPrimitiveDeclFile && strings.Contains(text, externalInputPrimitiveDecl) {
			sawDecl = true
		}
		if slash == externalInputCompositionHome {
			sawCompositionHome = strings.Count(text, "NewService(")
		}
		if n := strings.Count(text, externalInputPrimitiveCallForm); n > 0 {
			found[slash] += n
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if !sawDecl {
		t.Fatalf("the census found no %q in %s, so it read a corpus that cannot contain the primitive; "+
			"a clean result here would mean nothing",
			externalInputPrimitiveDecl, externalInputPrimitiveDeclFile)
	}
	if sawCompositionHome == 0 {
		t.Fatalf("the census found no construction of the primitive in %s, so the composed postures are not "+
			"where the guard is looking; a clean result would mean nothing", externalInputCompositionHome)
	}

	for file, n := range found {
		t.Errorf("%s composes an external-input service itself (%d construction(s)); "+
			"a site that composes one decides acquisition, transport and the pinned view independently, "+
			"and a second set of those decisions agrees today and diverges silently later — "+
			"ask %s for a named posture instead", file, n, externalInputCompositionHome)
	}
}
