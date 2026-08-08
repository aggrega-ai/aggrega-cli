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
// Meta and Register.
//
// A registration is recognised only when the file carrying it imports that
// package, or is that package. Matching a bare function name instead would let
// any unrelated function called Register declare an identity, and the resulting
// entry would look exactly like a real one.
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

// registered is what one registration declares: the code the identity is
// registered under and the message it renders. It is keyed by the package-local
// name the registration is bound to, which is the name the accessor beside it
// returns and never a name a caller can reach.
type registered struct {
	code    string
	message string
}

// accessorTarget is what an accessor's body yields. Exactly one field is set:
// the package-local name of the registration that declares the identity, or the
// qualified handle of an identity in another package that the accessor
// re-exports.
type accessorTarget struct {
	local string
	alias string
}

// scan reads every non-test Go file under root and returns the identities the
// module declares, ordered by package then name so the output is a function of
// the tree alone and not of the order the filesystem happened to yield.
//
// It reads declaration syntax rather than a compiled program, which is what lets
// it run without building the module. An identity is declared in two halves that
// sit beside each other: a registration bound to a package-local name, carrying
// the code and the message, and an exported accessor returning that name. Only
// the accessor's name is reachable by a caller, so it is the accessor that fixes
// the handle and the registration that supplies what the handle describes. The
// two halves are joined after the whole tree is read rather than as each file is
// parsed, because an accessor may be written either side of the registration it
// returns.
//
// The cost is stated rather than hidden: a handle produced by anything other
// than an accessor over a registration, or an accessor re-exporting one from
// another package, is invisible to it. Two such handles exist and neither is
// tabulated. One is a variable wrapping another identity so that a caller may
// match either, which registers no message for a table to carry; it is described
// in the page's curated prose instead. The other is an accessor forwarding to a
// second accessor in its own package, which names the same identity twice within
// one import — a reader who can reach either name can already reach the other,
// so the page loses no reachable match by carrying it once.
func scan(root, modulePath string) ([]Identity, error) {
	type accessorDecl struct {
		pkg    string
		name   string
		target accessorTarget
	}

	registrations := map[string]registered{} // pkgDir + "." + local name
	accessors := []accessorDecl{}            // in discovery order, sorted later
	seen := map[string]bool{}                // accessor handles already declared

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
			switch d := decl.(type) {
			case *ast.FuncDecl:
				name, target, ok := accessor(d, imports)
				if !ok {
					continue
				}
				h := pkgDir + "." + name
				// Two declarations of one handle are unreachable in a package
				// Go compiles, but the scan parses every file whatever its build
				// constraints, so two files excluded from each other by a build
				// tag reach here together.
				if seen[h] {
					return fmt.Errorf("%s: %s declared twice", path, h)
				}
				seen[h] = true
				accessors = append(accessors, accessorDecl{pkg: pkgDir, name: name, target: target})
			case *ast.GenDecl:
				if d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					name, reg, ok := registration(vs, errPkg, isErrorsPkg)
					if !ok {
						continue
					}
					key := pkgDir + "." + name
					if prev, dup := registrations[key]; dup && prev != reg {
						return fmt.Errorf("%s: %s is registered twice, with different metadata", path, key)
					}
					registrations[key] = reg
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The declaring accessors are resolved first, because a re-export takes the
	// message registered against the identity it forwards to and cannot be
	// resolved until that identity has one. The re-exported name is the one a
	// caller is told to match, and its own declaration carries no message at all.
	byHandle := map[string]*Identity{}
	out := make([]Identity, 0, len(accessors))
	for _, acc := range accessors {
		if acc.target.alias != "" {
			continue
		}
		reg, ok := registrations[acc.pkg+"."+acc.target.local]
		if !ok {
			// The accessor returns a package-local name that no registration
			// binds, so the handle names no identity: matching it would read
			// whatever that name happens to hold. It is reported rather than
			// skipped, because a skip removes the handle from the page while
			// leaving it exported and apparently matchable.
			return nil, fmt.Errorf("%s.%s returns %s, which no registration in that package binds; "+
				"a caller matching the handle would match whatever that name holds rather than a declared condition",
				acc.pkg, acc.name, acc.target.local)
		}
		byHandle[acc.pkg+"."+acc.name] = &Identity{
			Pkg: acc.pkg, Name: acc.name, Code: reg.code, Message: reg.message,
		}
	}
	// A re-export forwards to a DECLARING accessor and never to another
	// re-export. The restriction is checked rather than assumed: the loop above
	// resolved every declaring accessor, so a target absent from byHandle either
	// declares nothing or is itself a re-export, and the two are separated here
	// so a chain is refused by name instead of resolving or not according to the
	// order the two happened to be walked in.
	reExported := map[string]bool{}
	for _, acc := range accessors {
		if acc.target.alias != "" {
			reExported[acc.pkg+"."+acc.name] = true
		}
	}
	for _, acc := range accessors {
		if acc.target.alias == "" {
			continue
		}
		origin, ok := byHandle[acc.target.alias]
		if !ok {
			if reExported[acc.target.alias] {
				return nil, fmt.Errorf("%s.%s re-exports %s, which is itself a re-export; "+
					"a re-export names the identity it forwards to, so a chain would print a reader "+
					"a second name to follow rather than the one that declares the condition",
					acc.pkg, acc.name, acc.target.alias)
			}
			return nil, fmt.Errorf("%s.%s re-exports %s, which registers no message", acc.pkg, acc.name, acc.target.alias)
		}
		byHandle[acc.pkg+"."+acc.name] = &Identity{
			Pkg: acc.pkg, Name: acc.name, Message: origin.Message, AliasOf: acc.target.alias,
		}
	}
	for _, acc := range accessors {
		id := byHandle[acc.pkg+"."+acc.name]
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

// accessor reports the exported name an accessor declares and what its body
// yields, for the two shapes that publish an identity:
//
//	func ErrX() error { return errX }              // declares, over a registration
//	func ErrX() error { return otherpkg.ErrY() }   // re-exports another package's
//
// The signature is checked rather than the name alone. A name beginning "Err"
// is not on its own evidence of an accessor — a package-level formatting or
// predicate helper can share the prefix — and it is the empty parameter list
// with a single unnamed `error` result that tells them apart.
//
// A body forwarding to another accessor in the SAME package is deliberately not
// recognised. Such a handle names an identity that is already reachable under
// its own name from the same import, so admitting it would put one condition on
// the page twice, under a second name a reader gains nothing by matching. The
// omission is stated on [scan] as a cost rather than left to be inferred here.
func accessor(fd *ast.FuncDecl, imports map[string]string) (string, accessorTarget, bool) {
	none := accessorTarget{}
	if fd.Recv != nil || fd.Body == nil || fd.Name == nil {
		return "", none, false
	}
	name := fd.Name.Name
	if !strings.HasPrefix(name, "Err") || !yieldsErrorAlone(fd.Type) {
		return "", none, false
	}
	if len(fd.Body.List) != 1 {
		return "", none, false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", none, false
	}
	switch v := ret.Results[0].(type) {
	case *ast.Ident:
		return name, accessorTarget{local: v.Name}, true
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok || len(v.Args) != 0 || !strings.HasPrefix(sel.Sel.Name, "Err") {
			return "", none, false
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return "", none, false
		}
		dir, ok := imports[pkgIdent.Name]
		if !ok {
			return "", none, false
		}
		return name, accessorTarget{alias: dir + "." + sel.Sel.Name}, true
	}
	return "", none, false
}

// yieldsErrorAlone reports whether the signature takes nothing and returns a
// single unnamed error, which is the shape that distinguishes an accessor from
// any other function sharing the name prefix.
func yieldsErrorAlone(ft *ast.FuncType) bool {
	if ft.TypeParams != nil || (ft.Params != nil && len(ft.Params.List) != 0) {
		return false
	}
	if ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	result := ft.Results.List[0]
	if len(result.Names) != 0 {
		return false
	}
	id, ok := result.Type.(*ast.Ident)
	return ok && id.Name == "error"
}

// registration reports the package-local name, code and message of
// `var errX = Register("…", Meta{Message: "…"})`.
//
// The code is read from the call's first argument rather than from a type
// ascribed to the declaration, because the code is what the call declares: a
// registration binds an identity that carries the code, and the declaration
// itself names no type at all.
func registration(vs *ast.ValueSpec, errPkg string, isErrorsPkg bool) (string, registered, bool) {
	none := registered{}
	if len(vs.Names) != 1 || len(vs.Values) != 1 {
		return "", none, false
	}
	call, ok := vs.Values[0].(*ast.CallExpr)
	if !ok || !namesErrorsSymbol(call.Fun, "Register", errPkg, isErrorsPkg) || len(call.Args) != 2 {
		return "", none, false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", none, false
	}
	code, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", none, false
	}
	meta, ok := call.Args[1].(*ast.CompositeLit)
	if !ok {
		return "", none, false
	}
	for _, el := range meta.Elts {
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
		message, uerr := strconv.Unquote(str.Value)
		if uerr != nil {
			return "", none, false
		}
		return vs.Names[0].Name, registered{code: code, message: message}, true
	}
	return "", none, false
}

// namesErrorsSymbol reports whether expr names sym in the package that declares
// Register — `cerrors.Register` from outside it, a bare `Register` from within.
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
