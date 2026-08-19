// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sync"
)

// The call read: every production function, the calls it makes, and where a call
// sits relative to a cut the caller controls.
//
// It exists because the caller question — which surfaces reach which stages —
// cannot be answered from call sites alone. A stage's rules are invoked deep
// inside the compiler, and the surface a user reaches calls an entry several hops
// above that, so the relation between the two is a path through the call graph
// rather than a name at a call site.
//
// # How a call is attributed, and why proximity is not used
//
// A call belongs to the function whose body syntactically contains it, found by
// walking each function declaration's own body — including any function literal
// declared inside it, because a continuation the function passes is still that
// function's own code. Attribution by nearest preceding declaration would be a
// guess that reads correctly on tidy source and silently mis-attributes on source
// that is not.
//
// # How a call's target is resolved, and the bound that follows
//
// A bare call resolves to a declaration in the calling file's own package. A
// package-qualified call resolves through that file's own import list, so the
// qualifier names a directory rather than a bare identifier — without which two
// packages declaring the same function name would be conflated, and a caller of
// either would inherit the reach of both.
//
// What cannot be resolved is a call through a value: a method on a receiver, a
// function held in a field or a variable, or one reached through an interface.
// Such an edge is recorded as unresolved and followed nowhere. The consequence is
// stated wherever a reach derived from this read is reported: a reach is a lower
// bound on what an entry can touch, never an upper one.

// callEdge is one call a function makes.
type callEdge struct {
	// Target is the key of the called function's declaration, empty when the call
	// is through a value this read cannot resolve.
	Target string

	// Pos is where the call sits, which is what places it before or after a cut.
	Pos token.Pos
}

// traversalCall is one call a function makes to a framework traversal entry.
type traversalCall struct {
	// Entry is the traversal entry called.
	Entry string

	// Check is the check the call names, empty when the argument is not a name
	// this syntactic read can attribute to a declaration.
	Check string

	// Pos is where the call sits, which is what places it before or after a cut.
	Pos token.Pos
}

// conditionalCut is a place where a function stops early on a value its caller
// supplied: an `if` over a field of one of the function's own parameters whose
// body returns.
//
// It is what makes a reach past that point conditional on the call rather than a
// property of the call graph, and naming the field is what lets the report say
// which value decides.
type conditionalCut struct {
	// Field is the parameter field the condition reads.
	Field string

	// Pos is where the cut sits.
	Pos token.Pos
}

// funcNode is one production function declaration.
type funcNode struct {
	// Key identifies the declaration as `directory::name`, with a method's
	// receiver type in the name so that a method and a function sharing a name in
	// one package stay distinct.
	Key string

	// Dir is the package directory declaring it.
	Dir string

	// File is the file declaring it, relative to the module root.
	File string

	// Name is the declaration's own name, a method's carrying its receiver type.
	Name string

	// Exported reports whether another package can name it.
	Exported bool

	// Calls are the calls it makes, in source order.
	Calls []callEdge

	// Traversals are the framework traversal entries it calls.
	Traversals []traversalCall

	// Cuts are the early returns it takes on a value its caller supplied.
	Cuts []conditionalCut
}

// callState is the production call graph.
type callState struct {
	// Nodes are every production function declaration, keyed as funcNode.Key.
	Nodes map[string]*funcNode

	// Order is the keys in walk order, so every projection of this read is
	// deterministic without sorting at each use.
	Order []string
}

// readCallState derives the production call graph once per process.
func readCallState() (*callState, error) {
	callOnce.Do(func() {
		callRead, callErr = walkCallState()
	})
	return callRead, callErr
}

var (
	callOnce sync.Once
	callRead *callState
	callErr  error
)

func walkCallState() (*callState, error) {
	m, err := readModuleSource()
	if err != nil {
		return nil, err
	}
	inv, err := readInvocationState()
	if err != nil {
		return nil, err
	}
	entries := map[string]bool{}
	for _, e := range inv.Entries {
		entries[e] = true
	}

	s := &callState{Nodes: map[string]*funcNode{}}
	for _, p := range m.Files {
		scope := m.packageDirsInScope(p.File)
		for _, decl := range p.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			node := &funcNode{
				Dir:      p.Dir,
				File:     p.Path,
				Name:     declaredName(fn),
				Exported: fn.Name.IsExported(),
			}
			node.Key = p.Dir + "::" + node.Name
			params := parameterNames(fn)
			collectCalls(fn.Body, p.Dir, scope, params, entries, node)
			// A package may declare one name once, so a key already present is a
			// second declaration of the same function — which the compiler would
			// have refused. Keeping the first is the honest reading of a tree that
			// somehow carries both.
			if _, seen := s.Nodes[node.Key]; !seen {
				s.Nodes[node.Key] = node
				s.Order = append(s.Order, node.Key)
			}
		}
	}
	if len(s.Nodes) == 0 {
		return nil, fmt.Errorf(
			"the call read found no production function under %s, so every reach derived from it "+
				"would be empty for want of a graph rather than for want of a path", m.Root)
	}
	return s, nil
}

