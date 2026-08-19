// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// The module read: one parse of this module's own source, shared by every
// derivation that asks a question about the tree rather than about the registry.
//
// Two such derivations exist — which declared checks are invoked, and which
// surfaces reach which stages — and they must answer over the same tree. Parsing
// twice would let them observe two different trees and print figures that do not
// describe the page beside them, which is a correctness property before it is a
// saving. So the parse is here, once, and both reads project from it.
//
// It also holds the one judgement both reads need and neither should make twice:
// which packages are production. That is drawn from the import graph, which is
// why the read parses test files too — for their imports alone.

// sourceFile is one parsed production file.
type sourceFile struct {
	// Dir is the package directory holding it, relative to the module root.
	Dir string

	// Path is the file, relative to the module root.
	Path string

	// File is the parsed syntax.
	File *ast.File
}

// moduleSource is this module's production source, parsed once.
type moduleSource struct {
	// Root is the module root directory.
	Root string

	// Path is the module's own import path, read from its module file so that a
	// call's package qualifier can be resolved to a directory in this module.
	Path string

	// Fset positions every parsed file.
	Fset *token.FileSet

	// Files are the production files: no test suffix, and in a package something
	// outside a test imports.
	Files []sourceFile

	// PackageNameOfDir maps a package directory to the package name its files
	// declare, so an import with no alias resolves to the directory it names
	// rather than to that path's last segment, which the two need not share.
	PackageNameOfDir map[string]string

	// TestOnlyDirs are the package directories excluded as test support, sorted.
	// They are carried rather than dropped so the exclusion is reportable.
	TestOnlyDirs []string

	// FilesParsed is how many files the read parsed in total, test files
	// included. A search for something absent certifies nothing unless the
	// search reached a corpus.
	FilesParsed int
}

// readModuleSource parses the module's own Go source once per process.
//
// It refuses an empty read rather than reporting a clean one: every question
// asked of the result is a search for something that may be absent, so a read
// that found no file has measured the instrument rather than the tree.
func readModuleSource() (*moduleSource, error) {
	moduleOnce.Do(func() {
		moduleRead, moduleErr = walkModuleSource()
	})
	return moduleRead, moduleErr
}

var (
	moduleOnce sync.Once
	moduleRead *moduleSource
	moduleErr  error
)

func walkModuleSource() (*moduleSource, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	modulePath, err := modulePathOf(root)
	if err != nil {
		return nil, err
	}

	m := &moduleSource{
		Root:             root,
		Path:             modulePath,
		Fset:             token.NewFileSet(),
		PackageNameOfDir: map[string]string{},
	}

	// importedBy counts, per package directory in this module, how many files
	// import it and how many of those are test files. A package every importer
	// of which is a test file is test support, and the count is what says so.
	type importerCount struct{ production, test int }
	importedBy := map[string]*importerCount{}
	type candidate struct {
		dir  string
		path string
		file *ast.File
	}
	var candidates []candidate

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			if skipCensusDir(path, root, d.Name()) {
				return fs.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go"):
			return nil
		}
		isTest := strings.HasSuffix(path, "_test.go")

		// A test file is parsed for its imports alone: it contributes nothing to
		// either derivation, and its import list is what decides whether the
		// package it imports is production.
		mode := parser.Mode(0)
		if isTest {
			mode = parser.ImportsOnly
		}
		f, perr := parser.ParseFile(m.Fset, path, nil, mode)
		if perr != nil {
			// A file this read cannot parse is skipped rather than fatal: the read
			// is a census and not a compiler, and a tree mid-edit must not turn
			// generation red for a reason unrelated to the registry.
			return nil
		}
		m.FilesParsed++

		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		dir := filepath.ToSlash(filepath.Dir(rel))

		for _, im := range f.Imports {
			target, ok := m.dirOfImportPath(im.Path.Value)
			if !ok {
				continue
			}
			c := importedBy[target]
			if c == nil {
				c = &importerCount{}
				importedBy[target] = c
			}
			if isTest {
				c.test++
			} else {
				c.production++
			}
		}
		if isTest {
			return nil
		}
		if f.Name != nil {
			m.PackageNameOfDir[dir] = f.Name.Name
		}
		candidates = append(candidates, candidate{dir: dir, path: filepath.ToSlash(rel), file: f})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	if m.FilesParsed == 0 {
		return nil, fmt.Errorf(
			"the module read parsed no Go file under %s, so every derivation over it would describe "+
				"the instrument rather than the tree", root)
	}

	testOnly := map[string]bool{}
	for _, c := range candidates {
		if testOnly[c.dir] {
			continue
		}
		count := importedBy[c.dir]
		// A package nothing imports at all is NOT test support: a command's own
		// entry package is imported by nothing and is the surface a user reaches.
		if count != nil && count.production == 0 && count.test > 0 {
			testOnly[c.dir] = true
		}
	}
	m.TestOnlyDirs = sorted(keysOf(testOnly))

	for _, c := range candidates {
		if testOnly[c.dir] {
			continue
		}
		m.Files = append(m.Files, sourceFile{Dir: c.dir, Path: c.path, File: c.file})
	}
	if len(m.Files) == 0 {
		return nil, fmt.Errorf(
			"the module read kept no production file under %s after excluding test files and "+
				"test-support packages, so it has measured its own exclusions rather than the tree", root)
	}
	return m, nil
}

// dirOfImportPath reports the package directory a quoted import path names,
// where that path is inside this module.
func (m *moduleSource) dirOfImportPath(quoted string) (string, bool) {
	p, err := strconv.Unquote(quoted)
	if err != nil {
		return "", false
	}
	if p == m.Path {
		return ".", true
	}
	if !strings.HasPrefix(p, m.Path+"/") {
		return "", false
	}
	return strings.TrimPrefix(p, m.Path+"/"), true
}

// packageDirsInScope maps the local name each import of one file introduces to
// the package directory it names, so a package-qualified call resolves to a
// declaration rather than to a bare identifier.
//
// An import with no alias contributes the package name the imported files
// declare, which is read from the tree rather than assumed from the path's last
// segment: the two need not agree, and assuming they do resolves a call to a
// directory that declares no such package.
func (m *moduleSource) packageDirsInScope(f *ast.File) map[string]string {
	scope := map[string]string{}
	for _, im := range f.Imports {
		dir, ok := m.dirOfImportPath(im.Path.Value)
		if !ok {
			continue
		}
		switch {
		case im.Name != nil && im.Name.Name != "_" && im.Name.Name != ".":
			scope[im.Name.Name] = dir
		case im.Name != nil:
			// A blank or dot import introduces no qualifier to resolve.
		default:
			name := m.PackageNameOfDir[dir]
			if name == "" {
				name = filepath.Base(dir)
			}
			scope[name] = dir
		}
	}
	return scope
}

// modulePathRE matches the module declaration of a Go module file.
var modulePathRE = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// modulePathOf reads the module's own import path.
//
// It is read rather than spelled: the path is what turns a call's package
// qualifier into a directory in this module, and a spelled copy would keep
// resolving after the module was renamed.
func modulePathOf(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read the module file under %s: %w", root, err)
	}
	match := modulePathRE.FindSubmatch(b)
	if match == nil {
		return "", fmt.Errorf("the module file under %s declares no module path, so no call's "+
			"package qualifier can be resolved to a directory in it", root)
	}
	return string(match[1]), nil
}
