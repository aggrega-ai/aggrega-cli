// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// errorsPackageDir is the module-relative directory of the package that declares
// Code, Meta and Register.
//
// A declaration is recognised only when the file carrying it imports that
// package, or is that package. Matching a bare type name instead would let any
// unrelated type called Code register an identity, and the resulting entry would
// look exactly like a real one.
const errorsPackageDir = "pkg/errors"

// Identity is one handle a caller can match: the package that declares the name,
// the name itself, and the message the condition renders.
//
// Code and AliasOf are mutually exclusive. A declared identity carries the code
// value it was declared with; a re-export carries the qualified handle it
// forwards to and takes that handle's message, because a re-export declares no
// new identity and registers no message of its own.
type Identity struct {
	Pkg        string // module-relative directory, e.g. "pkg/resolver"
	Name       string // exported identifier, e.g. "ErrParameterMissing"
	Code       string // declared code value; empty for a re-export
	Message    string // registered message, resolved through a re-export
	AliasOf    string // "pkg/oci.ErrFetchNotFound" for a re-export, else empty
	Importable bool   // reachable by `import` from outside this module
}

// Handle is the identity's key. It is (package, name) and never the bare name:
// a name-keyed map merges two same-named declarations in different packages into
// one entry, and it collapses a re-export onto the origin it forwards to, which
// loses the package a caller was told to import.
func (id Identity) Handle() string { return id.Pkg + "." + id.Name }

// skipDirs are directory names whose contents declare nothing the matching
// surface contains: version-control and dependency trees, the deliberately
// malformed fixtures under testdata, and the plugin tree, which compiles to
// WebAssembly guests that share no identity with the host binary.
var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
	"plugins":      true,
}

// scan reads every non-test Go file under root and returns the identities the
// module declares, ordered by package then name so the output is a function of
// the tree alone and not of the order the filesystem happened to yield.
//
// It reads declaration syntax rather than a compiled program, which is what lets
// it run without building the module. The cost is stated rather than hidden: a
// handle produced by anything other than a `Code`-typed constant or a re-export
// of one is invisible to it. One such handle exists — a variable wrapping another
// identity so that a caller may match either — and it is described in the page's
// curated prose rather than tabulated, because it registers no message for a
// table to carry.
func scan(root, modulePath string) ([]Identity, error) {
	origins := map[string]*Identity{}  // handle -> identity
	aliasTarget := map[string]string{} // handle -> target handle
	messages := map[string]string{}    // handle -> registered message
	order := []string{}                // handles in discovery order, sorted later

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, filepath.Dir(path))
		if rerr != nil {
			return rerr
		}
		pkgDir := filepath.ToSlash(rel)
		if pkgDir == "." {
			pkgDir = ""
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}

		imports := importMap(file, modulePath)
		errPkg, isErrorsPkg := errorsPackageAlias(imports, pkgDir)

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				// A registration stands as a discarded declaration beside the
				// identity it describes, so it is read from the same specs.
				if name, msg, ok := registration(vs, errPkg, isErrorsPkg); ok {
					h := pkgDir + "." + name
					if prev, dup := messages[h]; dup && prev != msg {
						return fmt.Errorf("%s: %s has two registered messages", path, h)
					}
					messages[h] = msg
					continue
				}
				if len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				name := vs.Names[0].Name
				if !strings.HasPrefix(name, "Err") {
					continue
				}
				h := pkgDir + "." + name

				if code, ok := codeDeclaration(vs, errPkg, isErrorsPkg); ok {
					if _, dup := origins[h]; dup {
						return fmt.Errorf("%s: %s declared twice", path, h)
					}
					origins[h] = &Identity{Pkg: pkgDir, Name: name, Code: code}
					order = append(order, h)
					continue
				}
				if target, ok := reExport(vs, imports); ok {
					if _, dup := aliasTarget[h]; dup {
						return fmt.Errorf("%s: %s re-exported twice", path, h)
					}
					aliasTarget[h] = target
					origins[h] = &Identity{Pkg: pkgDir, Name: name, AliasOf: target}
					order = append(order, h)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// A re-export takes the message registered against the identity it forwards
	// to. Resolving it is not a convenience: the re-exported name is the one a
	// caller is told to match, and its own declaration carries no message at all.
	for h, target := range aliasTarget {
		msg, ok := messages[target]
		if !ok {
			return nil, fmt.Errorf("%s re-exports %s, which registers no message", h, target)
		}
		messages[h] = msg
	}

	out := make([]Identity, 0, len(order))
	for _, h := range order {
		id := origins[h]
		msg, ok := messages[h]
		if !ok {
			return nil, fmt.Errorf("%s declares a code with no registered message; "+
				"a caller matching it would read text naming the unregistered code instead of a description", h)
		}
		id.Message = msg
		id.Importable = importable(id.Pkg)
		out = append(out, *id)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pkg != out[j].Pkg {
			return out[i].Pkg < out[j].Pkg
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// importMap maps each in-module import's local name to its module-relative
// directory. Out-of-module imports are dropped: a re-export can only forward to
// an identity this scan can resolve.
func importMap(file *ast.File, modulePath string) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if p != modulePath && !strings.HasPrefix(p, modulePath+"/") {
			continue
		}
		dir := strings.TrimPrefix(strings.TrimPrefix(p, modulePath), "/")
		local := lastSegment(dir)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		out[local] = dir
	}
	return out
}

// lastSegment returns the default local name of an import: its final path
// segment, which an explicit alias in the import spec overrides.
func lastSegment(dir string) string {
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		return dir[i+1:]
	}
	return dir
}

