// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package projectionparity

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// modulePathRE matches the module declaration of a Go module file.
var modulePathRE = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// ModuleRoot walks up from start until it finds the directory holding a Go
// module file, and returns that directory together with the module's own import
// path.
//
// The import path is read rather than spelled, because it is what turns a call's
// package qualifier into a directory in this module: a spelled copy would keep
// resolving after the module was renamed, and would then resolve to nothing while
// reporting a clean read.
func ModuleRoot(start string) (root, modulePath string, err error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		body, readErr := os.ReadFile(candidate)
		if readErr == nil {
			match := modulePathRE.FindSubmatch(body)
			if match == nil {
				return "", "", fmt.Errorf("the module file at %s declares no module path, so no call's "+
					"package qualifier can be resolved to a directory in it", candidate)
			}
			return dir, string(match[1]), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("no Go module file at or above %s", start)
		}
		dir = parent
	}
}

// ConstantValues reads the string values of every constant of one named type
// declared in dir, in declaration order.
//
// It exists so a driver can derive the set it must account for from the
// declaration itself rather than from a list beside it. A driver holding a
// mapping keyed on such a constant can then be held from both ends: a constant
// nothing maps is a surface nobody declared a corpus for, and a mapping key that
// is not a declared constant is a stale row.
func ConstantValues(root, dir, typeName string) ([]string, error) {
	s := newSource(root, "")
	parsed, err := s.read(dir)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, file := range parsed.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || vs.Type == nil || bareTypeName(vs.Type) != typeName {
					continue
				}
				for _, value := range vs.Values {
					lit, ok := value.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					text, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						continue
					}
					out = append(out, text)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s declares no string constant of type %s, so a driver deriving its corpus from them would derive nothing",
			dir, typeName)
	}
	return out, nil
}

// bareTypeName is typeName restricted to a bare identifier, so a constant typed by
// another package's identically-named type is not read as this one's.
func bareTypeName(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// parsedDir is one package directory's non-test source, parsed with its comments.
type parsedDir struct {
	// Dir is the directory, relative to the module root and slash-separated.
	Dir string

	// Files are the parsed files, ordered by path so nothing derived from them
	// depends on a map's iteration order.
	Files []*ast.File

	// Paths are the file paths, relative to the module root, in Files order.
	Paths []string
}

// source is one parse of every directory this read needs, keyed by directory.
//
// The parse is held rather than repeated because two derivations run over it —
// which producers the surfaces call, and what those producers return — and they
// must answer over the same tree. Parsing twice would let them observe two
// different trees and report a divergence that describes neither.
type source struct {
	root       string
	modulePath string
	fset       *token.FileSet
	dirs       map[string]*parsedDir

	// packageNameOfDir maps a directory to the package name its files declare,
	// so an unaliased import resolves to the directory it names rather than to
	// that path's last segment, which the two need not share.
	packageNameOfDir map[string]string

	// filesByPath maps the file name a position resolves to back to the parsed
	// file, so the import scope a call is read under can be the scope of the file
	// the call actually sits in.
	filesByPath map[string]*ast.File
}

func newSource(root, modulePath string) *source {
	return &source{
		root:             root,
		modulePath:       modulePath,
		fset:             token.NewFileSet(),
		dirs:             map[string]*parsedDir{},
		packageNameOfDir: map[string]string{},
		filesByPath:      map[string]*ast.File{},
	}
}

// read parses dir's non-test Go sources, once per directory.
//
// Comments are kept because an exemption is stated in one, and a reading that
// dropped them would report every stated exemption as an absent one.
//
// A directory holding no non-test Go source is an error rather than an empty
// result: every question asked of a parse is a search for something that may be
// absent, so a parse that reached no file has measured the instrument.
func (s *source) read(dir string) (*parsedDir, error) {
	if held, ok := s.dirs[dir]; ok {
		return held, nil
	}
	absolute := filepath.Join(s.root, filepath.FromSlash(dir))
	pkgs, err := parser.ParseDir(s.fset, absolute, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dir, err)
	}

	byPath := map[string]*ast.File{}
	var paths []string
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			paths = append(paths, path)
			byPath[path] = file
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s holds no non-test Go source, so a clean read over it certifies nothing", dir)
	}
	slices.Sort(paths)

	held := &parsedDir{Dir: dir}
	for _, path := range paths {
		file := byPath[path]
		held.Files = append(held.Files, file)
		s.filesByPath[path] = file
		rel, rerr := filepath.Rel(s.root, path)
		if rerr != nil {
			rel = path
		}
		held.Paths = append(held.Paths, filepath.ToSlash(rel))
		if file.Name != nil {
			s.packageNameOfDir[dir] = file.Name.Name
		}
	}
	s.dirs[dir] = held
	return held, nil
}

