// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// selectedPathCallSites is the sanctioned set: every parameter on this surface
// resolved as a path the person driving the client selected, keyed by the label
// its call site passes.
//
// 🔴 This is the one HAND-WRITTEN set in this guard, and it is hand-written
// because it IS the decision rather than a fact about the source. Deriving it
// from the calls would make the guard agree with whatever the code does, which is
// the one thing a guard must not do. Each entry names a parameter an operation
// only READS and which an author selects for themselves: a description exported
// by another tool, a module built under a toolchain's own output directory, and
// the overlay authored beside that module.
//
// Adding an entry is a deliberate decision that a parameter's reach may leave the
// workspace, and it belongs with whoever may take that decision rather than with
// whoever is adding the call.
var selectedPathCallSites = map[string]string{
	"source":                      "aggrega/import converts a description the author selected",
	"plugin gen-catalog: plugin":  "aggrega/pluginGenCatalog reads a module the author built",
	"plugin gen-catalog: overlay": "aggrega/pluginGenCatalog reads the overlay authored beside that module",
	// Reached through pickedOrWorkspacePath, which takes the selected class only for
	// an absolute value — a file picked in a dialog — and holds a relative one inside
	// the workspace by workspacePath.
	"registry add: keyPath":     "aggrega/registryAdd reads a public-key file the author picked in a dialog",
	"registry add: trustedRoot": "aggrega/registryAdd records a trust-root file the author picked in a dialog",
}

// selectedPathForwarders are the helpers that reach selectedPath on behalf of a
// caller, forwarding the caller's label. The label is judged where the forwarder
// is called, which is where it is a literal, rather than at the forwarder's own
// call of selectedPath, where it is a variable.
var selectedPathForwarders = map[string]bool{
	"pickedOrWorkspacePath": true,
}

// TestSelectedPathIsReachedOnlyBySanctionedParameters keeps the permissive path
// class from becoming the hole in a design whose whole value is having one home.
//
// Two rules resolve a path on this surface. workspacePath confines a path to the
// workspace the session opened, and every destination takes it. selectedPath
// confines nothing, because its subject is a file someone picked out of a dialog
// and such a file sits wherever they put it. The failure this guards is not a
// wrong rule written on purpose: it is a later call site reaching for the
// permissive helper because it was the nearer one, silently widening a parameter
// nobody decided to widen. Nothing about that mistake fails to compile, and the
// widened parameter behaves correctly on every input a test is likely to send.
//
// It FAILS rather than reports. A Go package that passes prints nothing at all —
// not a log line, not standard error, not standard output — so a guard that only
// reported what it found would be inert in every routine test run whatever it
// found.
//
// 🔴 What it cannot see, because a guard's reach is part of its contract: a call
// site in another package. The walk covers this package's own production source,
// which is where the wire boundary is, and pathscope.ResolveSelected is exported
// — so another package may reach that resolver directly and this check will not
// know. The path-scope package's guard of the one judgement of inside holds that
// instead: it walks the whole module and lets pathscope.ResolveSelected be reached
// by selectedPath alone.
func TestSelectedPathIsReachedOnlyBySanctionedParameters(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package source: %v", err)
	}

	seen := map[string]bool{}
	visited := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		visited++
		fset := token.NewFileSet()
		node, perr := parser.ParseFile(fset, file, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", file, perr)
		}

		// Every expression that is the function OF a call, collected first so a
		// bare method value is distinguishable from a call below. A resolver
		// assigned into a table and invoked later carries no call site for the
		// label check to read, which would leave the site resolving a path with
		// nothing able to judge it.
		called := map[ast.Expr]bool{}
		ast.Inspect(node, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				called[call.Fun] = true
			}
			return true
		})

		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// The enclosing function is read from the declaration rather than
			// tracked during the walk, so the one legitimate direct use of
			// pathscope.ResolveSelected — inside the helper that IS this class's
			// wire boundary — is exempt by what it is rather than by which file it
			// happens to sit in.
			inHelper := fn.Name.Name == "selectedPath"

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}

				// pathscope.ResolveSelected reached past the helper. The helper is
				// the one place this surface's refusal sentence is written, so a
				// dispatch calling the resolver directly would answer with no
				// sentence at all, or with a second copy of one.
				if pkg, isPkg := sel.X.(*ast.Ident); isPkg && pkg.Name == "pathscope" &&
					sel.Sel.Name == "ResolveSelected" && !inHelper {
					t.Errorf("%s: pathscope.ResolveSelected called directly in %s — route a selected path through the selectedPath helper, which is where this surface's refusal sentence is written",
						fset.Position(sel.Pos()), fn.Name.Name)
					return true
				}

				if sel.Sel.Name != "selectedPath" && !selectedPathForwarders[sel.Sel.Name] {
					return true
				}
				if !called[ast.Expr(sel)] {
					t.Errorf("%s: %s used as a value rather than called, in %s — call it directly at each site, because a resolver held as a value carries no call site for this guard to read",
						fset.Position(sel.Pos()), sel.Sel.Name, fn.Name.Name)
					return true
				}
				return true
			})

			// The labels are read off the calls themselves, which is where the
			// argument list is. A forwarder's own call of selectedPath carries its
			// caller's label, which is judged at the forwarder's call sites instead.
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || (sel.Sel.Name != "selectedPath" && !selectedPathForwarders[sel.Sel.Name]) {
					return true
				}
				if sel.Sel.Name == "selectedPath" && selectedPathForwarders[fn.Name.Name] {
					return true
				}
				label, isLiteral := firstStringLiteral(call.Args)
				if !isLiteral {
					t.Errorf("%s: selectedPath called with a computed label in %s — pass the parameter's own name as a string literal, so the sanctioned set names a parameter rather than a variable",
						fset.Position(call.Pos()), fn.Name.Name)
					return true
				}
				if _, sanctioned := selectedPathCallSites[label]; !sanctioned {
					t.Errorf("%s: selectedPath resolves %q, which is not a sanctioned selected parameter.\n"+
						"A path this surface only READS and which the author picks for themselves may go here. A path any operation WRITES to may not, and neither may a workspace-scoped read.\n"+
						"Use workspacePath instead, which confines the value to the workspace the session opened. If %q genuinely names a file the author selects out of a dialog, add it to selectedPathCallSites in this file together with its reason — that entry is the decision, and it is not the caller's to take.",
						fset.Position(call.Pos()), label, label)
					return true
				}
				seen[label] = true
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

	// Each sanctioned entry must have been FOUND, not merely permitted. A set
	// difference is asserted rather than a count, because both sides short by one
	// still agree on a count: a renamed label would silently drop a parameter out
	// of this guard's reach while leaving the tally right.
	var missing []string
	for label := range selectedPathCallSites {
		if !seen[label] {
			missing = append(missing, label)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("sanctioned selected parameters that no call site resolves: %v\n"+
			"Either the parameter was renamed, in which case update the label here in the same change; or it no longer resolves a selected path, in which case drop its entry — a permission nothing exercises reads as one something does.",
			missing)
	}
}

// firstStringLiteral returns the value of args[0] when it is a string literal.
// The label is what the sanctioned set is keyed by, so a site that does not pass
// one cannot be judged against that set at parse time.
func firstStringLiteral(args []ast.Expr) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	lit, ok := args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return unquoted, true
}
