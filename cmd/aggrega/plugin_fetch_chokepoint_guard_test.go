// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// calleeName returns the callee spelling of a call expression: a bare function
// name for a package-local call (verifyFetchedPlugin) or "pkg.Name" for a
// qualified call (pullverify.Verify). Anything else (a method value, a call
// through a variable) yields "" and is ignored.
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

// enclosingCallers parses every non-test Go source file in the package and
// returns, for each callee spelling, the set of top-level function names whose
// body calls it. It is the structural fact the chokepoint guard asserts against.
func enclosingCallers(t *testing.T) map[string]map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package source: %v", err)
	}
	callers := map[string]map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name == nil || fn.Body == nil {
					continue
				}
				enclosing := fn.Name.Name
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if name := calleeName(call.Fun); name != "" {
						if callers[name] == nil {
							callers[name] = map[string]bool{}
						}
						callers[name][enclosing] = true
					}
					return true
				})
			}
		}
	}
	return callers
}

// TestPluginFetchVerificationChokepoint locks the single-verification-home
// invariant structurally: every plugin fetch reaches verification, and a new
// fetch path cannot silently skip it. It pins the exact set of functions that
// call each of the three verification surfaces:
//
//   - verifyFetchedPlugin is the single home the cache-not-pin tiers route
//     through. Its only callers are the verdict-socket cache verify and the CLI
//     install fetch. A new cache-not-pin fetch that skips it (or a second copy
//     that re-implements verification inline) breaks this set.
//   - pullverify.Verify (the registry-signature primitive) is reachable from
//     exactly two homes: the closure signature pass (the pinned tier) and
//     verifyFetchedPlugin (every other tier). A third caller means a fetch path
//     grew its own signature-verify copy instead of routing through a home.
//   - verifyPluginHostFunctionStamp (the host-function primitive) is reachable
//     from exactly two homes: the closure host-function pass and
//     verifyFetchedPlugin. A third caller means the same dis-unification.
//
// If a legitimate new verification route is added, update the expected sets
// consciously — the failure is the point: it forces the author to prove the new
// path verifies rather than letting it land unverified.
func TestPluginFetchVerificationChokepoint(t *testing.T) {
	callers := enclosingCallers(t)

	cases := []struct {
		callee string
		want   []string
	}{
		{
			callee: "verifyFetchedPlugin",
			want:   []string{"productionInstallPluginFetch", "verifyCachedVerdictSocketPlugin"},
		},
		{
			callee: "pullverify.Verify",
			want:   []string{"verifyClosureSignatures", "verifyFetchedPlugin"},
		},
		{
			callee: "verifyPluginHostFunctionStamp",
			want:   []string{"verifyPluginHostFunctions", "verifyFetchedPlugin"},
		},
	}

	for _, tc := range cases {
		got := make([]string, 0, len(callers[tc.callee]))
		for name := range callers[tc.callee] {
			got = append(got, name)
		}
		sort.Strings(got)
		sort.Strings(tc.want)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("callers of %s = %v; want exactly %v\n"+
				"a plugin fetch must route through the single verification home (verifyFetchedPlugin) "+
				"and must not carry a second copy of a verification primitive; "+
				"if you added a legitimate verification route, update this guard consciously",
				tc.callee, got, tc.want)
		}
	}
}

// TestVerdictSocketFetchRoutesThroughVerify locks the verdict tier's fetch loop
// to its verification hop: cacheVerdictSocketPlugins (the cache-not-pin fetch
// entry) must call verifyCachedVerdictSocketPlugin for every gate it resolves.
// A refactor that resolved and cached a verdict gate without this call would
// leave the gate unverified at build; this guard fails on that regression.
func TestVerdictSocketFetchRoutesThroughVerify(t *testing.T) {
	callers := enclosingCallers(t)
	if !callers["verifyCachedVerdictSocketPlugin"]["cacheVerdictSocketPlugins"] {
		t.Errorf("cacheVerdictSocketPlugins does not call verifyCachedVerdictSocketPlugin; "+
			"the verdict-socket fetch loop must verify every gate it caches (callers seen: %v)",
			callers["verifyCachedVerdictSocketPlugin"])
	}
}
