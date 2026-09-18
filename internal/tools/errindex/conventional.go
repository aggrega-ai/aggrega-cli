// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"sort"
	"strings"
)

// conventionalName matches the declaration convention a reader takes for a
// halting condition's handle: the letter sequence Err followed by an upper-case
// letter, exported, at package level.
//
// A name whose fourth character is lower-case is a different word — Error,
// Errorf, Errors — and names a formatting or predicate helper rather than a
// condition, so the upper-case letter is what separates the convention from the
// prefix it shares.
//
// # A deliberate second expression of one convention, recorded as deliberate
//
// The same convention is expressed a second time, by the census that counts
// where each condition's rule is evaluated. Neither expression can import the
// other: this scan is a standalone command that reads syntax precisely so it can
// run without building the module, and the census is a test file, whose
// identifiers no package can reach. Publishing the pattern from the errors
// package would put a development-tooling concern on an exported surface that
// callers import for matching.
//
// What keeps the two from drifting silently is that they are read against the
// same tree by gates that run together: a convention one recognised and the
// other did not would show up as a condition present in one population and
// absent from the other, on the run that introduced it.
var conventionalName = regexp.MustCompile(`^Err[A-Z][A-Za-z0-9]*$`)

// conventionalDecl is one package-level declaration whose name matches the
// convention, recorded with enough of its declaration to say why it could not be
// placed.
//
// reaches carries the handles the declaration's own text names, resolved through
// the importing file's own import set. It is what admits a handle whose value IS
// a condition this scan already carries — a forward, or a wrap — without any
// name being written down: the admission is read from the declaration on every
// run, so it cannot outlive the condition it rests on.
type conventionalDecl struct {
	pkg     string
	name    string
	file    string
	form    string
	reaches []string
}

// handle is the declaration's key, in the same (package, name) form an identity
// carries, so the two are comparable without either side re-deriving the other's
// spelling.
func (d conventionalDecl) handle() string { return d.pkg + "." + d.name }

// conventionalDecls returns every package-level declaration in one file whose
// name matches the convention.
//
// It is read from the same files, and inside the same guard, as the declarations
// [scan] tabulates. A file only a WebAssembly guest target can build declares
// nothing a caller imports, so a condition-shaped name in one is outside this
// question exactly as it is outside the identity set.
//
// Every declaration kind that can bind a package-level name is read, because the
// blind spot this closes is precisely a name bound by a kind the identity walk
// does not recognise: the walk reads a function whose signature makes it an
// accessor and a variable whose value is a registration, and a name bound any
// other way reaches neither.
func conventionalDecls(file *ast.File, path, pkgDir string, imports map[string]string) []conventionalDecl {
	var out []conventionalDecl
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil || d.Name == nil || !conventionalName.MatchString(d.Name.Name) {
				continue
			}
			form := "a function declaration"
			if !yieldsErrorAlone(d.Type) {
				form = "a function declaration that does not take nothing and return one error"
			}
			var reaches []string
			if d.Body != nil {
				reaches = reachedHandles(d.Body, pkgDir, imports)
			}
			out = append(out, conventionalDecl{
				pkg: pkgDir, name: d.Name.Name, file: path, form: form, reaches: reaches,
			})
		case *ast.GenDecl:
			switch d.Tok {
			case token.VAR, token.CONST:
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, ident := range vs.Names {
						if !conventionalName.MatchString(ident.Name) {
							continue
						}
						form := "a variable declaration"
						if d.Tok == token.CONST {
							form = "a constant declaration"
						}
						var reaches []string
						// A one-to-one spec names the value belonging to this
						// name; any other shape distributes one right-hand side
						// across several names, so the whole spec is read.
						if len(vs.Values) == len(vs.Names) {
							reaches = reachedHandles(vs.Values[i], pkgDir, imports)
						} else {
							for _, v := range vs.Values {
								reaches = append(reaches, reachedHandles(v, pkgDir, imports)...)
							}
						}
						out = append(out, conventionalDecl{
							pkg: pkgDir, name: ident.Name, file: path, form: form, reaches: reaches,
						})
					}
				}
			case token.TYPE:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name == nil || !conventionalName.MatchString(ts.Name.Name) {
						continue
					}
					out = append(out, conventionalDecl{
						pkg: pkgDir, name: ts.Name.Name, file: path, form: "a type declaration",
					})
				}
			}
		}
	}
	return out
}

