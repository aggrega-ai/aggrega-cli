// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
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
// the name itself, and the two codes and the message the condition carries.
//
// A re-export declares no new condition. It forwards to one, and the value it
// returns IS that condition's value, so it reports the same codes and the same
// message. All three are therefore resolved through the forward rather than left
// empty: a handle that reports a code at run time and shows none here would read
// as a condition without one. AliasOf is what distinguishes the two kinds — it
// names the handle forwarded to, and only a re-export carries it.
type Identity struct {
	Pkg        string // module-relative directory, e.g. "pkg/resolver"
	Name       string // exported identifier, e.g. "ErrParameterMissing"
	Code       string // registered descriptive code, resolved through a re-export
	ShortCode  string // registered short code, E and four digits, resolved through a re-export
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
// surface contains: version-control and dependency trees, and the deliberately
// malformed fixtures under testdata.
//
// The plugin tree is deliberately not among them. What shares no identity with
// the host binary is a guest's entry point, which is constrained to the
// WebAssembly guest target and compiled into nothing else; the translation core
// beside it carries no constraint at all and builds for the host and the guest
// alike, so the conditions it declares are the very ones a caller matches. That
// line runs between files rather than around a directory, so it is read per file
// by [guestEntryPoint] and not written down here as a path.
var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
}

// wasmGuestTags are the build tags only a WebAssembly guest target satisfies:
// the operating system a guest is built for, and the architecture it is built
// as.
//
// This set is the whole of what separates a guest's entry point from the core
// beside it, so it is declared once and consulted by both axes a build
// constraint can be written on — the //go:build expression and the file name.
var wasmGuestTags = map[string]bool{
	"wasip1": true,
	"wasm":   true,
}

// maxConstraintTags bounds the enumeration in [requiresGuestTag]. A build
// constraint names a handful of tags at most; the bound exists so that an
// expression naming an implausible number of them is refused by name, rather
// than silently taking a fallback in whichever direction happens to be
// convenient.
const maxConstraintTags = 16

// guestEntryPoint reports whether a file's build constraints can be satisfied
// only by a WebAssembly guest target.
//
// That is the property the matching surface turns on, and it is read from each
// file rather than from where the file sits. A guest's entry point is
// constrained to the guest target, so it enters no host binary and publishes
// nothing a caller can match; a shared core carries no constraint, builds for
// host and guest alike, and declares the conditions a caller reaches through an
// ordinary import. Deciding this by directory would restate the same split as a
// list of paths, and a guest added later would sit outside the list while
// looking exactly like one inside it.
//
// Both axes are read. The expression is decided by satisfiability with the guest
// tags held false; the file name is decided by its final underscore-separated
// segment, which is where a target suffix restricting the file to a guest
// appears.
func guestEntryPoint(path string, file *ast.File) (bool, error) {
	if wasmGuestTags[targetSuffix(path)] {
		return true, nil
	}
	expr, err := buildConstraint(file)
	if err != nil || expr == nil {
		return false, err
	}
	return requiresGuestTag(expr)
}

// targetSuffix returns the build-target suffix a file name carries, which Go
// reads from the final underscore-separated segment of the name.
//
// A name with no underscore carries no suffix at all: the constrained form is
// name_target.go, so a file called wasm.go is restricted by nothing and its
// declarations belong to every build.
func targetSuffix(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".go")
	i := strings.LastIndex(base, "_")
	if i < 0 {
		return ""
	}
	return base[i+1:]
}

// buildConstraint returns the //go:build expression the file carries, or nil
// when it carries none. Only a comment above the package clause is read, which
// is where the toolchain requires the constraint to sit.
func buildConstraint(file *ast.File) (constraint.Expr, error) {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			return constraint.Parse(c.Text)
		}
	}
	return nil, nil
}

