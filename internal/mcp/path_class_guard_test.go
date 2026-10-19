// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

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

// workspacePathArguments is the sanctioned set: every argument on this surface
// that names a path, keyed by the tool that takes it and the argument's own name.
//
// 🔴 This is the one HAND-WRITTEN set in this guard, and it is hand-written
// because it IS the decision rather than a fact about the source. Deriving it from
// the calls would make the guard agree with whatever the code does, which is the
// one thing a guard must not do. Adding an entry states that an argument names a
// path and that the reach it is given here was decided, and it belongs with
// whoever may take that decision rather than with whoever is adding the call.
//
// Each value says what the argument names, so a reader can tell a document the
// operation READS from a directory it WRITES into — which is the distinction that
// decides how much a widening costs.
var workspacePathArguments = map[string]string{
	"build: release":                "the Release a build renders",
	"pull: release":                 "the Release whose dependencies are resolved",
	"describe: release":             "the Release whose closure is summarised, or whose catalog directories a name is resolved through",
	"list-entities: release":        "the Release whose catalog directories an entity listing is given in the terms of",
	"validate: release":             "the Release validated over its closure, or whose catalog directories an entity is checked in the terms of",
	"validate: entity":              "the catalog entity document checked",
	"validate: plugin":              "the .wasm checked against its own self-report",
	"lint: release":                 "the Release linted",
	"lint: entity":                  "the catalog entity document linted",
	"lint: plugin":                  "the .wasm a pre-publish lint cross-checks",
	"attach-candidates: release":    "the Release whose graph a node is read from",
	"attachment-container: release": "the Release whose graph a node is read from",
	"publish-entity: entity":        "the entity document published",
	"publish-entity: plugin":        "the .wasm published alongside a Plugin entity",
	"publish-artifact: file":        "the data artifact published",
	"deprecate-artifact: from":      "the batch file a withdrawal reads its entries from",
	"insertion-point: file":         "the authored document a position is located in",
	// The pair the position-to-connection tool takes, each with the reach its
	// sibling already has: the Release the way the container tool resolves it, and
	// the document the way the insertion-point tool resolves it — read, never
	// written.
	"connection-at-position: release": "the Release whose graph records the connections",
	"connection-at-position: file":    "the authored document a position is read in",
	// The one entry here that names a document this surface WRITES rather than
	// reads, which is why it is spelled out: the reach given is the reach a
	// caller's own value decides, and every other authored-document argument
	// above only opens the file. The edit is confined to the served workspace by
	// the same helper for that reason rather than by a rule of its own.
	"edit-document: file": "the authored document one bounded edit is applied to, and WRITTEN back",
	// The import tool's pair, one read and one written, spelled out because the
	// split is the point: the source is only opened, and the drafted files land
	// under the directory output_dir names, or under the resolved workspace root
	// where it names none.
	"import: source":          "the foreign description draft authoring files are translated from",
	"import: output_dir":      "the directory drafted authoring files are WRITTEN into",
	"gen-catalog: plugin":     "the .wasm an entity is generated for",
	"gen-catalog: output_dir": "the directory generated entities are WRITTEN into",
	"gen-catalog: overlay":    "the authored overlay a generated entity takes its identity from",
	// Read rather than written, and spelled out because the tool taking it writes
	// beside it: the entity.lock lands in the directory the named document sits in,
	// so this one path decides both what is read and where that write lands.
	"pin: entity": "the Recipe or FeatureRecipe document pinned, beside which the entity.lock is WRITTEN",
	// The one entry not keyed by a tool, and it is spelled out for that reason: every
	// tool taking catalog_dirs has it resolved in one place, where the call's
	// workspace context is composed, so the call rather than any one tool is what
	// names it. Read, never written; a relative directory reaches the workspace root
	// the call resolved first, as a relative 'release' does.
	"the call: catalog_dirs": "the catalog directories a call reads beside its workspace, for every tool taking them",
}

// resolversReachedOnlyAtABoundary are the path-resolving calls that may appear
// only inside the functions boundaryFunctions names. Each composes a location to
// open out of a value a caller sent, which is the act this surface has exactly one
// home for.
//
// pathscope.ReportedName and pathscope.IsLocalSlash are deliberately absent. The
// first spells a path already resolved into the name an answer reports it under,
// and the second judges a slash-separated logical name that is not a filesystem
// path; neither turns a caller's value into somewhere to open.
var resolversReachedOnlyAtABoundary = map[string][]string{
	"pathscope": {"Resolve", "ResolveSelected", "Relative"},
	"filepath":  {"Join", "Abs", "IsAbs", "Clean", "Rel", "EvalSymlinks"},
}

