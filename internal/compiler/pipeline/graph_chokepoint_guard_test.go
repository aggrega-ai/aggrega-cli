// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// graphConstructionHomes maps each entry that can produce a compiler graph to
// the complete set of non-test files permitted to call it, relative to the
// module root.
//
// Two names rather than one, because they close different halves. The builder
// is the primitive: it turns a flattened composition into a graph, and it is
// reachable from any package under this module, so nothing in the language
// stops a surface calling it directly — which is what two command-layer
// surfaces did. The chokepoint is the entry that runs the whole sequence around
// that primitive; it is exported because those surfaces need it, and being
// exported it is a second door that has to be watched for the same reason the
// first one is.
//
// The map is the specification, not a record of what happens to be there. A new
// entry here is a deliberate decision that a surface may construct a graph,
// taken by editing this file — which is the point: the edit is what puts the
// decision in front of a reviewer.
var graphConstructionHomes = map[string][]string{
	"BuildIRContext": {
		"internal/compiler/pipeline/pipeline.go",
	},
	"BuildGraph": {
		"cmd/aggrega/inspect.go",
		"cmd/aggrega/pull.go",
		"internal/compiler/pipeline/pipeline.go",
	},
}

// TestGraphConstructionHasOneChokepoint asserts that a compiler graph is built
// only where this file says it may be.
//
// The failure it exists to catch produces no symptom at the call site. A
// surface that assembles resolve, flatten and build for itself gets a graph —
// so nothing errors, nothing warns, and the code reads as though it went
// through the pipeline. What differs is what the graph SAYS: the sequence the
// chokepoint runs contributes the label transform and reads the build-identity
// stamp off the inputs, and a hand-assembled sequence contributes whichever of
// those its author remembered. Both assemblies that existed ran no label
// transform, and both took the stamp from their own command layer — so a stamp
// set on the inputs would have been silently ignored on those two surfaces and
// honoured everywhere else.
//
// The check is exact on both axes. An unlisted file means a surface built a
// graph outside the chokepoint. A LISTED file that no longer appears means the
// home moved and this guard did not follow it — equally a failure, because a
// guard whose corpus silently stopped containing what it guards reports a clean
// verdict over nothing at all. The module-root assertion below is the same
// concern one level up: it fails loudly rather than walking an empty tree.
//
// 🔴 COVERAGE BOUND. This matches CALL SYNTAX — an identifier or a selector in
// call position, keyed on the function's own name so an import alias cannot
// hide it. A call reached through a function VALUE (a variable, a struct field,
// a dispatch-table entry, an argument), through an interface method, or through
// reflection is invisible to it, because none of those name the function at the
// call site. So this guard bounds the direct route and no other; it is not, and
// cannot be, a proof that nothing else reaches the builder.
//
// Test files are exempt, deliberately. A test legitimately drives one stage in
// isolation from hand-assembled in-memory structures — there are two such tests
// at the compiler level, building a graph from a synthesised topology that no
// Release could produce — and forcing them through an entry that starts from a
// Release would mean they could no longer test the stage they are about.
func TestGraphConstructionHasOneChokepoint(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q carries no go.mod; the guard is looking in the wrong place, so a clean result here would mean nothing: %v", root, err)
	}

	// name -> file -> the call sites found in it, for the failure detail.
	sites := map[string]map[string][]string{}
	for name := range graphConstructionHomes {
		sites[name] = map[string][]string{}
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch {
			case d.Name() == "testdata", d.Name() == "vendor":
				return fs.SkipDir
			case path != root && strings.HasPrefix(d.Name(), "."):
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", rel, perr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calleeFuncName(call.Fun)
			if _, watched := sites[name]; !watched {
				return true
			}
			pos := fset.Position(call.Pos())
			sites[name][rel] = append(sites[name][rel], fmt.Sprintf("%s:%d", rel, pos.Line))
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk module: %v", walkErr)
	}

	for _, name := range sortedKeys(graphConstructionHomes) {
		want := append([]string(nil), graphConstructionHomes[name]...)
		sort.Strings(want)

		got := sortedKeys(sites[name])

		if !equalStrings(want, got) {
			var detail strings.Builder
			for _, file := range got {
				for _, site := range sites[name][file] {
					detail.WriteString("\n    " + site)
				}
			}
			t.Errorf(""+
				"%s must be called only from the files declared in graphConstructionHomes.\n"+
				"  want: %v\n"+
				"  got:  %v%s\n"+
				"A file present in got and absent from want built a graph outside the chokepoint: route it through pipeline.BuildGraph.\n"+
				"A file present in want and absent from got means the home moved and this guard did not follow it: update graphConstructionHomes in the same change.",
				name, want, got, detail.String())
		}
	}

	// The builder is called exactly once, by the chokepoint. A second call
	// inside the same file is a second graph construction, which the file-set
	// check above cannot see.
	const builderHome = "internal/compiler/pipeline/pipeline.go"
	if n := len(sites["BuildIRContext"][builderHome]); n != 1 {
		t.Errorf("%s must build the graph exactly once; got %d call site(s): %v",
			builderHome, n, sites["BuildIRContext"][builderHome])
	}
}

// calleeFuncName renders a call's callee as its own function name, dropping the
// package qualifier the call site wrote — "BuildGraph" for both a bare call
// inside the package and a qualified one from outside it.
//
// Keying on the bare name is what makes the guard blind to an import alias. It
// admits a same-named function from an unrelated package as collateral; that is
// accepted, because the resulting false positive is a visible failure naming a
// file and line, whereas an alias-sensitive matcher fails the other way and
// reports a clean tree.
func calleeFuncName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
