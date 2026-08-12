// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestHelperNameBindingChokepointGuard enforces that an authored helper name is
// bound to a function in exactly one place. The helper functions themselves are
// unexported, so no package outside this one can reuse an existing helper under a
// different name — that much the compiler already refuses. What it cannot refuse
// is a package writing its OWN function and registering it under a name this
// package already defines, which is how one authored name came to carry two
// semantics: a template surface elsewhere bound `optional` to a local
// implementation that coerced non-string values, while this package's bound the
// value through with its type intact. Both compiled, both looked right at their
// own call site, and the disagreement was reachable only by an author who wrote
// the same expression on two surfaces and got two answers.
//
// The guard is deny-by-default over a whole-module walk: it fails when any
// non-test Go file outside this package binds one of this package's helper names
// as a template-function key, either as a composite-literal key
// (`template.FuncMap{"optional": …}`) or as an index assignment
// (`funcs["optional"] = …`). A package wanting Aggrega's helpers takes the whole
// set from [Helpers] or [FuncMap] and never names the key, so a legitimate
// consumer cannot trip this.
//
// 🔴 The covered name set is DERIVED from Helpers() at run time, never listed
// here. A hand-written list of helper names is a second copy of the registration,
// and the copy is what goes stale: a helper added tomorrow would be silently
// uncovered on the day it is added, which is precisely when the guard is worth
// having.
//
// Test files are exempt: a test legitimately builds a deliberately-divergent
// function set to prove a surface's behavior, and that independence is the point
// of such a test.
func TestHelperNameBindingChokepointGuard(t *testing.T) {
	names := map[string]bool{}
	for name := range Helpers() {
		names[name] = true
	}
	if len(names) == 0 {
		t.Fatal("derived no helper name from Helpers() — the derivation is broken, not the source")
	}

	// A planted needle proves the matcher fires before any zero is trusted. Both
	// binding shapes the walk relies on are planted; a matcher that found neither
	// would report a clean module while saying nothing about it.
	needle := `package p

import "text/template"

func f() template.FuncMap {
	m := template.FuncMap{"optional": nil}
	m["optional"] = nil
	return m
}
`
	if got := helperNameBindings(t, "needle.go", needle, names); len(got) != 2 {
		t.Fatalf("matcher calibration: planted 2 bindings of a derived helper name, matcher found %d — a zero over the module would mean nothing", len(got))
	}

	root := filepath.Join("..", "..")
	home, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs .: %v", err)
	}

	walked := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			abs, aerr := filepath.Abs(p)
			if aerr != nil {
				return aerr
			}
			base := d.Name()
			isRoot := filepath.Clean(p) == filepath.Clean(root)
			if abs == home || base == "vendor" || base == "testdata" ||
				(!isRoot && strings.HasPrefix(base, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		walked++
		// A nil source makes the parser read the file itself.
		for _, b := range helperNameBindings(t, p, nil, names) {
			t.Errorf("%s: binds the template-function name %q outside its home — take the whole helper set from gotmpl.Helpers() or gotmpl.FuncMap() instead of registering an implementation of your own under a name Aggrega already defines", b.at, b.name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if walked == 0 {
		t.Fatal("walked no production Go file — the walk is broken, so its clean result means nothing")
	}
	t.Logf("inspected %d non-test Go files for bindings of %d derived helper name(s)", walked, len(names))
}

type helperBinding struct {
	name string
	at   string
}

// helperNameBindings reports every site in src that binds one of names as a
// template-function key: a string-literal key in a composite literal, or an index
// assignment with a string-literal index. Both are the shapes a function set is
// populated through; a consumer taking the whole set writes neither. A nil src
// makes the parser read path from disk.
func helperNameBindings(t *testing.T, path string, src any, names map[string]bool) []helperBinding {
	t.Helper()
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var found []helperBinding
	record := func(key ast.Expr, pos token.Pos) {
		lit, ok := key.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		name, uerr := strconv.Unquote(lit.Value)
		if uerr != nil || !names[name] {
			return
		}
		found = append(found, helperBinding{name: name, at: fset.Position(pos).String()})
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			for _, elt := range v.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					record(kv.Key, kv.Pos())
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				if idx, ok := lhs.(*ast.IndexExpr); ok {
					record(idx.Index, idx.Pos())
				}
			}
		}
		return true
	})
	return found
}
