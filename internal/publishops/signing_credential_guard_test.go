// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// theSigningSequenceHome names, for each step of turning a caller's signing
// inputs into a signer, the one function in this package allowed to take it.
//
// The first three are this package's own steps: reading a registry's signing
// configuration, resolving a keyless identity, and constructing a signer. The
// last three are the signing package's constructors beneath them, held to the one
// function each so the sequence cannot be re-derived one level down either.
//
// A method is named by its receiver's type and its own name.
var theSigningSequenceHome = map[string]string{
	"resolveSigning":       "resolveCredential",
	"resolveIdentity":      "resolveCredential",
	"buildSigner":          "credential.signerFor",
	"ResolveIdentityToken": "resolveIdentity",
	"LoadKeyedSigner":      "buildSigner",
	"KeylessSigner":        "buildSigner",
}

// signingPackageSteps are the steps above reached through the signing package, so
// they are matched only where that package qualifies them.
var signingPackageSteps = map[string]bool{
	"ResolveIdentityToken": true,
	"LoadKeyedSigner":      true,
	"KeylessSigner":        true,
}

// TestEverySignerIsResolvedThroughTheOneCredentialHome keeps the sequence that
// turns a caller's inputs into a signer in the one place every signing verb
// reaches: resolveCredential and the signer it hands out.
//
// The failure it guards is not a wrong rule written on purpose. It is a new verb,
// or a new branch of an existing one, taking one of these steps itself because that
// was the nearer thing to write — which compiles, signs, and passes every test
// whose references share one credential, while deciding the credential from
// whatever that verb happened to look at. That is how a batch once took its whole
// credential from its first reference.
//
// It FAILS rather than reports: a Go package that passes prints nothing at all, so
// a guard that only reported what it found would be inert in every routine run.
//
// 🔴 What it cannot see, because a guard's reach is part of its contract:
//
//   - Another package. The walk covers this package's production source, which is
//     where every signer the toolchain builds is built today; a package outside it
//     constructing a signer straight from the signing package is not walked.
//   - A verb that hands the home only SOME of its references. That reaches the
//     home and passes this guard, and it is exactly the defect the mixed-credential
//     refusal exists for — it is caught by the batch tests beside the home, which
//     drive every batch verb over references needing different credentials.
//   - Test files, which build signers of their own on purpose.
//
// It reads identifiers rather than calls, so a step taken as a function value is
// caught as surely as one that is called.
func TestEverySignerIsResolvedThroughTheOneCredentialHome(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package source: %v", err)
	}
	sort.Strings(files)

	fset := token.NewFileSet()
	visited := 0
	sanctioned := map[string]int{}

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
			enclosing := declaredName(fn)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				step, at := sequenceStep(n)
				if step == "" {
					return true
				}
				if theSigningSequenceHome[step] == enclosing {
					sanctioned[step]++
					return true
				}
				t.Errorf("%s: %s is reached in %s — every signer is resolved through resolveCredential, and %s is "+
					"taken only in %s, so a verb taking it elsewhere decides its credential from whatever it "+
					"happened to look at rather than from every reference it signs",
					fset.Position(at), step, enclosing, step, theSigningSequenceHome[step])
				return true
			})
		}
	}

	// The walk's own reach is asserted rather than assumed. A guard that is
	// negative by default passes over an empty walk exactly as it passes over a
	// clean tree, so a renamed home or a relocated step would retire it in silence.
	if visited == 0 {
		t.Fatal("the walk visited no production Go file in this package — the guard is inert, not the tree clean")
	}
	for step, home := range theSigningSequenceHome {
		if sanctioned[step] == 0 {
			t.Errorf("the walk found %s nowhere in %s — either the home no longer takes that step, or this guard "+
				"names a function the package does not have, and every other site is then unguarded", step, home)
		}
	}
}

// declaredName names a function declaration as theSigningSequenceHome does: a
// method by its receiver's type and its own name, a function by its name.
func declaredName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// sequenceStep reports which step of the signing sequence n names, if any, and
// where. This package's own steps are bare identifiers; the signing package's are
// selectors qualified by that package.
func sequenceStep(n ast.Node) (string, token.Pos) {
	switch node := n.(type) {
	case *ast.SelectorExpr:
		pkg, ok := node.X.(*ast.Ident)
		if ok && pkg.Name == "signing" && signingPackageSteps[node.Sel.Name] {
			return node.Sel.Name, node.Pos()
		}
	case *ast.Ident:
		if _, step := theSigningSequenceHome[node.Name]; step && !signingPackageSteps[node.Name] {
			return node.Name, node.Pos()
		}
	}
	return "", token.NoPos
}
