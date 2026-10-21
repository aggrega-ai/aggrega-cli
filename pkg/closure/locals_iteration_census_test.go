// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Four sites walk an authored locals block entry by entry, and they split two
// and two. Two EVALUATE each entry as a template; two only READ ITS TEXT.
//
// The split is the substance, because the two halves carry different
// obligations. An evaluator answers what a locals block RESOLVES to, so a
// second one is a second answer to one question and the two can disagree — and
// the two that exist do disagree, in both directions, which the pin beside this
// file records. A text reader answers nothing about resolution: it inspects
// authored template text and executes none of it, so adding one introduces no
// second answer to anything.
//
// Nothing structural holds that split. A fifth site would join whichever half
// its author happened to write it into, and the choice is invisible in a diff
// that shows only a loop. So this census names the four with a per-site reason
// and fails on a fifth, which is what forces that choice to be made
// deliberately rather than by default.
//
// # What this census can and cannot tell
//
// It counts sites TEXTUALLY. It cannot tell whether an existing site still does
// the right thing: a site listed here as a text reader that started evaluating
// entries would keep passing, and a site whose evaluation semantics changed
// would keep passing too. It is a floor under the ARRANGEMENT, not a proof of
// any site's behaviour — the behaviour is pinned by assertions that drive the
// two evaluators against each other.
//
// Its discriminator is a range statement over a map the source names `locals`
// or `Spec.Locals`, so it also cannot see a block iterated under some third
// name, nor one walked without ranging its entries. Both bounds are the price
// of a check that needs no site to cooperate; a site that opts out of a
// registry is invisible to the registry, whereas a site that opts out of this
// has to avoid writing the loop everyone writes.

// localsIterationRole is which of the two halves a site sits in.
type localsIterationRole string

const (
	// localsRoleEvaluates is a site that executes each entry as a template and
	// produces resolved values. A new one is a new answer to what a locals
	// block resolves to.
	localsRoleEvaluates localsIterationRole = "evaluates each entry"
	// localsRoleReadsText is a site that inspects each entry's authored text
	// and executes nothing. It produces no resolved value, so it cannot
	// disagree with an evaluator about one.
	localsRoleReadsText localsIterationRole = "reads the entry text only"
)

type localsIterationSite struct {
	role localsIterationRole
	why  string
}

// localsIterationCensus is every site entitled to walk a locals block entry by
// entry, keyed by module-relative path, with the role it sits in and the reason
// it is there.
func localsIterationCensus() map[string]localsIterationSite {
	return map[string]localsIterationSite{
		"internal/compiler/renderer/locals.go": {
			role: localsRoleEvaluates,
			why: "the rendered-output evaluator, and the one home of that contract for every surface " +
				"that resolves a block for rendering; refuses an out-of-order or unknown .locals " +
				"reference before executing anything",
		},
		"pkg/closure/recipe_source_descent.go": {
			role: localsRoleEvaluates,
			why: "the source-coordinate evaluator, reached at lock time and at build time from one body; " +
				"evaluates only the entries a source field reads, directly or through another entry, or " +
				"every entry where a field reads the block whole; skips an entry that cannot execute, and " +
				"registers the Aggrega helpers without Sprig's",
		},
		"internal/compiler/renderer/boundary_fr.go": {
			role: localsRoleReadsText,
			why:  "collects each entry's authored text into the field list a boundary scan inspects",
		},
		"internal/compiler/renderer/sensitive_taint.go": {
			role: localsRoleReadsText,
			why:  "inspects each entry's authored text for a reference to a withheld value",
		},
	}
}

// localsIterationPattern matches a range statement over a locals block: the map
// named `locals`, or reached as `<something>.Spec.Locals`. The optional dotted
// prefix is what admits `recipe.Spec.Locals.All()` and `fr.Spec.Locals.All()`
// alongside a bare `locals.All()`.
var localsIterationPattern = regexp.MustCompile(`range\s+(?:[A-Za-z_][A-Za-z0-9_.]*)?[Ll]ocals\.All\(\)`)

// TestLocalsIterationSiteCensus pins the four-site, two-and-two arrangement.
//
// It fails in four separate ways, and each is a change worth stopping on: a new
// site, a listed site that no longer iterates, a census that matched nothing,
// and a census in which one of the two halves has emptied out.
func TestLocalsIterationSiteCensus(t *testing.T) {
	t.Parallel()
	root := localsCensusModuleRoot(t)
	expected := localsIterationCensus()

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
		if n := len(localsIterationPattern.FindAllIndex(b, -1)); n > 0 {
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = n
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Calibration: a census that found nothing would satisfy every assertion
	// below for the wrong reason.
	if len(found) == 0 {
		t.Fatal("the census matched no locals-iteration site at all; the scan is broken, not the code")
	}

	for path := range found {
		if _, ok := expected[path]; !ok {
			t.Errorf("%s walks a locals block entry by entry and is not in the census.\n"+
				"Decide which half it sits in and add it with that reason.\n"+
				"  %q — it executes each entry as a template and produces resolved values. That is a "+
				"SECOND answer to what a locals block resolves to, so it must agree with the existing "+
				"evaluators or the disagreement must be recorded where it is implemented, with what it "+
				"covers and what it over-covers.\n"+
				"  %q — it inspects authored text and executes nothing, so it produces no resolved value "+
				"and cannot disagree with an evaluator about one.",
				path, localsRoleEvaluates, localsRoleReadsText)
		}
	}
	for path, site := range expected {
		if _, ok := found[path]; !ok {
			t.Errorf("%s no longer walks a locals block (%s: %s); drop it from the census",
				path, site.role, site.why)
		}
	}

	// Both halves have to stay populated, or the split above is pinned by a
	// census that no longer exercises it: a census with no text reader cannot
	// show that the distinction is live, and one with no evaluator is not
	// watching the sites that can disagree.
	var evaluators, textReaders int
	for path := range found {
		switch expected[path].role {
		case localsRoleEvaluates:
			evaluators++
		case localsRoleReadsText:
			textReaders++
		}
	}
	if evaluators == 0 {
		t.Error("the census found no evaluating site; the half that can disagree is unwatched")
	}
	if textReaders == 0 {
		t.Error("the census found no text-reading site; the two-and-two split is no longer exercised")
	}
	t.Logf("census: %d site(s) — %d evaluate, %d read text only", len(found), evaluators, textReaders)
}

// localsCensusModuleRoot is the module root the census walks.
func localsCensusModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// pkg/closure -> module root
	root := filepath.Join(wd, "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	return root
}
