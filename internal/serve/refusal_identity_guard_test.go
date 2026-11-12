// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// failureMessageSinks are the functions here that build an error answer out of a
// message alone, with no failure beside it, keyed by name to the index of the
// message argument. A message rendered from an error and handed to one of them
// has lost the condition the error names before conditionRefusal can read it.
var failureMessageSinks = map[string]int{
	"writeError":              3,
	"invalidPublicationParam": 0,
}

// TestEveryErrorAnswerRenderedFromAFailureCarriesIt is the guard behind the
// refusal identity: an answer's condition is read off the failure it was rendered
// from, at the one place every envelope reaches the wire, so an answer that turns
// an error into text and drops the error answers with no identity however plainly
// its message names the condition — and nothing about that fails to compile or
// reads wrong in review.
//
// It holds four things, over this package's production source:
//
//   - no message handed to a function that builds an answer from a message alone
//     (failureMessageSinks) renders a failure — calls an Error method, or names an
//     identifier spelled as an error is spelled here (err, or ending in Err or
//     err): such a site answers through writeRefusal, or an envelope with the
//     failure as its cause;
//   - an envelope literal whose message renders a failure carries that failure as
//     its cause;
//   - no errorData literal sets either identifier, so conditionRefusal is the only
//     site that decides them;
//   - conditionRefusal is applied by writeErrorEnvelope and called from nowhere
//     else, so no answer can be identified twice or by a second rule.
//
// It FAILS rather than reports: a Go package that passes prints nothing, so a
// guard that only reported would be inert in every routine run.
//
// 🔴 What it cannot see, stated because a guard's reach is part of its contract: a
// failure held under a name spelled unlike an error, and rendered without its
// Error method — through a formatting verb, say — reaches a sink unnoticed. The
// behaviour test beside it drives one refusal of each kind through the dispatch,
// which is the half that reads what a client receives.
func TestEveryErrorAnswerRenderedFromAFailureCarriesIt(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package source: %v", err)
	}
	visited, refusals, sinks := 0, 0, 0
	appliedBy := map[string]int{}
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
		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					name := calledName(x)
					if name == "writeRefusal" {
						refusals++
					}
					if name == "conditionRefusal" {
						appliedBy[fn.Name.Name]++
					}
					if at, sink := failureMessageSinks[name]; sink && at < len(x.Args) {
						sinks++
						if rendersFailure(x.Args[at]) {
							t.Errorf("%s: %s in %s is handed a message rendered from a failure and not the failure itself, "+
								"so the condition it names is lost before the answer is identified — answer through "+
								"writeRefusal, or an envelope carrying the failure as its cause",
								fset.Position(x.Pos()), name, fn.Name.Name)
						}
					}
				case *ast.CompositeLit:
					id, ok := x.Type.(*ast.Ident)
					if !ok {
						return true
					}
					switch id.Name {
					case "jsonRPCError":
						message, cause := keyedValue(x, "Message"), keyedValue(x, "cause")
						if message != nil && rendersFailure(message) && cause == nil {
							t.Errorf("%s: an envelope in %s renders a failure into its message and does not carry it as "+
								"its cause, so the condition it names is never identified on the answer",
								fset.Position(x.Pos()), fn.Name.Name)
						}
					case "errorData":
						for _, key := range []string{"Code", "ShortCode"} {
							if keyedValue(x, key) != nil {
								t.Errorf("%s: an errorData literal in %s sets %s; the identity is decided by "+
									"conditionRefusal alone, from what the answer reports", fset.Position(x.Pos()), fn.Name.Name, key)
							}
						}
					}
				}
				return true
			})
		}
	}

	if visited == 0 {
		t.Fatal("the walk visited no production Go file in this package — the guard is inert, not the tree clean")
	}
	if refusals == 0 || sinks == 0 {
		t.Fatalf("the walk found %d writeRefusal call(s) and %d message-only answer(s); it cannot have read the "+
			"package's answers, so its silence would be a property of the walk", refusals, sinks)
	}
	if appliedBy["writeErrorEnvelope"] != 1 || len(appliedBy) != 1 {
		t.Errorf("conditionRefusal is applied by %v; want it applied once, by writeErrorEnvelope, and nowhere else — "+
			"it is the one place an answer's identity is decided", appliedBy)
	}
}

// calledName is the name a call is made by: the function's own, or a method's.
func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// keyedValue returns the value a composite literal gives key, or nil where it
// gives none.
func keyedValue(lit *ast.CompositeLit, key string) ast.Expr {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == key {
			return kv.Value
		}
	}
	return nil
}

// rendersFailure reports whether e renders an error to text: it calls an Error
// method taking nothing, or names an identifier spelled as an error is spelled in
// this package.
func rendersFailure(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(x.Args) == 0 {
				found = true
			}
		case *ast.Ident:
			if x.Name == "err" || strings.HasSuffix(x.Name, "Err") || strings.HasSuffix(x.Name, "err") {
				found = true
			}
		}
		return !found
	})
	return found
}
