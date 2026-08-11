// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ambientWorkspaceSiteCeiling bounds how many sites in this package's tests still
// leave their workspace to be discovered from the working directory instead of
// declaring it. It is a ratchet: it only ever decreases, and the census below
// fails when the derived count exceeds it, so a new ambient site cannot be added
// without declaring its workspace. It reaches zero when the resolve-from-working-
// directory fallback is removed, and the constant and the census go with it.
//
// Criterion: the count the census below derives, over every *_test.go file in this
// directory — all of them, whatever their build tags, because the parser reads the
// sources rather than a configured build. Measured 2026-08-11.
const ambientWorkspaceSiteCeiling = 117

// TestAmbientWorkspaceSiteCensus counts the test sites that hand a command helper
// no workspace at all, so resolution falls back to walking upward from the working
// directory until it finds a configuration that anchors a root.
//
// Why the count is worth holding down: a test whose outcome depends on a directory
// it never named can pass on one machine and fail on another, and the failure is
// then attributed to the change under test rather than to the environment. Nor is
// changing into a temporary directory a fix — that declares nothing and passes
// only by the accident that the upward walk finds no configuration above it. A
// test that names the directory it resolves cannot be affected either way, which
// is what resolveTempWorkspace is for.
//
// A site takes one of three shapes, which is why the count is derived rather than
// searched for textually:
//
//  1. A command config literal that carries a workspace field and omits it.
//  2. A literal nil at the workspace parameter of a helper that threads its own
//     parameter down to the fallback. This one is invisible at the fallback's own
//     call site, because the helper passes a named value there and the nil sits a
//     hop earlier.
//  3. A literal nil handed straight to the fallback.
//
// 🔴 Both covered sets are DERIVED from the parsed source, and the derivations are
// shared with the guard over production code rather than restated here, so the two
// counts cannot disagree about what a site is. A hand-written list of config type,
// helper or file names would be a second copy of the declarations, and the copy is
// what goes stale: a new command config would be silently uncounted on the day it
// is added, which is precisely when the count is worth having.
func TestAmbientWorkspaceSiteCensus(t *testing.T) {
	fset := token.NewFileSet()
	prod, tests := parsePackageSources(t, fset)

	// The derivations are shared with the production-side guard. Assert each is
	// non-empty before trusting a count taken from it: a derivation that silently
	// produced nothing would report zero ambient sites and pass this ceiling for
	// the wrong reason, which is the one failure a maximum cannot catch on its own.
	cfgTypes := workspaceBearingConfigTypes(prod)
	if len(cfgTypes) == 0 {
		t.Fatal("derived no command config type carrying a ws field — the derivation is broken, not the source")
	}
	// Test sources join the derivation corpus here, unlike on the production side:
	// a helper declared in a test file can thread a workspace parameter down to the
	// fallback just as a production one can, and a nil handed to it is a site.
	both := make(map[string]*ast.File, len(prod)+len(tests))
	for name, node := range prod {
		both[name] = node
	}
	for name, node := range tests {
		both[name] = node
	}
	threaded := workspaceThreadingHelpers(both)
	if len(threaded) == 0 {
		t.Fatal("derived no helper threading a workspace parameter into workspaceOrResolve — the derivation is broken, not the source")
	}
	if len(tests) == 0 {
		t.Fatal("parsed no test source in this directory — the walk is broken, not the source")
	}

	sites := ambientWorkspaceSites(fset, tests, cfgTypes, threaded)
	if len(sites) > ambientWorkspaceSiteCeiling {
		t.Errorf("%d test site(s) resolve a workspace they do not declare, above the ceiling of %d — declare the workspace at the new site with resolveTempWorkspace(t, tempWorkspaceOptions{Root: <the directory the test works in>}); the ceiling only moves down:\n%s",
			len(sites), ambientWorkspaceSiteCeiling, strings.Join(sites, "\n"))
	}
}

// parsePackageSources parses every Go file in this directory, split into
// production and test sources. Build tags are deliberately not applied: the census
// counts what the sources contain, and a tagged file no untagged build compiles is
// exactly the kind of file whose sites would otherwise go uncounted.
func parsePackageSources(t *testing.T, fset *token.FileSet) (prod, tests map[string]*ast.File) {
	t.Helper()
	prod, tests = map[string]*ast.File{}, map[string]*ast.File{}
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob .: %v", err)
	}
	for _, file := range matches {
		node, perr := parser.ParseFile(fset, file, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", file, perr)
		}
		if strings.HasSuffix(file, "_test.go") {
			tests[file] = node
		} else {
			prod[file] = node
		}
	}
	return prod, tests
}

// ambientWorkspaceSites returns one "file:line: shape" entry per site in files
// that reaches the resolve-from-working-directory fallback, sorted so the census
// reports the same list whatever order the files were parsed in.
func ambientWorkspaceSites(fset *token.FileSet, files map[string]*ast.File, cfgTypes map[string]bool, threaded map[string]int) []string {
	var found []string
	for _, node := range files {
		ast.Inspect(node, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				id, ok := v.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if id.Name == "workspaceOrResolve" {
					if len(v.Args) == 1 {
						if arg, ok := v.Args[0].(*ast.Ident); ok && arg.Name == "nil" {
							found = append(found, fmt.Sprintf("%s: workspaceOrResolve(nil)", fset.Position(v.Pos())))
						}
					}
					return true
				}
				if idx, isThreaded := threaded[id.Name]; isThreaded && idx < len(v.Args) {
					if arg, ok := v.Args[idx].(*ast.Ident); ok && arg.Name == "nil" {
						found = append(found, fmt.Sprintf("%s: %s(… nil at the workspace parameter …)", fset.Position(v.Pos()), id.Name))
					}
				}
			case *ast.CompositeLit:
				typeName, wsField := "", ""
				switch tp := v.Type.(type) {
				case *ast.Ident:
					if cfgTypes[tp.Name] {
						typeName, wsField = tp.Name, "ws"
					}
				case *ast.SelectorExpr:
					if pkg, ok := tp.X.(*ast.Ident); ok && pkg.Name == "lintops" && tp.Sel.Name == "Config" {
						typeName, wsField = "lintops.Config", "Workspace"
					}
				}
				if typeName == "" {
					return true
				}
				for _, elt := range v.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if k, ok := kv.Key.(*ast.Ident); ok && k.Name == wsField {
							return true
						}
					}
				}
				found = append(found, fmt.Sprintf("%s: %s literal without a %s field", fset.Position(v.Pos()), typeName, wsField))
			}
			return true
		})
	}
	sort.Strings(found)
	return found
}