// declaredName is a function declaration's name, a method's qualified by its
// receiver type so that a method and a function sharing a name stay distinct.
func declaredName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := baseIdent(fn.Recv.List[0].Type)
	if recv == "" {
		return fn.Name.Name
	}
	return recv + "." + fn.Name.Name
}

// parameterNames is the set of identifiers a function's caller supplies: its
// parameters and its receiver.
func parameterNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	add := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	add(fn.Recv)
	if fn.Type != nil {
		add(fn.Type.Params)
	}
	return out
}

// collectCalls walks one function body, recording the calls it makes, the
// traversal entries it invokes, and the cuts it takes on a caller-supplied value.
func collectCalls(body *ast.BlockStmt, dir string, scope map[string]string, params map[string]bool, entries map[string]bool, node *funcNode) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IfStmt:
			if field, ok := callerControlledCut(v, params); ok {
				node.Cuts = append(node.Cuts, conditionalCut{Field: field, Pos: v.Pos()})
			}
		case *ast.CallExpr:
			name := baseIdent(v.Fun)
			if entries[name] {
				// Every traversal entry takes the check last, which is what lets the
				// check be read without resolving the signature: the argument in that
				// position is the check by the framework's own shape.
				var named string
				if k := len(v.Args); k > 0 {
					named = baseIdent(v.Args[k-1])
				}
				node.Traversals = append(node.Traversals, traversalCall{Entry: name, Check: named, Pos: v.Pos()})
			}
			node.Calls = append(node.Calls, callEdge{Target: resolveTarget(v.Fun, dir, scope), Pos: v.Pos()})
		}
		return true
	})
}

// callerControlledCut reports whether an `if` stops the function early on a field
// of one of its own parameters, and which field.
//
// Both halves are required. The condition must read a field of a caller-supplied
// value, which is what makes the outcome the caller's rather than the code's; and
// the body must end in a return, which is what makes it a cut rather than a
// branch the function continues past. A condition comparing a value — an error
// against nil, a function reference against nil — is not a cut: it is a fault
// path or an optional step, not a caller choosing a depth.
func callerControlledCut(stmt *ast.IfStmt, params map[string]bool) (string, bool) {
	sel, ok := stmt.Cond.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	base, ok := sel.X.(*ast.Ident)
	if !ok || !params[base.Name] {
		return "", false
	}
	if stmt.Body == nil || len(stmt.Body.List) == 0 {
		return "", false
	}
	if _, ok := stmt.Body.List[len(stmt.Body.List)-1].(*ast.ReturnStmt); !ok {
		return "", false
	}
	return sel.Sel.Name, true
}

// resolveTarget is the declaration key a call names, empty when the call goes
// through a value this read cannot follow.
func resolveTarget(fun ast.Expr, dir string, scope map[string]string) string {
	switch e := stripTypeArgs(fun).(type) {
	case *ast.Ident:
		return dir + "::" + e.Name
	case *ast.SelectorExpr:
		if base, ok := e.X.(*ast.Ident); ok {
			if target, ok := scope[base.Name]; ok {
				return target + "::" + e.Sel.Name
			}
		}
		return ""
	default:
		return ""
	}
}

// stripTypeArgs reaches the expression an explicit type argument list wraps.
func stripTypeArgs(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return stripTypeArgs(e.X)
	case *ast.IndexListExpr:
		return stripTypeArgs(e.X)
	default:
		return expr
	}
}

// traversalCallSites returns each call to a traversal entry within one file,
// together with the check the call names where that is a name at the call site.
//
// It projects the call read rather than walking the file a second time, so the
// site listing and the reach derivation cannot disagree about which calls exist.
// A traversal call outside any function body would be invisible to that read, so
// the projection is cross-checked against a whole-file count and refuses on a
// disagreement rather than reporting the smaller number.
func traversalCallSites(p sourceFile, entries map[string]bool) ([]invocationSite, error) {
	var out []invocationSite
	for _, decl := range p.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			// The called function's own name, with any package qualifier and any
			// explicit type arguments stripped, so the framework's unqualified call
			// and an importing package's qualified one read the same.
			entry := baseIdent(call.Fun)
			if !entries[entry] {
				return true
			}
			var named string
			if n := len(call.Args); n > 0 {
				named = baseIdent(call.Args[n-1])
			}
			out = append(out, invocationSite{Entry: entry, File: p.Path, Check: named})
			return true
		})
	}
	if total := countTraversalCalls(p.File, entries); total != len(out) {
		return nil, fmt.Errorf(
			"%s holds %d call(s) to a traversal entry and %d of them sit inside a function body; "+
				"the reach derivation follows calls from one function to another, so a call outside "+
				"every body has no caller to attribute it to and reporting the smaller number would "+
				"undercount the sites silently",
			filepath.ToSlash(p.Path), total, len(out))
	}
	return out, nil
}
