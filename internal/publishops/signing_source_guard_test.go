// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// signingResolverHome is the one function in this package allowed to ask a
// workspace configuration what signing settings apply. Every other site resolves
// through it.
const signingResolverHome = "resolveSigning"

// TestTheConfiguredSigningKeyHasOneSource keeps the question "may this caller
// spend the key the workspace's configuration names" from acquiring a second
// answer.
//
// Four verbs in this package build a signer, and each has its own reason to ask a
// workspace what signing settings apply. Every one of them asking directly is an
// arrangement with no single failure — each site can be written correctly — and
// with four places to remember, where a caller that must not reach a key the
// server holds is safe only if all four do. resolveSigning is where the question
// is answered instead: it removes the key from the value a withholding caller
// sees, so a site that knows nothing about the withholding still cannot select a
// key with it.
//
// The failure this guards is not a wrong rule written on purpose. It is a fifth
// verb, or a fifth branch of an existing one, calling the configuration's own
// resolver because that was the nearer thing to write — which compiles, passes
// every test whose workspace names no key, and silently hands the server's key
// back to every caller that was supposed to be unable to reach it.
//
// It FAILS rather than reports. A Go package that passes prints nothing at all —
// not a log line, not standard error, not standard output — so a guard that only
// reported what it found would be inert in every routine test run whatever it
// found.
//
// 🔴 What it cannot see, because a guard's reach is part of its contract:
//
//   - A call site in another package. The walk covers this package's own
//     production source, which is where every signer this layer builds is built.
//     A caller outside it that resolves a signing configuration of its own and
//     hands the result in as a KeyPath has supplied a key path, which is the
//     input this layer takes at face value by design.
//   - A key path reaching the layer through a config field rather than through
//     the configuration. That is the caller's own supplied value, which the
//     withholding deliberately does not touch: it withholds the key the SERVER
//     names, not the one the caller named.
func TestTheConfiguredSigningKeyHasOneSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package source: %v", err)
	}
	sort.Strings(files)

	fset := token.NewFileSet()
	visited := 0
	sanctioned := 0

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		node, perr := parser.ParseFile(fset, file, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", file, perr)
		}
		visited++

		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// The enclosing function is read from the declaration rather than
			// tracked during the walk, so the one legitimate direct use — inside
			// the resolver that IS this package's boundary — is exempt by what it
			// is rather than by which file it happens to sit in.
			inHome := fn.Name.Name == signingResolverHome

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, isSel := n.(*ast.SelectorExpr)
				if !isSel || sel.Sel.Name != "ResolveSigning" {
					return true
				}
				if inHome {
					sanctioned++
					return true
				}
				t.Errorf("%s: ResolveSigning reached directly in %s — resolve through %s, which is the one "+
					"place this package asks a workspace configuration what signing settings apply, and the "+
					"one place a caller that may not spend a key the server holds has that key withheld from "+
					"what it is shown",
					fset.Position(sel.Pos()), fn.Name.Name, signingResolverHome)
				return true
			})
		}
	}

	// The walk's own reach is asserted rather than assumed. A guard that is
	// negative by default passes over an empty walk exactly as it passes over a
	// clean tree, so a relocated package or a mistaken skip would retire it in
	// silence.
	if visited == 0 {
		t.Fatal("the walk visited no production Go file in this package — the guard is inert, not the tree clean")
	}
	if sanctioned == 0 {
		t.Fatalf("the walk found no call to ResolveSigning inside %s — either the resolver asks the "+
			"configuration nothing, or this guard names a function the package does not have, in which case "+
			"every other site is unguarded", signingResolverHome)
	}
}
