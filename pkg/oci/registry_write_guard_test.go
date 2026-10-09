// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This guard holds every registry write in the module to the three retrying
// helpers in this package. A write that reaches the registry any other way has
// no retry, and nothing else would notice: it works against a healthy registry,
// and the first transient failure leaves a publication half-done.
//
// # What it recognises
//
// It parses every non-test .go file in the module — skipping testdata, vendor,
// node_modules, dot-directories and any nested module — and reports two call
// shapes, syntactically, with no type information:
//
//   - a call to a writing function of the oras root package
//     (oras.land/oras-go/v2), under whatever name the file imports it as:
//     PushBytes, PackManifest, Pack, Copy, CopyGraph, ExtendedCopy,
//     ExtendedCopyGraph, Tag, TagN, TagBytes, TagBytesN;
//   - a method call named Push or Tag with three arguments, or PushReference
//     or Mount with four, on anything that is not an imported package — the
//     shapes of oras's push, tag, reference-push and mount interface methods,
//     which is how a target or a repository is written to directly.
//
// Each is allowed only inside the helper that owns it (retryingHelpers), and
// anywhere else fails, naming the file and line. A dot-import of the oras root
// package fails too, because its calls could not be attributed.
//
// # What it cannot see
//
// A write reached through a function or method value (`push := target.Push`,
// then `push(...)`), or through an interface the call site names differently; a
// method call whose receiver is a variable named like a package the file
// imports, which reads as a package-qualified call; a write made by an oras
// function this list does not name, or by another library handed a target; a
// registry write made over HTTP without oras; deletions; and writes in test
// files or nested modules. Because it matches by shape, a method
// named Push or Tag with the same arity on a type that has nothing to do with a
// registry is reported too: rename it, or teach this guard to tell it apart —
// never silence it by widening the allowed set.
//
// It calibrates itself: each helper must be found making its own call, so a walk
// that matched nothing cannot pass by agreeing with an empty result.

// retryingHelpers are the only functions allowed to write to a registry, keyed
// `<package-relative-dir>.<function>`, with the one call each owns.
var retryingHelpers = map[string]string{
	"pkg/oci.pushBlob":     "oras.PushBytes",
	"pkg/oci.packManifest": "oras.PackManifest",
	"pkg/oci.tagManifest":  "method Tag/3",
}

// orasWritingFunctions are the functions of the oras root package that write to
// the target they are handed.
var orasWritingFunctions = map[string]bool{
	"PushBytes": true, "PackManifest": true, "Pack": true,
	"Copy": true, "CopyGraph": true, "ExtendedCopy": true, "ExtendedCopyGraph": true,
	"Tag": true, "TagN": true, "TagBytes": true, "TagBytesN": true,
}

// targetWritingMethods are the method names of oras's writing interfaces, with
// the argument count each takes.
var targetWritingMethods = map[string]int{"Push": 3, "Tag": 3, "PushReference": 4, "Mount": 4}

const orasRootImport = "oras.land/oras-go/v2"

var majorVersionElement = regexp.MustCompile(`^v[0-9]+$`)

// registryWriteCall is one recognised call: where it is and which shape it took.
type registryWriteCall struct {
	owner string
	shape string
	where string
}

func TestEveryRegistryWriteGoesThroughARetryingHelper(t *testing.T) {
	root := moduleRoot(t)
	var calls []registryWriteCall

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if p != root {
				if strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
				if _, serr := os.Stat(filepath.Join(p, "go.mod")); serr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil // a file this parser cannot read is reported by the compiler, not here
		}
		rel, _ := filepath.Rel(root, p)
		dir := filepath.ToSlash(filepath.Dir(rel))
		calls = append(calls, registryWriteCallsIn(fset, file, dir, filepath.ToSlash(rel))...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	foundOwn := map[string]bool{}
	var outside []string
	for _, c := range calls {
		if own, ok := retryingHelpers[c.owner]; ok && own == c.shape {
			foundOwn[c.owner] = true
			continue
		}
		outside = append(outside, c.where+": "+c.shape+" in "+c.owner)
	}

	var blind []string
	for helper := range retryingHelpers {
		if !foundOwn[helper] {
			blind = append(blind, helper+" ("+retryingHelpers[helper]+")")
		}
	}
	sort.Strings(blind)
	if len(blind) > 0 {
		t.Errorf("the walk did not find these helpers making their own call, so it cannot be trusted to find any other:\n  %s",
			strings.Join(blind, "\n  "))
	}

	sort.Strings(outside)
	if len(outside) > 0 {
		t.Errorf("these calls write to a registry outside the retrying helpers in pkg/oci, so a transient registry failure "+
			"is not retried there. Route each through pushBlob, packManifest or tagManifest:\n  %s",
			strings.Join(outside, "\n  "))
	}
}

// registryWriteCallsIn returns every recognised registry-write call in file,
// each attributed to the function declaration that encloses it, or to the
// package scope where no function does.
func registryWriteCallsIn(fset *token.FileSet, file *ast.File, dir, rel string) []registryWriteCall {
	orasNames := map[string]bool{}
	packageNames := map[string]bool{}
	var found []registryWriteCall
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		for _, name := range importedNames(spec, importPath) {
			packageNames[name] = true
			if importPath == orasRootImport {
				orasNames[name] = true
			}
		}
		if importPath == orasRootImport && spec.Name != nil && spec.Name.Name == "." {
			found = append(found, registryWriteCall{
				owner: dir + ".<imports>",
				shape: "dot-import of " + orasRootImport,
				where: rel + ":" + strconv.Itoa(fset.Position(spec.Pos()).Line),
			})
		}
	}

	inspect := func(owner string, node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			where := rel + ":" + strconv.Itoa(fset.Position(call.Pos()).Line)
			if x, isIdent := sel.X.(*ast.Ident); isIdent && packageNames[x.Name] {
				if orasNames[x.Name] && orasWritingFunctions[sel.Sel.Name] {
					found = append(found, registryWriteCall{owner: owner, shape: "oras." + sel.Sel.Name, where: where})
				}
				return true
			}
			if arity, writes := targetWritingMethods[sel.Sel.Name]; writes && len(call.Args) == arity {
				found = append(found, registryWriteCall{owner: owner, shape: "method " + sel.Sel.Name + "/" + strconv.Itoa(arity), where: where})
			}
			return true
		})
	}

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if fn.Body != nil {
				inspect(dir+"."+funcIdentity(fn), fn.Body)
			}
			continue
		}
		inspect(dir+".<package scope>", decl)
	}
	return found
}

// importedNames returns the names a file may refer to an import by. An explicit
// name is exact. Without one the package's own name is not known from the path,
// so every name the path plausibly declares is returned — the last element, the
// element before a major-version suffix, and each with its "go-" or "-go" affix
// removed — which can only keep a package-qualified call from being read as a
// method call, never hide a method call.
func importedNames(spec *ast.ImportSpec, importPath string) []string {
	if spec.Name != nil {
		if spec.Name.Name == "_" || spec.Name.Name == "." {
			return nil
		}
		return []string{spec.Name.Name}
	}
	elems := strings.Split(importPath, "/")
	base := path.Base(importPath)
	candidates := []string{base}
	if majorVersionElement.MatchString(base) && len(elems) > 1 {
		candidates = append(candidates, elems[len(elems)-2])
	}
	var names []string
	for _, c := range candidates {
		names = append(names, c,
			strings.TrimPrefix(c, "go-"),
			strings.TrimSuffix(c, "-go"),
			strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(c, "go-"), "-go"), "-", ""))
	}
	if importPath == orasRootImport {
		names = append(names, "oras")
	}
	return names
}
