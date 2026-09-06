// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// verificationUnit names one source directory the plugin-fetch verification
// chokepoint spans, together with the prefix every call to a function declared
// in it resolves to.
//
// The chokepoint is not confined to one package: the verification home and its
// host-function primitive are declared in internal/pluginfetchverify, and the
// fetch tiers that must route through them are declared in the command package.
// A guard walking one directory would see the call sites on that side and pass
// blind on the other, so it walks both and reports one caller set per callee
// across them.
type verificationUnit struct {
	dir    string
	prefix string
}

var verificationUnits = []verificationUnit{
	{dir: ".", prefix: "main"},
	{dir: filepath.Join("..", "..", "internal", "pluginfetchverify"), prefix: "pluginfetchverify"},
}

// qualify resolves a call expression to the package-qualified name of its
// callee, so one function has one spelling wherever it is called from: a call
// inside the callee's own package spells it bare, a call from outside spells it
// through the importing file's name for that package, and both resolve to the
// same "prefix.Name" key here. That is what lets one expected caller set cover
// call sites on both sides of a package boundary.
//
// A qualified callee resolves through the file's own imports, and to the LAST
// SEGMENT of the imported path rather than to the local name, so an aliased
// import resolves to the same key as an unaliased one. Resolving through the
// imports also keeps two same-named functions in different packages apart:
// pullverify.Verify and pluginfetchverify.Verify are distinct keys, and a
// qualifier-stripping shortcut would silently merge them.
//
// A selector whose left side is not an imported package in that file (a method
// call, a call through a variable) yields "" and is ignored, as does any other
// call shape.
func qualify(fun ast.Expr, pkgPrefix string, imports map[string]string) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return pkgPrefix + "." + f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			if prefix, ok := imports[x.Name]; ok {
				return prefix + "." + f.Sel.Name
			}
		}
	}
	return ""
}

// importPrefixes maps each package name a file uses as a qualifier to the last
// segment of the path it imports, which is the canonical prefix qualify keys on.
// A blank or dot import contributes no qualifier and is skipped.
func importPrefixes(file *ast.File) map[string]string {
	prefixes := map[string]string{}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		canonical := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			canonical = path[i+1:]
		}
		local := canonical
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				continue
			}
			local = spec.Name.Name
		}
		prefixes[local] = canonical
	}
	return prefixes
}

// enclosingCallers parses every non-test Go source file in each unit of the
// chokepoint and returns, for each package-qualified callee, the set of
// package-qualified top-level function names whose body calls it. It is the
// structural fact the chokepoint guard asserts against.
//
// A unit that parses to no package fails the walk loudly rather than
// contributing an empty caller set: an empty set from a directory that moved
// looks exactly like a call site that was removed, and the guard would then pass
// while seeing nothing.
func enclosingCallers(t *testing.T) map[string]map[string]bool {
	t.Helper()
	callers := map[string]map[string]bool{}
	for _, unit := range verificationUnits {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, unit.dir, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("parse package source at %s: %v", unit.dir, err)
		}
		if len(pkgs) == 0 {
			t.Fatalf("no package source parsed at %s; the verification chokepoint's source moved — "+
				"point verificationUnits at its directories so this guard keeps seeing every call site", unit.dir)
		}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				imports := importPrefixes(file)
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Name == nil || fn.Body == nil {
						continue
					}
					enclosing := unit.prefix + "." + fn.Name.Name
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						call, ok := n.(*ast.CallExpr)
						if !ok {
							return true
						}
						if name := qualify(call.Fun, unit.prefix, imports); name != "" {
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
	}
	return callers
}

// TestPluginFetchVerificationChokepoint locks the single-verification-home
// invariant structurally: every plugin fetch reaches verification, and a new
// fetch path cannot silently skip it. It pins the exact set of functions that
// call each of the three verification surfaces:
//
//   - pluginfetchverify.Verify is the single home the cache-not-pin tiers route
//     through. Its only callers are the verdict-socket cache verify and the CLI
//     install fetch. A new cache-not-pin fetch that skips it (or a second copy
//     that re-implements verification inline) breaks this set.
//   - pullverify.Verify (the registry-signature primitive) is reachable from
//     exactly two homes: the closure signature pass (the pinned tier) and
//     pluginfetchverify.Verify (every other tier). A third caller means a fetch
//     path grew its own signature-verify copy instead of routing through a home.
//   - pluginfetchverify.VerifyHostFunctionStamp (the host-function primitive) is
//     reachable from exactly two homes: the closure host-function pass and
//     pluginfetchverify.Verify. A third caller means the same dis-unification.
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
			callee: "pluginfetchverify.Verify",
			want:   []string{"main.productionInstallPluginFetch", "main.verifyCachedVerdictSocketPlugin"},
		},
		{
			callee: "pullverify.Verify",
			want:   []string{"main.verifyClosureSignatures", "pluginfetchverify.Verify"},
		},
		{
			callee: "pluginfetchverify.VerifyHostFunctionStamp",
			want:   []string{"main.verifyPluginHostFunctions", "pluginfetchverify.Verify"},
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
				"a plugin fetch must route through the single verification home (pluginfetchverify.Verify) "+
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
	if !callers["main.verifyCachedVerdictSocketPlugin"]["main.cacheVerdictSocketPlugins"] {
		t.Errorf("cacheVerdictSocketPlugins does not call verifyCachedVerdictSocketPlugin; "+
			"the verdict-socket fetch loop must verify every gate it caches (callers seen: %v)",
			callers["main.verifyCachedVerdictSocketPlugin"])
	}
}
