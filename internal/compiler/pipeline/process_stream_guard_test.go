// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline_test

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

// advisoryLibraryDirs are the library packages whose advisories a run reports,
// relative to this package's directory: the run itself, the catalog view it
// resolves entities through, the plugin resolution it loads guests through, and
// the schema checks the view asks. Each writes what it has to say where its
// caller says — a display writer, a notice writer, a receiver — or returns it.
var advisoryLibraryDirs = []string{
	".",
	"../../../pkg/catalog",
	"../../../pkg/pluginpath",
	"../../../pkg/validator",
}

// TestNoAdvisoryLibraryNamesAProcessStream holds the library packages a run's
// advisories come from to naming no process stream of their own: no reference to
// the process's standard error or standard output, and no import of the log
// package, in any of their non-test sources. A library that wrote to the process
// stream would reach a terminal reader whatever surface it ran under — a
// long-running process's launcher instead of its editor — and would reach no
// reader of the run's advisory channel at all.
//
// It reads the syntax rather than the text, so a sentence in a comment that names
// the stream is not a finding and a reference written any way the language allows
// is.
//
// 🔴 PLANTED DEFECT: put back any one of the writes this replaced — the lint, the
// refusal, the trace-close, the static-analysis or the render-warning display in
// the run, either governance verdict display, the provider-binding display, a
// catalog view's extends or skip notice, the unrecorded-identity advisory's
// writer, or the schema check's log line — and this fails naming the file and
// line.
func TestNoAdvisoryLibraryNamesAProcessStream(t *testing.T) {
	var findings []string
	inspected := 0
	for _, dir := range advisoryLibraryDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			inspected++
			findings = append(findings, processStreamUses(fset, file)...)
		}
	}
	if inspected == 0 {
		t.Fatal("no source file was inspected, so a clean result would say nothing")
	}
	sort.Strings(findings)
	for _, f := range findings {
		t.Errorf("a library package names a process stream: %s", f)
	}
}

// processStreamUses reports each import of the log package and each reference to
// the os package's Stderr or Stdout in file, under whatever name file imports os.
func processStreamUses(fset *token.FileSet, file *ast.File) []string {
	var out []string
	osNames := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		switch path {
		case "log":
			out = append(out, fset.Position(imp.Pos()).String()+": imports the log package")
		case "os":
			name := "os"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			osNames[name] = true
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || !osNames[pkg.Name] {
			return true
		}
		if sel.Sel.Name == "Stderr" || sel.Sel.Name == "Stdout" {
			out = append(out, fset.Position(sel.Pos()).String()+": names "+pkg.Name+"."+sel.Sel.Name)
		}
		return true
	})
	return out
}
