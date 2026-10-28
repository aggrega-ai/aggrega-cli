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

// compositionCallForms are the two accessors that, between them, ARE the
// governance attachment list a build dispatches: the transform half reconstructed
// from the lockfile, and the verdict half read live. Each is searched in its call
// form — dot-prefixed, or package-qualified — so the declarations themselves are
// not matches.
var compositionCallForms = []string{
	".VerdictGovernanceRefs(",
	"lockfile.MutatorAttachments(",
}

// compositionHome is the one file allowed to call them: the shared composition
// every build surface routes through.
const compositionHome = "internal/compilerops/verdict_trust.go"

// TestGovernanceCompositionHasOneHome is the guard that keeps the composition
// single-sourced, and it guards against a failure that is silent by construction.
//
// A surface that composes the attachment list itself acquires no recorded digests
// with it: the digests arrive as a map, an unsupplied map is an empty one, and an
// empty one means "nothing is recorded" rather than "nobody supplied this". So a
// second composition site runs every configured gate while holding none of them to
// the identity that was reviewed, and nothing fails, nothing warns, and the build's
// output is byte-identical to a build that held them. Proving that today's two
// surfaces both carry the supply would say nothing about a third added later, which
// is why this guards the CALL rather than the surfaces.
//
// The census is calibrated by requiring the known home to be found: a walk that
// returned nothing at all — a moved module root, a changed accessor name — would
// otherwise report a clean single-sourcing over a corpus it never read.
func TestGovernanceCompositionHasOneHome(t *testing.T) {
	moduleRoot := moduleRootFromTestDir(t)

	found := map[string]int{}
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
		for _, form := range compositionCallForms {
			if n := strings.Count(string(body), form); n > 0 {
				found[filepath.ToSlash(rel)] += n
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if found[compositionHome] == 0 {
		t.Fatalf("the census found no call in %s, so it read a corpus that cannot contain the composition; "+
			"the guard would report single-sourcing it never measured (found: %+v)", compositionHome, found)
	}
	for file, n := range found {
		if file == compositionHome {
			continue
		}
		t.Errorf("%s composes the governance attachment list itself (%d call(s)); "+
			"a surface that composes it acquires no recorded verdict-plugin digests with it, and the omission is silent — "+
			"route it through %s instead", file, n, compositionHome)
	}
}

// attachmentHalfCallForm is the call form of the composition's attachment half
// reached on its own, from outside this package.
const attachmentHalfCallForm = ".ComposeGovernanceAttachments("

// attachmentHalfCallers are the files allowed to reach the attachment half
// without the record of the plugin identities trusted for verdict attachments: the
// release inspection, which draws the graph a build produces and dispatches no
// verdict role, so there is no verdict plugin for that record to hold.
var attachmentHalfCallers = map[string]bool{
	"cmd/aggrega/inspect.go": true,
}

// TestTheAttachmentHalfAloneServesOnlyASurfaceDispatchingNoVerdict holds the
// exported attachment half to the surfaces that need it alone. A surface that
// dispatches a verdict attachment and composed through it would run that gate held
// to no recorded identity — the silent omission the guard above exists for, reached
// one call over — so each caller is named here, and adding one is a decision made
// in this list rather than by reaching for the function.
//
// The census is calibrated the same way: every allowed caller must be found, so a
// walk that read nothing cannot report a clean set.
func TestTheAttachmentHalfAloneServesOnlyASurfaceDispatchingNoVerdict(t *testing.T) {
	moduleRoot := moduleRootFromTestDir(t)

	found := map[string]int{}
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
		if n := strings.Count(string(body), attachmentHalfCallForm); n > 0 {
			rel, relErr := filepath.Rel(moduleRoot, path)
			if relErr != nil {
				return relErr
			}
			found[filepath.ToSlash(rel)] += n
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	for file := range attachmentHalfCallers {
		if found[file] == 0 {
			t.Errorf("the census found no call in %s, which is listed as a caller; either the census read a corpus "+
				"that cannot contain it, or the caller no longer needs the listing (found: %+v)", file, found)
		}
	}
	for file, n := range found {
		if attachmentHalfCallers[file] {
			continue
		}
		t.Errorf("%s composes the governance attachment list without the verdict-plugin record (%d call(s)); "+
			"a surface dispatching a verdict attachment composes through ApplyGovernanceComposition, and one that "+
			"dispatches none is named in attachmentHalfCallers", file, n)
	}
}

// moduleRootFromTestDir returns the module root from this package's test working
// directory, which Go sets to the package directory.
func moduleRootFromTestDir(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	// This test runs from internal/compilerops/; the module root is two dirs up.
	return filepath.Dir(filepath.Dir(cwd))
}