// errorsPackageAlias reports the local name the file uses for the package that
// declares Code, and whether the file IS that package — in which case the
// declarations are unqualified.
func errorsPackageAlias(imports map[string]string, pkgDir string) (string, bool) {
	if pkgDir == errorsPackageDir {
		return "", true
	}
	for local, dir := range imports {
		if dir == errorsPackageDir {
			return local, false
		}
	}
	return "", false
}

// codeDeclaration reports the value of `Err… Code = "…"`, qualified by the
// errors package unless the file is that package.
func codeDeclaration(vs *ast.ValueSpec, errPkg string, isErrorsPkg bool) (string, bool) {
	if vs.Type == nil || !namesErrorsSymbol(vs.Type, "Code", errPkg, isErrorsPkg) {
		return "", false
	}
	lit, ok := vs.Values[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// reExport reports the qualified handle of `ErrX = otherpkg.ErrY`.
func reExport(vs *ast.ValueSpec, imports map[string]string) (string, bool) {
	if vs.Type != nil {
		return "", false
	}
	sel, ok := vs.Values[0].(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "Err") {
		return "", false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	dir, ok := imports[pkgIdent.Name]
	if !ok {
		return "", false
	}
	return dir + "." + sel.Sel.Name, true
}

// registration reports the identity name and message of
// `_ = Register(ErrX, Meta{Message: "…"})`.
func registration(vs *ast.ValueSpec, errPkg string, isErrorsPkg bool) (string, string, bool) {
	if len(vs.Values) != 1 {
		return "", "", false
	}
	call, ok := vs.Values[0].(*ast.CallExpr)
	if !ok || !namesErrorsSymbol(call.Fun, "Register", errPkg, isErrorsPkg) || len(call.Args) != 2 {
		return "", "", false
	}
	ident, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return "", "", false
	}
	lit, ok := call.Args[1].(*ast.CompositeLit)
	if !ok {
		return "", "", false
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Message" {
			continue
		}
		str, ok := kv.Value.(*ast.BasicLit)
		if !ok || str.Kind != token.STRING {
			continue
		}
		v, err := strconv.Unquote(str.Value)
		if err != nil {
			return "", "", false
		}
		return ident.Name, v, true
	}
	return "", "", false
}

// namesErrorsSymbol reports whether expr names sym in the package that declares
// Code — `cerrors.Code` from outside it, a bare `Code` from within.
func namesErrorsSymbol(expr ast.Expr, sym, errPkg string, isErrorsPkg bool) bool {
	if isErrorsPkg {
		ident, ok := expr.(*ast.Ident)
		return ok && ident.Name == sym
	}
	if errPkg == "" {
		return false
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != sym {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == errPkg
}

// importable reports whether a reader outside this module can import the
// package. Go makes an internal package structurally unreachable from outside
// the subtree that contains it, so an entry naming one advertises a match no
// reader can perform.
func importable(pkgDir string) bool {
	if pkgDir == "" {
		return false
	}
	for _, seg := range strings.Split(pkgDir, "/") {
		if seg == "internal" {
			return false
		}
	}
	return strings.Split(pkgDir, "/")[0] != "cmd"
}

// modulePathOf reads the module path from go.mod. It is read rather than
// written down here so the import prefix the page teaches has one home.
func modulePathOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("no module directive in %s/go.mod", root)
}
