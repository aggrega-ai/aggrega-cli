// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// pathscopeImportPath and windowshostImportPath are the two packages this guard
// reads calls of, by import path, so a file importing either under another name is
// still read.
const (
	pathscopeImportPath   = "github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	windowshostImportPath = "github.com/aggrega-ai/aggrega-cli/internal/windowshost"
)

// nameJudgements are the entry points of this package that judge a name a writer
// did not choose without placing it: IsLocalSlash, and NamesFileBeneath, which
// asks IsLocalSlash's question and one further. A site judging a name with either
// and joining it onto a root itself skips what PlaceBeneath adds, so both are
// read here and neither is a way around the guard.
var nameJudgements = map[string]bool{"IsLocalSlash": true, "NamesFileBeneath": true}

// judgementsThatWriteNothing are the places outside this package that judge a
// name with IsLocalSlash or NamesFileBeneath and write nothing beneath a root
// from it, keyed by the package directory and the top-level declaration the
// judgement sits in — a function, a method with its receiver type first, or a
// package-level variable whose value holds it — each valued by why no name
// reaches disk from it.
//
// 🔴 Hand-written because it IS the decision rather than a fact about the source:
// an entry states that a site judges a name and places nothing on disk with it.
// Every other site judging a name with IsLocalSlash is a writer, and a writer asks
// PlaceBeneath instead, which judges the climb by IsLocalSlash and adds what a
// write owes beyond it. Deriving this set from the calls would make the guard
// agree with whatever the code does.
var judgementsThatWriteNothing = map[string]string{
	"internal/compiler/renderer:ArtifactsEntryBundleFilesStayUnderTheirKey": "judges an ArtifactLibrary bundle's " +
		"file name at compile time; the name it composes reaches disk only through the rendered-artifact write, " +
		"which places it with PlaceBeneath",
	"internal/compiler/renderer:walkArtifactBlock": "judges an ArtifactLibrary bundle's file name before " +
		"composing it under its key, for a Recipe's own artifacts and its boundary block alike; the walk hands " +
		"the composed name to its caller and writes nothing, and the name reaches disk only as a path of the " +
		"render's file set, which the rendered-artifact write places with PlaceBeneath",
	"internal/compiler/renderer:AssemblyPathNamesADirectoryBeneathTheOutputRoot": "judges the directory a " +
		"Release boundary's output is written under, the dispatch path its evaluated directory and sub-directory " +
		"compose to; it raises a finding and writes nothing, and the path reaches disk only as the prefix of the " +
		"render's file names, which the rendered-artifact write places with PlaceBeneath",
	"internal/compiler/renderer:boundaryPathFieldAtFault": "judges which of a Release boundary's two path fields " +
		"a refused dispatch path came from, to name it in a finding; it returns the field and writes nothing",
	"pkg/docscache:readTarEntry": "looks a page up inside a documentation bundle held in memory; the cache " +
		"writes the bundle whole and lays no entry out",
	"internal/mcp:bundlePageForURI": "looks a page up inside a documentation bundle held in memory",
	"pkg/oci:staysInItsPlace": "judges a value a cache location is built from; every directory name of the " +
		"location is spelled by pathsegment.Encode, which writes no name Windows cannot hold",
}

