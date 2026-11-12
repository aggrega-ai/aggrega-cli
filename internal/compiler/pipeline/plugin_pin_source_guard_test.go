// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// pinSourceHomes is the complete set of non-test files permitted to build a
// pipeline inputs value, relative to the module root. Each one names where its
// plugin pins come from, and this map is what makes a new one a decision rather
// than an oversight.
//
// The map is the specification, not a record of what happens to be there. A new
// entry is a deliberate decision that a surface may drive the compiler, taken by
// editing this file — which is the point: the edit is what puts the decision in
// front of a reviewer, at the moment that surface has to answer where its pins
// come from.
var pinSourceHomes = []string{
	"cmd/aggrega/inspect.go",
	"internal/compiler/pipeline/pipelinetest/build.go",
	"internal/compilerops/build.go",
	"internal/compilerops/validate.go",
	"internal/pullops/pull.go",
	"internal/validateops/entity.go",
}

// TestEveryPipelineInputsConstructionNamesItsPinSource asserts that every
// production site building a pipeline inputs value says where its plugin pins
// come from, and that the set of such sites is exactly the one declared above.
//
// The failure it exists to catch produces no symptom at the construction site. A
// site that names no pin source compiles, runs, and resolves plugins — it just
// resolves them against an address nobody supplied, which probes the unversioned
// cache slot and misses a guest that is on disk. The entry's refusal catches that
// at run time; this guard catches it at the moment the site is written, and it
// also catches the case the refusal structurally cannot: a site that reaches an
// internal helper directly rather than through the entry.
//
// The check is exact on both axes. An unlisted file means a surface started
// driving the compiler without that decision being reviewed. A LISTED file that
// no longer appears means the site moved and this guard did not follow it —
// equally a failure, because a guard whose corpus silently stopped containing
// what it guards reports a clean verdict over nothing at all. The module-root
// assertion below is the same concern one level up: it fails loudly rather than
// walking an empty tree.
//
// 🔴 COVERAGE BOUND. This matches a COMPOSITE LITERAL of the inputs type. A value
// built by declaring a variable and assigning its fields, or returned from a
// helper that built it elsewhere, names the type in no literal and is invisible
// here. Zero production sites use either shape today — every one is a literal —
// and this guard cannot prove that stays true. It bounds the literal route and no
// other.
func TestEveryPipelineInputsConstructionNamesItsPinSource(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q carries no go.mod; the guard is looking in the wrong place, so a clean result here would mean nothing: %v", root, err)
	}

	// file -> the sites in it that named no pin source.
	unnamed := map[string][]string{}
	// the files carrying at least one construction, named or not.
	found := map[string]struct{}{}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch {
			case d.Name() == "testdata", d.Name() == "vendor":
				return fs.SkipDir
			case path != root && strings.HasPrefix(d.Name(), "."):
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", rel, perr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isPipelineInputsType(lit.Type) {
				return true
			}
			found[rel] = struct{}{}
			if !namesAKey(lit, "PluginPins") {
				pos := fset.Position(lit.Pos())
				unnamed[rel] = append(unnamed[rel], fmt.Sprintf("%s:%d", rel, pos.Line))
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk module: %v", walkErr)
	}

	if len(unnamed) > 0 {
		var detail strings.Builder
		for _, f := range sortedKeys(unnamed) {
			for _, site := range unnamed[f] {
				detail.WriteString("\n    " + site)
			}
		}
		t.Errorf(""+
			"every pipeline inputs construction must name where its plugin pins come from.%s\n"+
			"A site with a recorded closure to read supplies pluginpath.RecordedPins(lockfile.PluginPins(lf, releasePath)); "+
			"a site with nothing to pin against supplies pluginpath.NoPins(). "+
			"Omitting the field is neither: it resolves plugins against an empty address.",
			detail.String())
	}

	want := append([]string(nil), pinSourceHomes...)
	sort.Strings(want)
	got := sortedKeys(found)
	if !equalStrings(want, got) {
		t.Errorf(""+
			"the set of files constructing pipeline inputs must be exactly the one pinSourceHomes declares.\n"+
			"  want: %v\n"+
			"  got:  %v\n"+
			"A file present in got and absent from want drives the compiler without that decision having been reviewed: add it here in the same change.\n"+
			"A file present in want and absent from got means the construction moved and this guard did not follow it: a guard over a corpus that no longer contains what it guards reports a clean verdict over nothing.",
			want, got)
	}
}

// isPipelineInputsType reports whether a composite literal's type is the
// pipeline inputs type, in either spelling: qualified from outside the package,
// bare from within it.
//
// Keying on the bare name admits a same-named type from an unrelated package as
// collateral. That is accepted for the reason the sibling guard accepts it: the
// resulting false positive is a visible failure naming a file and line, whereas a
// stricter matcher fails the other way and reports a clean tree. No other type in
// this module is named exactly Inputs today.
func isPipelineInputsType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "Inputs"
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		return ok && pkg.Name == "pipeline" && t.Sel.Name == "Inputs"
	}
	return false
}

// namesAKey reports whether a composite literal assigns the named field.
func namesAKey(lit *ast.CompositeLit, field string) bool {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == field {
			return true
		}
	}
	return false
}
