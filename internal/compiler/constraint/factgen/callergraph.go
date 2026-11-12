// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// A call through a name the same body binds to a function resolves to the
// functions that body names, which is what keeps a call attributed to the
// operation the source names when a body holds the operation in a variable so a
// wrapping one can stand in its place. The reasoning is local by construction: the
// binding and the call sit in one body, and the right-hand side is a function name
// resolved exactly as a call's own qualifier is. A binding whose right-hand side
// names something other than a production declaration this read found — a closure,
// a method value, a call's result, a value supplied from elsewhere — leaves that
// call unresolved beside whatever functions the name does hold, so a body that can
// reach an operation this read cannot see says so rather than resting on the
// binding it could see.
//
// What cannot be resolved is a call through a value the read cannot name that way:
// a method on a receiver, a function held in a field, one supplied as a parameter,
// or one reached through an interface. Such an edge is recorded as unresolved and
// followed nowhere. The consequence is stated wherever a reach derived from this
// read is reported: a reach is a lower bound on what an entry can touch, never an
// upper one.

// callEdge is one call a function makes.
type callEdge struct {
	// Target is the key of the called function's declaration, empty when the call
	// is through a value this read cannot resolve.
	Target string

	// Pos is where the call sits, which is what places it before or after a cut.
	Pos token.Pos

	// ViaValue reports that the target comes from a name the containing body binds
	// to a function rather than from the call's own expression. Such a target is
	// checked against the declarations the read found before it is followed: the
	// binding's right-hand side is a name, and a name that declares no production
	// function must leave the call unresolved rather than pointing at a key nothing
	// declares.
	ViaValue bool
}

// valueBinding is what one name a body binds to a function value can hold at a
// call through that name.
type valueBinding struct {
	// Targets are the declaration keys the body names, in the order the body names
	// them, each once.
	Targets []string

	// Opaque reports that the body also binds the name to something this read
	// cannot name: a function literal, a method value, a call's result, a value
	// supplied from elsewhere. A call through such a name carries an unresolved
	// edge beside its named targets, so the reach stays a lower bound rather than
	// resting on the bindings the read could see.
	Opaque bool
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
	// An edge taken from a name a body binds is checked against the declarations
	// the whole read found, which is a question no single body can answer while it
	// is being walked. A binding naming something that is not a production function
	// declaration — a value of the same name, a declaration in a package this read
	// walks past — leaves its call unresolved, so the reach rests on declarations
	// rather than on spellings.
	for _, key := range s.Order {
		node := s.Nodes[key]
		for i, e := range node.Calls {
			if e.ViaValue && e.Target != "" && s.Nodes[e.Target] == nil {
				node.Calls[i].Target = ""
			}
		}
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
	bound := collectValueBindings(body, dir, scope)
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
			if ident, ok := stripTypeArgs(v.Fun).(*ast.Ident); ok {
				if binding, held := bound[ident.Name]; held {
					for _, target := range binding.Targets {
						node.Calls = append(node.Calls, callEdge{Target: target, Pos: v.Pos(), ViaValue: true})
					}
					if binding.Opaque {
						node.Calls = append(node.Calls, callEdge{Pos: v.Pos()})
					}
					return true
				}
				// A name the caller supplied holds whatever the caller passed, which
				// is a value rather than a declaration — and reading it as a call into
				// a same-package declaration of that name would attribute the reach of
				// a function the parameter merely shadows.
				if params[ident.Name] {
					node.Calls = append(node.Calls, callEdge{Pos: v.Pos()})
					return true
				}
			}
			node.Calls = append(node.Calls, callEdge{Target: resolveTarget(v.Fun, dir, scope), Pos: v.Pos()})
		}
		return true
	})
}

// collectValueBindings is, per name one body binds to a function this read can
// name, what that name can hold — the functions the body names, and whether it
// also holds something this read cannot name.
//
// A name the body binds only to things it cannot name is left out entirely rather
// than carried as wholly opaque. The two readings differ only for a call through
// such a name, and leaving it out keeps this derivation from narrowing a reach: a
// name bound in one block and a same-package function of that name called in
// another are one name here, and dropping the call would lose an edge the source
// does make.
//
// The whole body is one scope for this purpose, function literals included, for
// the same reason a call inside a literal belongs to the enclosing function: the
// literal is that function's own code. A name bound in two disjoint blocks is
// therefore read as one name holding both, which widens a reach rather than
// narrowing it — and a widened reach is the direction this read already states as
// its bound.
func collectValueBindings(body *ast.BlockStmt, dir string, scope map[string]string) map[string]*valueBinding {
	bound := map[string]*valueBinding{}
	bind := func(name string, expr ast.Expr) {
		if name == "" || name == "_" {
			return
		}
		binding := bound[name]
		if binding == nil {
			binding = &valueBinding{}
			bound[name] = binding
		}
		target, named := namedFunction(expr, dir, scope)
		if !named {
			binding.Opaque = true
			return
		}
		for _, held := range binding.Targets {
			if held == target {
				return
			}
		}
		binding.Targets = append(binding.Targets, target)
	}
	bindPairs := func(names, values []ast.Expr) {
		for i, name := range names {
			ident, ok := name.(*ast.Ident)
			if !ok {
				continue
			}
			switch {
			case len(values) == len(names):
				bind(ident.Name, values[i])
			case len(values) > 0:
				// One expression filling several names is a call or a type assertion,
				// so no name in it holds a function this read can name.
				bind(ident.Name, nil)
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if v.Tok != token.ASSIGN && v.Tok != token.DEFINE {
				return true
			}
			bindPairs(v.Lhs, v.Rhs)
		case *ast.DeclStmt:
			decl, ok := v.Decl.(*ast.GenDecl)
			if !ok || decl.Tok != token.VAR {
				return true
			}
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				names := make([]ast.Expr, 0, len(value.Names))
				for _, name := range value.Names {
					names = append(names, name)
				}
				bindPairs(names, value.Values)
			}
		}
		return true
	})
	for name, binding := range bound {
		if len(binding.Targets) == 0 {
			delete(bound, name)
		}
	}
	if len(bound) == 0 {
		return nil
	}
	return bound
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
	key, _ := namedFunction(fun, dir, scope)
	return key
}

// namedFunction is the declaration key an expression names, and whether it names
// one at all.
//
// A bare identifier names a declaration in the package the expression sits in; a
// qualified one names a declaration in the package its qualifier imports. Every
// other shape — a literal, a call's result, a method value, a field — names no
// declaration this read can follow, and the second return is what lets a caller
// tell that apart from a name it resolved.
func namedFunction(expr ast.Expr, dir string, scope map[string]string) (string, bool) {
	if expr == nil {
		return "", false
	}
	switch e := stripTypeArgs(expr).(type) {
	case *ast.Ident:
		return dir + "::" + e.Name, true
	case *ast.SelectorExpr:
		if base, ok := e.X.(*ast.Ident); ok {
			if target, ok := scope[base.Name]; ok {
				return target + "::" + e.Sel.Name, true
			}
		}
		return "", false
	default:
		return "", false
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
