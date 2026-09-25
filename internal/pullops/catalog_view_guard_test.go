// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

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

// catalogImportPath is the package whose whole-catalog loaders the pull must not
// call.
const catalogImportPath = "github.com/aggrega-ai/aggrega-cli/pkg/catalog"

// wholeCatalogLoaders derives, from the catalog package's own source, the set of
// names a caller would reach for to hold an eagerly loaded catalog: every exported
// package-level function returning a *Catalog, and the Catalog type itself. It is
// derived rather than listed, so a loader added to that package is covered the day
// it is added rather than the day somebody remembers this guard.
func wholeCatalogLoaders(t *testing.T) map[string]bool {
	t.Helper()
	dir := filepath.Join("..", "..", "pkg", "catalog")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the catalog package at %s: %v", dir, err)
	}
	names := map[string]bool{"Catalog": true}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Results == nil {
					continue
				}
				for _, result := range fn.Type.Results.List {
					if star, ok := result.Type.(*ast.StarExpr); ok {
						if id, ok := star.X.(*ast.Ident); ok && id.Name == "Catalog" {
							names[fn.Name.Name] = true
						}
					}
				}
			}
		}
	}
	return names
}

// TestPullReadsTheCatalogThroughOneView holds the pull to the one catalog view a
// build resolves through: production code in this package names no whole-catalog
// loader of the catalog package, and no Catalog value. The pull reads the catalog
// through the lazy view — the walk, the consistency check, the aggregate resolve
// and the failure annotation all ask it — and an eager whole-catalog load beside
// it would be a second view, free to select a different version or report a
// different refusal than the one the build reads the lockfile through.
//
// It matches the catalog package's selector through each file's own imports, so
// an aliased import is caught as surely as a plain one, and it catches a loader
// named as a value as well as one called.
func TestPullReadsTheCatalogThroughOneView(t *testing.T) {
	loaders := wholeCatalogLoaders(t)
	// Calibration: the derivation must hold the load this package once read the
	// catalog through, or it matched nothing and would agree with any source.
	if !loaders["LoadPassOneTolerant"] {
		t.Fatalf("the loader set derived from the catalog package does not hold LoadPassOneTolerant; "+
			"the derivation matched nothing and this guard would pass blind: %v", sortedNames(loaders))
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse this package's source: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no package source parsed in this directory; the guard would see nothing")
	}
	var found []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			local := catalogImportName(file)
			if local == "" {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == local && loaders[sel.Sel.Name] {
					pos := fset.Position(sel.Pos())
					found = append(found, filepath.Base(pos.Filename)+":"+strconv.Itoa(pos.Line)+": catalog."+sel.Sel.Name)
				}
				return true
			})
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("production code in the pull names a whole-catalog load of the catalog package; the pull reads the "+
			"catalog through the lazy view a build resolves through, and nothing else:\n  %s",
			strings.Join(found, "\n  "))
	}
}

// catalogImportName is the name file uses for the catalog package, or "" when it
// does not import it (or imports it blank or dot, which names nothing).
func catalogImportName(file *ast.File) string {
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != catalogImportPath {
			continue
		}
		if spec.Name == nil {
			return "catalog"
		}
		if spec.Name.Name == "_" || spec.Name.Name == "." {
			return ""
		}
		return spec.Name.Name
	}
	return ""
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