// reachedHandles returns the condition handles a declaration's own text calls,
// in the order they appear, resolved through the declaring file's import set.
//
// It reads calls and nothing else, because reaching a condition's value is what
// a call to its accessor does: an accessor yields the registered value, so a
// declaration that calls one has that condition's identity in it wherever the
// result travels. A bare mention that is not a call names the function rather
// than its value and reaches no condition.
//
// The walk is over the whole declaration rather than its outermost expression,
// because the call that reaches the condition sits inside whatever wraps it —
// under a formatting verb, inside a return, behind a composite literal — and a
// reader who wrote any of those has still bound this name to that condition.
func reachedHandles(n ast.Node, pkgDir string, imports map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		handle := ""
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if conventionalName.MatchString(fun.Name) {
				handle = pkgDir + "." + fun.Name
			}
		case *ast.SelectorExpr:
			ident, isIdent := fun.X.(*ast.Ident)
			if !isIdent || !conventionalName.MatchString(fun.Sel.Name) {
				return true
			}
			dir, inModule := imports[ident.Name]
			if !inModule {
				return true
			}
			handle = dir + "." + fun.Sel.Name
		}
		if handle != "" && !seen[handle] {
			seen[handle] = true
			out = append(out, handle)
		}
		return true
	})
	return out
}

// refuseUnplaceable reports every declaration that matches the convention and
// that this scan cannot place against a condition it carries.
//
// # Why it refuses rather than passing over
//
// A skipped declaration is indistinguishable from one that does not exist. The
// name stays exported, a reader still matches it, and the condition it appears
// to name has no code, no short code and no row on the published reference — so
// the reader matches something they can look up nowhere, and every gate reads
// clean because the scan never saw it. Tolerating the declaration certifies a
// handle nothing describes; refusing every declaration this scan does not
// already tabulate would refuse a forward and a wrap, which name conditions that
// are described. Both answers are wrong, so what decides is whether the
// declaration resolves, and a declaration that resolves to nothing is named.
//
// # The two admissions, and why neither is a list of names
//
// A declaration is placed when the scan already carries it — an accessor over a
// registration, or a re-export of one — or when the declaration's own text
// reaches a handle the scan carries. The second covers the two shapes a handle
// can take without being an accessor: a forward to a condition declared beside
// it, and a variable wrapping one so that a caller may match either name.
//
// Both are DERIVED from the declaration on every run, which is what keeps them
// from being an exception list. A recorded name cannot fail when its subject
// disappears: the name stays permitted, the declaration it excused is gone, and
// nothing reports the difference. These resolve instead. Rewrite a wrap into a
// plain error value and it reaches no handle, so it is named here on the next
// run; delete or rename the condition underneath it and the same thing happens,
// at the declaration that depended on it rather than somewhere a reader has to
// go looking.
//
// # What it deliberately does not judge
//
// Whether the condition a placed declaration resolves to is the RIGHT one. That
// is a question about meaning, and this scan reads syntax; what it establishes
// is that the handle leads somewhere a reader can follow, which is the property
// its silence would otherwise assert without checking.
func refuseUnplaceable(decls []conventionalDecl, byHandle map[string]*Identity) error {
	var unplaceable []string
	for _, d := range decls {
		if byHandle[d.handle()] != nil {
			continue
		}
		placed := false
		for _, h := range d.reaches {
			if byHandle[h] != nil {
				placed = true
				break
			}
		}
		if placed {
			continue
		}
		unplaceable = append(unplaceable, fmt.Sprintf("%s in %s, %s", d.handle(), d.file, d.form))
	}
	if len(unplaceable) == 0 {
		return nil
	}
	// Sorted, because the walk's order is the filesystem's and a refusal that
	// lists its members in a different order between runs cannot be compared
	// with the previous one.
	sort.Strings(unplaceable)
	return fmt.Errorf("%d declaration(s) match the condition naming convention and name no condition this scan "+
		"carries:\n  %s\n"+
		"A declaration named this way is read as a halting condition's handle. One this scan cannot place has "+
		"no code, no short code and no row on the published reference, so a caller can match it from Go and "+
		"look it up nowhere — and passing over it would leave that handle exported and apparently matchable "+
		"while every count here read as complete.\n"+
		"Each is one of two things and the declaration says which. A halting condition declares its identity "+
		"through a registration and publishes it through an accessor beside it, which is what gives it a code "+
		"and a row. An ordinary error that is not a condition stops carrying a condition's name, so nothing "+
		"reads it as one. A handle whose value IS a condition declared elsewhere — a forward to it, or a wrap "+
		"around it — is placed already and is not listed here",
		len(unplaceable), strings.Join(unplaceable, "\n  "))
}