// boundaryFunctions are the functions that may reach those resolvers directly,
// keyed by name, each valued by what it is. There are two boundaries: the one a
// path a caller named comes in through, and the one a location this server holds
// goes out through.
//
// workspacePath is the incoming one, and the only function here that turns a
// caller's value into somewhere to open. reportedLocation and heldLocations are
// the outgoing one, and are exempt for the reason pathscope.ReportedName is
// absent above. The first spells a location this server already holds in the
// name a caller may be told it by, through the same containment judgement that
// confines every path a caller sends; the second filters the server's own
// locations to absolute, cleaned ones, so that a relative string is never matched
// in prose. Neither composes a place to open from anything a caller sent.
//
// Each must be FOUND reaching a resolver, as each sanctioned argument must be
// found resolved, so a rename cannot leave an exemption standing that nothing
// exercises.
var boundaryFunctions = map[string]string{
	"workspacePath":    "resolves a path a caller named, and writes the refusal where it may not reach",
	"reportedLocation": "spells a location this server holds as a caller may be told it",
	"heldLocations":    "lists the server's own locations, absolute and cleaned, for prose to be matched against",
}

// TestEveryPathArgumentIsResolvedByTheOneHelper keeps the resolution of a path a
// caller named from acquiring a second home.
//
// One rule decides where a path argument may reach on this surface, and which
// answer it gives depends on the transport the session speaks: a client that
// launched this process names paths on the machine it is sitting at, and a caller
// reaching a listener names its subject inside the one workspace that process
// serves. toolWorkspace.workspacePath is where that rule is written and where the
// refusal sentence is composed.
//
// The failure this guards is not a wrong rule written on purpose. It is a later
// call site joining a value onto the workspace root itself, because that was the
// nearer thing to write, and thereby giving one argument a reach nobody decided to
// give it. Nothing about that mistake fails to compile, and the argument behaves
// correctly on every input a test is likely to send, because a test sends paths
// that are inside the workspace.
//
// 🔴 It checks BOTH ways past the helper, and the second is the one a check shaped
// around calls alone misses. A function HELD AS A VALUE — assigned into a
// dispatch table, stored in a struct field, passed as an argument — carries no call
// site at all, so the label check below would never see it and the arguments it
// went on to resolve would be judged by nothing.
//
// It FAILS rather than reports. A Go package that passes prints nothing at all —
// not a log line, not standard error, not standard output — so a guard that only
// reported what it found would be inert in every routine test run whatever it
// found.
//
// 🔴 What it cannot see, because a guard's reach is part of its contract:
//
//   - An argument this package hands to another package WITHOUT resolving it,
//     which that package then resolves for itself. 'key_path' is exactly that, on
//     the three tools that sign: where a call may carry it at all — which is a
//     caller at this machine's keyboard, since a caller reaching a listener is
//     refused for sending it before anything is resolved — it is passed to the
//     publication layer as it arrived. That is a decision taken elsewhere about
//     credentials rather than an omission here, and no walk of this package's
//     source can distinguish the two.
//   - A call site in another package. The walk covers this package's own
//     production source, which is where the wire boundary is.
func TestEveryPathArgumentIsResolvedByTheOneHelper(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package source: %v", err)
	}

	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		node, perr := parser.ParseFile(fset, file, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", file, perr)
		}
		parsed[file] = node
	}

	// The tool a call names is a package constant at several sites, so that the
	// tool's advertised name has one home rather than being spelled again here.
	// The constants are collected across the whole package first, because the file
	// declaring one is not the file using it.
	constants := stringConstants(parsed)

	seen := map[string]bool{}
	exercised := map[string]bool{}
	visited := 0
	for _, file := range parsedInFixedOrder(parsed) {
		node := parsed[file]
		visited++

		// Every expression that is the function OF a call, collected first so a
		// bare method value is distinguishable from a call below.
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
			// tracked during the walk, so the legitimate direct uses of the
			// resolvers — inside the functions that ARE this surface's boundaries —
			// are exempt by what they are rather than by which file they happen to
			// sit in.
			_, atABoundary := boundaryFunctions[fn.Name.Name]

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, isSel := n.(*ast.SelectorExpr)
				if !isSel {
					return true
				}
				pkg, isPkg := sel.X.(*ast.Ident)

				if isPkg {
					for _, name := range resolversReachedOnlyAtABoundary[pkg.Name] {
						if sel.Sel.Name != name {
							continue
						}
						if atABoundary {
							exercised[fn.Name.Name] = true
							break
						}
						t.Errorf("%s: %s.%s reached directly in %s — resolve the path through "+
							"toolWorkspace.workspacePath, which is the one place this surface decides how far a "+
							"path a caller named may reach and the one place its refusal is written",
							fset.Position(sel.Pos()), pkg.Name, name, fn.Name.Name)
						return true
					}
				}

				if sel.Sel.Name != "workspacePath" {
					return true
				}
				if !called[ast.Expr(sel)] {
					t.Errorf("%s: workspacePath used as a value rather than called, in %s — call it directly at "+
						"each site, because a resolver held as a value carries no call site for this guard to "+
						"read and the arguments it goes on to resolve would be judged by nothing",
						fset.Position(sel.Pos()), fn.Name.Name)
				}
				return true
			})

			// The labels are read off the calls themselves, which is where the
			// argument list is.
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || sel.Sel.Name != "workspacePath" {
					return true
				}
				tool, toolOK := staticString(call.Args, 0, constants)
				argument, argOK := staticString(call.Args, 1, constants)
				if !toolOK || !argOK {
					t.Errorf("%s: workspacePath called with a computed tool or argument name in %s — pass each as "+
						"a string literal or a package constant, so the sanctioned set names an argument rather "+
						"than a variable", fset.Position(call.Pos()), fn.Name.Name)
					return true
				}
				label := tool + ": " + argument
				if _, sanctioned := workspacePathArguments[label]; !sanctioned {
					t.Errorf("%s: workspacePath resolves %q, which is not a sanctioned path argument.\n"+
						"Every argument that names a path is listed in workspacePathArguments in this file, "+
						"together with what it names. If %q genuinely names a path, add it there in the same "+
						"change — that entry is the decision that an argument reaches the filesystem, and it is "+
						"not the caller's to take. If it does not name a path, it does not belong here at all.",
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
	// still agree on a count: a renamed argument, or a renamed helper, would
	// silently drop a path argument out of this guard's reach while leaving the
	// tally right.
	var missing []string
	for label := range workspacePathArguments {
		if !seen[label] {
			missing = append(missing, label)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("sanctioned path arguments that no call site resolves: %v\n"+
			"Either the tool or the argument was renamed, in which case update the label here in the same "+
			"change; or the helper itself was renamed, which drops EVERY entry at once; or the argument no "+
			"longer names a path, in which case drop its entry — a permission nothing exercises reads as one "+
			"something does.", missing)
	}

	// Each boundary function must have been FOUND reaching a resolver, by the same
	// set difference and for the same reason: an exemption keyed by a name that no
	// longer declares anything would pass over an empty walk of it, and would then
	// quietly cover whatever a later function of that name came to resolve.
	var idle []string
	for name := range boundaryFunctions {
		if !exercised[name] {
			idle = append(idle, name)
		}
	}
	sort.Strings(idle)
	if len(idle) > 0 {
		t.Errorf("boundary functions the walk did not find reaching a resolver: %v\n"+
			"Either the function was renamed, in which case update its name in boundaryFunctions in the same "+
			"change; or it no longer reaches a resolver directly, in which case drop its entry — an exemption "+
			"nothing exercises reads as one something does.", idle)
	}
}

// stringConstants collects the package's string constants by name, so a call
// naming its tool through the constant that already holds that name is judged
// against the same value a literal would be.
func stringConstants(files map[string]*ast.File) map[string]string {
	out := map[string]string{}
	for _, node := range files {
		for _, decl := range node.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, isValue := spec.(*ast.ValueSpec)
				if !isValue || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					if value, isLiteral := stringLiteral(vs.Values[i]); isLiteral {
						out[name.Name] = value
					}
				}
			}
		}
	}
	return out
}

// staticString reads args[i] as a value fixed at parse time: a string literal, or
// an identifier naming a package string constant. Anything else is not something
// the sanctioned set can be keyed by.
func staticString(args []ast.Expr, i int, constants map[string]string) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	if value, ok := stringLiteral(args[i]); ok {
		return value, true
	}
	if ident, ok := args[i].(*ast.Ident); ok {
		value, known := constants[ident.Name]
		return value, known
	}
	return "", false
}

// stringLiteral returns the value of a string-literal expression.
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return unquoted, true
}

// parsedInFixedOrder walks the files in a fixed order, so a failure names the
// same site first on every run.
func parsedInFixedOrder(files map[string]*ast.File) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