// TestEveryWriterOfAJudgedNamePlacesItThroughPlaceBeneath keeps PlaceBeneath the
// one way a name somebody else chose reaches disk.
//
// A writer that judges a name with IsLocalSlash and joins it onto a root itself
// gets the climb right and skips everything else a write owes: the root made a
// directory path, so a bare Windows drive does not put the file at the top of the
// drive, and the refusal of a name Windows cannot hold where the process runs on
// Windows. Both of those live in PlaceBeneath, so a new site that writes a name it
// did not choose gets them by calling the one function there is — and a site that
// calls IsLocalSlash instead is caught here, before it ships.
//
// So every call of IsLocalSlash or NamesFileBeneath in production source outside
// this package must sit in a judgement judgementsThatWriteNothing names, with the
// reason it writes nothing; a site this file does not name fails, naming where it
// is and what to call instead. An entry the walk no longer finds fails too, because a permission
// nothing exercises reads as one something does — and the entries found are the
// evidence the walk reached the source at all, since a walk that reads nothing
// leaves every entry unfound.
//
// It holds the platform seam as well: windowshost.SetForTest moves the answer every
// writer reads, so a call of it from production source outside that package would
// make a Linux machine refuse what Windows refuses, and fails here.
//
// It FAILS rather than reports, because a Go package that passes prints nothing,
// so a guard that only reported would be inert in every routine run.
//
// 🔴 What it cannot see, because a guard's reach is part of its contract: a writer
// that judges a name by no predicate at all, or by one of its own, and joins it
// onto a root. Those are a climb nobody judged, which the writer's own tests have
// to catch, and they never reach IsLocalSlash for this guard to read.
//
// 🔴 PLANTED DEFECT: add, as a new file of any production package — say
// internal/importops/planted_writer.go — a function that judges a name with
// pathscope.IsLocalSlash and writes beneath a root with it:
//
//	func plantedWriter(root, name string) error {
//		if !pathscope.IsLocalSlash(name) {
//			return nil
//		}
//		return os.WriteFile(filepath.Join(root, name), nil, 0o644)
//	}
//
// and this test reddens, naming internal/importops:plantedWriter.
func TestEveryWriterOfAJudgedNamePlacesItThroughPlaceBeneath(t *testing.T) {
	fset := token.NewFileSet()
	found := map[string]bool{}
	err := filepath.WalkDir(moduleRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, moduleRoot+string(filepath.Separator)))
		if d.IsDir() {
			if d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") && p != moduleRoot {
				return filepath.SkipDir
			}
			for _, dir := range devToolingDirs {
				if rel == dir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		// Only a file spelling one of the names can call one, so the rest are not
		// parsed; the file is still read, so a call added to it is seen.
		if !bytes.Contains(src, []byte("IsLocalSlash")) && !bytes.Contains(src, []byte("NamesFileBeneath")) &&
			!bytes.Contains(src, []byte("SetForTest")) {
			return nil
		}
		node, perr := parser.ParseFile(fset, p, src, 0)
		if perr != nil {
			return perr
		}
		pathscopeName := importedAs(node, pathscopeImportPath)
		windowshostName := importedAs(node, windowshostImportPath)
		for _, decl := range node.Decls {
			for _, site := range declarationSites(decl) {
				key := pkgDir + ":" + site.name
				ast.Inspect(site.node, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					switch {
					case pkgDir != "pkg/pathscope" && pathscopeName != "" && x.Name == pathscopeName && nameJudgements[sel.Sel.Name]:
						found[key] = true
						if _, named := judgementsThatWriteNothing[key]; !named {
							t.Errorf("%s: pathscope.%s judges a name in %s, which this file does not name as "+
								"a judgement that writes nothing — a writer placing a name it did not choose asks "+
								"pathscope.PlaceBeneath, which judges the climb by IsLocalSlash and also makes the root a "+
								"directory path and refuses, on Windows, a name Windows cannot hold. If this site writes "+
								"nothing beneath a root from the name, add it to judgementsThatWriteNothing with the "+
								"reason, in the same change.",
								fset.Position(sel.Pos()), sel.Sel.Name, key)
						}
					case pkgDir != "internal/windowshost" && windowshostName != "" && x.Name == windowshostName && sel.Sel.Name == "SetForTest":
						t.Errorf("%s: windowshost.SetForTest called from production source in %s — it moves the "+
							"answer every writer reads about whether the process runs on Windows, and only a test may "+
							"move it", fset.Position(sel.Pos()), key)
					}
					return true
				})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module's production source: %v", err)
	}
	var missing []string
	for key := range judgementsThatWriteNothing {
		if !found[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("judgements named as writing nothing that the walk did not find asking pathscope.IsLocalSlash or "+
			"pathscope.NamesFileBeneath: %v — "+
			"a site renamed, or one that stopped asking; rename or drop its entry in the same change, because a "+
			"permission nothing exercises reads as one something does, and a walk that found none of them read "+
			"nothing at all", missing)
	}
}

// importedAs returns the name file refers to the package at importPath by, or ""
// where it does not import it. A dot import is reported as "", since its calls name
// no package to read.
func importedAs(file *ast.File, importPath string) string {
	for _, spec := range file.Imports {
		got, err := strconv.Unquote(spec.Path.Value)
		if err != nil || got != importPath {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name == "." || spec.Name.Name == "_" {
				return ""
			}
			return spec.Name.Name
		}
		return importPath[strings.LastIndex(importPath, "/")+1:]
	}
	return ""
}

// declarationSite is a top-level declaration's code, keyed by the name a site in it
// is reported under.
type declarationSite struct {
	name string
	node ast.Node
}

// declarationSites returns the code a top-level declaration holds, each piece
// keyed by its name: a function by its name, a method by its receiver type and
// name, and each spec of a variable or constant declaration by the names it binds
// — a package-level variable whose value is a function literal holds code as a
// function does. A type or an import declaration holds no call and yields nothing.
func declarationSites(decl ast.Decl) []declarationSite {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []declarationSite{{name: funcName(d), node: d}}
	case *ast.GenDecl:
		if d.Tok != token.VAR && d.Tok != token.CONST {
			return nil
		}
		var out []declarationSite
		for _, spec := range d.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			names := make([]string, 0, len(vs.Names))
			for _, n := range vs.Names {
				names = append(names, n.Name)
			}
			out = append(out, declarationSite{name: strings.Join(names, ","), node: vs})
		}
		return out
	}
	return nil
}
