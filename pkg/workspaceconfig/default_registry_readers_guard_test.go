// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestTheDefaultRegistryIsReadOnlyThroughItsLocation holds every reader of the
// default registry outside this package to DefaultRegistryLocation.
//
// The resolved default is a registry location — a host, optionally followed by a
// repository path — and the two parts do different jobs: the host is what every
// trust decision is keyed on, and the path is where things are published and
// fetched. A reader that took the field as it stands would carry the path into
// whatever it did with the value: a trust lookup keyed on it, a registry client
// constructed from it, or a location composed from it that the other side
// composes differently — which is how a publication and a resolution through the
// same default come to address two places. Reading it through the one accessor
// makes the reader say which part it wants.
//
// So the enforcement is syntactic: no non-test Go file outside this package
// selects the DefaultRegistry field at all. It needs no type information, so it
// matches a selection of any value's DefaultRegistry — a field of that name on
// another type would be reported too, and would be the moment to rename one of
// them. It cannot see a reader handed the value by a caller that did read it here,
// and it does not judge which part a caller of the accessor takes; the census
// behind each reader's choice is what its call site says.
func TestTheDefaultRegistryIsReadOnlyThroughItsLocation(t *testing.T) {
	root := moduleRootForGuard(t)
	own, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	var offenders []string
	inPackage := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // a file this parser cannot read is reported by the compiler, not here
		}
		inside := filepath.Dir(path) == own
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "DefaultRegistry" {
				return true
			}
			if inside {
				inPackage++
				return true
			}
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, filepath.ToSlash(rel)+":"+strconv.Itoa(fset.Position(sel.Pos()).Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Calibration: the resolution and the accessor read the field in this package,
	// so a walk that saw none of those reads saw nothing and would pass anything.
	if inPackage == 0 {
		t.Fatal("the walk found no selection of DefaultRegistry even in the package that declares it; it matched nothing and would agree with any tree")
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these read the default registry's field directly rather than through DefaultRegistryLocation:\n  %s\n"+
			"Take .Host for anything keyed on the registry — a trust policy, a login, an index, a registry client — and\n"+
			".Prefix() for a location composed through the default, so the repository path the default may carry\n"+
			"reaches only what is published and fetched.", strings.Join(offenders, "\n  "))
	}
}

// moduleRootForGuard returns the directory holding the module's go.mod, above the
// test's working directory.
func moduleRootForGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, serr := os.Stat(filepath.Join(dir, "go.mod")); serr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory; the walk below would have no root")
		}
		dir = parent
	}
}