// requiresGuestTag reports whether expr is unsatisfiable unless one of
// [wasmGuestTags] holds.
//
// It decides that by enumerating the assignments of the tags expr actually
// names, with the guest tags held false: when no such assignment satisfies expr,
// no build lacking a guest target can include the file. Enumerating is what
// makes the answer exact. A structural recursion over the expression would have
// to treat each operand as independent, and a tag appearing on both sides of an
// operator — unix && !unix — is then read as satisfiable when it is not.
//
// Holding the guest tags false rather than testing one reference target is what
// keeps the result a function of the tree alone. A file excluded from the target
// that happens to be running the scan is a different property, and reading that
// one would make the identity set differ between the machines it is generated
// on.
func requiresGuestTag(expr constraint.Expr) (bool, error) {
	var free []string
	seen := map[string]bool{}
	var collect func(constraint.Expr)
	collect = func(e constraint.Expr) {
		switch v := e.(type) {
		case *constraint.TagExpr:
			if wasmGuestTags[v.Tag] || seen[v.Tag] {
				return
			}
			seen[v.Tag] = true
			free = append(free, v.Tag)
		case *constraint.NotExpr:
			collect(v.X)
		case *constraint.AndExpr:
			collect(v.X)
			collect(v.Y)
		case *constraint.OrExpr:
			collect(v.X)
			collect(v.Y)
		}
	}
	collect(expr)
	if len(free) > maxConstraintTags {
		return false, fmt.Errorf("the build constraint %q names %d tags, more than the %d this scan enumerates",
			expr.String(), len(free), maxConstraintTags)
	}
	for mask := 0; mask < 1<<len(free); mask++ {
		on := make(map[string]bool, len(free))
		for i, tag := range free {
			if mask&(1<<i) != 0 {
				on[tag] = true
			}
		}
		if expr.Eval(func(tag string) bool { return on[tag] }) {
			return false, nil
		}
	}
	return true, nil
}

// registered is what one registration declares: the two codes the identity is
// registered under and the message it renders. It is keyed by the package-local
// name the registration is bound to, which is the name the accessor beside it
// returns and never a name a caller can reach.
type registered struct {
	code      string
	shortCode string
	message   string
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
		// Comments are retained because a build constraint is one, and the
		// constraint is what separates a guest's entry point from the core beside
		// it.
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		guest, gerr := guestEntryPoint(path, file)
		if gerr != nil {
			return fmt.Errorf("%s: %w", path, gerr)
		}
		if guest {
			return nil
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
				// Go compiles, but the scan reads files that exclude one another
				// — a unix file and its !unix counterpart — so both reach here
				// together.
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
	// code and the message registered against the identity it forwards to and
	// cannot be resolved until that identity has them. The re-exported name is the
	// one a caller is told to match, and its own declaration carries neither.
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
		if reg.shortCode == "" {
			// Reported rather than served with an empty column, for the reason
			// the missing-registration refusal above is reported: a condition
			// that shows no short code here reads as a condition that has none,
			// and the short code is the identifier a reader is given to quote.
			// A registration reaching here without one cannot run either — the
			// registration itself refuses it — so the tree is already broken and
			// this is the earlier, cheaper place to say so.
			return nil, fmt.Errorf("%s.%s registers no short code; "+
				"every condition declares one, and a listing that showed none for this handle "+
				"would read as a condition without one", acc.pkg, acc.name)
		}
		byHandle[acc.pkg+"."+acc.name] = &Identity{
			Pkg: acc.pkg, Name: acc.name, Code: reg.code, ShortCode: reg.shortCode, Message: reg.message,
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
			Pkg: acc.pkg, Name: acc.name, Code: origin.Code, ShortCode: origin.ShortCode,
			Message: origin.Message, AliasOf: acc.target.alias,
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

// registration reports the package-local name and the declared content of
// `var errX = Register("…", Meta{ShortCode: "…", Message: "…"})`.
//
// The descriptive code is read from the call's first argument rather than from a
// type ascribed to the declaration, because the code is what the call declares:
// a registration binds an identity that carries the code, and the declaration
// itself names no type at all. The short code is read from the metadata literal
// beside the message, which is where the condition declares it.
//
// A registered message is what makes this a registration at all — a Meta literal
// declaring no string message is a call this scan does not recognise. The short
// code is extracted but NOT part of that test, deliberately: treating a missing
// short code as "not a registration" would drop the whole identity out of the
// index silently, which is the one outcome worse than reporting it. [scan]
// refuses it by name instead, where the handle is known.
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
	out := registered{code: code}
	found := false
	for _, el := range meta.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if key.Name != "Message" && key.Name != "ShortCode" {
			continue
		}
		str, ok := kv.Value.(*ast.BasicLit)
		if !ok || str.Kind != token.STRING {
			continue
		}
		value, uerr := strconv.Unquote(str.Value)
		if uerr != nil {
			return "", none, false
		}
		if key.Name == "ShortCode" {
			out.shortCode = value
			continue
		}
		out.message = value
		found = true
	}
	if !found {
		return "", none, false
	}
	return vs.Names[0].Name, out, true
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