// dirOfImportPath reports the package directory a quoted import path names,
// where that path is inside this module.
func (s *source) dirOfImportPath(quoted string) (string, bool) {
	p, err := strconv.Unquote(quoted)
	if err != nil {
		return "", false
	}
	if p == s.modulePath {
		return ".", true
	}
	if !strings.HasPrefix(p, s.modulePath+"/") {
		return "", false
	}
	return strings.TrimPrefix(p, s.modulePath+"/"), true
}

// packageDirsInScope maps the local name each of one file's imports introduces
// to the module-internal directory it names.
//
// An unaliased import contributes the package name the imported files declare,
// read from the tree rather than assumed from the path's last segment. Where the
// imported directory has not been parsed yet the last segment is the only answer
// available, and it is used — a wrong guess resolves a call to a directory that
// declares no such function, which this read reports rather than clears.
func (s *source) packageDirsInScope(f *ast.File) map[string]string {
	scope := map[string]string{}
	for _, im := range f.Imports {
		dir, ok := s.dirOfImportPath(im.Path.Value)
		if !ok {
			continue
		}
		switch {
		case im.Name != nil && im.Name.Name != "_" && im.Name.Name != ".":
			scope[im.Name.Name] = dir
		case im.Name != nil:
			// A blank or dot import introduces no qualifier to resolve.
		default:
			name := s.packageNameOfDir[dir]
			if name == "" {
				name = path(dir)
			}
			scope[name] = dir
		}
	}
	return scope
}

// scopeAt is the import scope of the file a position sits in, mapping each local
// name an import introduces to the module-internal directory it names.
//
// It is taken per position rather than per directory because an import alias is a
// property of the file that writes it: two files of one package may bind the same
// local name to different packages, and a scope merged across the directory would
// resolve a call against an import its own file never made. An empty scope is the
// answer for a position in a file this read has not parsed, which resolves no
// qualifier and so follows nothing.
func (s *source) scopeAt(pos token.Pos) map[string]string {
	file, parsed := s.filesByPath[s.fset.Position(pos).Filename]
	if !parsed {
		return nil
	}
	return s.packageDirsInScope(file)
}

// path is a slash-separated directory's last segment.
func path(dir string) string {
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		return dir[i+1:]
	}
	return dir
}

// position is a node's file (relative to the module root) and line.
func (s *source) position(pos token.Pos) (string, int) {
	p := s.fset.Position(pos)
	rel, err := filepath.Rel(s.root, p.Filename)
	if err != nil {
		return filepath.ToSlash(p.Filename), p.Line
	}
	return filepath.ToSlash(rel), p.Line
}

// typeName is the bare name of a type expression, ignoring any package
// qualifier, pointer, slice or type arguments, so that a result named plainly
// and the same result named through its package qualifier read alike.
func typeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.StarExpr:
		return typeName(e.X)
	case *ast.ParenExpr:
		return typeName(e.X)
	case *ast.IndexExpr:
		return typeName(e.X)
	case *ast.IndexListExpr:
		return typeName(e.X)
	}
	return ""
}

// typeQualifier is the package qualifier a type expression names, empty for a
// type named without one.
func typeQualifier(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if ident, ok := e.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.StarExpr:
		return typeQualifier(e.X)
	case *ast.ParenExpr:
		return typeQualifier(e.X)
	case *ast.IndexExpr:
		return typeQualifier(e.X)
	case *ast.IndexListExpr:
		return typeQualifier(e.X)
	}
	return ""
}
