// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package projectionparity

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

// Surface is one caller-facing surface: the name a finding calls it by, and the
// package directories its projections live in, relative to the module root.
//
// The directories are supplied rather than derived because nothing in the tree
// links a surface to the packages that project its results. The command line
// declares its availability in one package and renders its results in another,
// and no declaration connects the two — so the mapping is the caller's to state
// and, being stated, the caller's to guard.
type Surface struct {
	Name string
	Dirs []string
}

// resultType is one struct a shared computation hands its callers.
type resultType struct {
	// coreDir is the package directory declaring it.
	coreDir string

	// name is the struct's own name.
	name string

	// fields are its exported field names, promoted fields included, sorted.
	fields []string

	// unreadable is non-empty where the struct could not be fully read, in
	// which case no parity verdict is drawn over it: a field set that may be
	// short cannot support a claim that a surface dropped something.
	unreadable string
}

// producer is one exported function a shared computation is reached through,
// together with which of its results are structs it declares itself.
type producer struct {
	coreDir  string
	funcName string

	// structResults maps a result position to the struct type at it. A position
	// carrying anything else — an error, a scalar, a type from elsewhere — is
	// absent, because there is no field set to compare across surfaces.
	structResults map[int]string

	// skip records that the name hands back nothing this read can compare: a
	// type conversion, which is syntactically a call and semantically the
	// caller's own value; a function returning nothing at all; or one returning
	// no struct that its own package declares.
	skip bool

	// unreadable is non-empty where the declaration could not be found or read.
	unreadable string
}

// callSite is one call to a shared computation from one surface.
type callSite struct {
	surface  string
	dir      string
	file     *ast.File
	fn       *ast.FuncDecl
	call     *ast.CallExpr
	coreDir  string
	funcName string
}

// collectCallSites reads every call from a surface to an exported function of a
// package under coreScope that is not itself part of any surface.
//
// A call inside a surface to another surface's package is excluded: the surfaces
// call each other — a command line hosts the other two — and treating one
// surface's own exports as a shared computation would compare a surface against
// itself.
//
// coreScope bounds which packages count as shared computation cores. Empty means
// every package in the module. The reason a caller narrows it is that a package
// outside the module's unimportable tree is published vocabulary, whose types are
// shared with consumers this read knows nothing about: two surfaces reading
// different parts of a loaded document are reading one published structure for
// different purposes, which is not the same thing as one surface dropping an
// answer another was handed.
func collectCallSites(s *source, surfaces []Surface, surfaceDirs map[string]bool, coreScope string) ([]callSite, error) {
	var sites []callSite
	for _, surface := range surfaces {
		for _, dir := range surface.Dirs {
			parsed, err := s.read(dir)
			if err != nil {
				return nil, err
			}
			for _, file := range parsed.Files {
				scope := s.packageDirsInScope(file)
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					ast.Inspect(fn.Body, func(node ast.Node) bool {
						call, ok := node.(*ast.CallExpr)
						if !ok {
							return true
						}
						selector, ok := call.Fun.(*ast.SelectorExpr)
						if !ok {
							return true
						}
						qualifier, ok := selector.X.(*ast.Ident)
						if !ok {
							return true
						}
						coreDir, inModule := scope[qualifier.Name]
						if !inModule || surfaceDirs[coreDir] {
							return true
						}
						if coreScope != "" && !strings.HasPrefix(coreDir+"/", coreScope) {
							return true
						}
						if !ast.IsExported(selector.Sel.Name) {
							return true
						}
						sites = append(sites, callSite{
							surface:  surface.Name,
							dir:      dir,
							file:     file,
							fn:       fn,
							call:     call,
							coreDir:  coreDir,
							funcName: selector.Sel.Name,
						})
						return true
					})
				}
			}
		}
	}
	return sites, nil
}

// sharedProducers are the producers called from two or more surfaces, keyed by
// producer key.
//
// Two surfaces is the whole criterion, and it is the parity subject rather than
// a proxy for it: one producer, called twice, hands two surfaces one result, and
// only then is there a question about whether both carry it. A producer one
// surface calls has no second projection to disagree with.
func sharedProducers(sites []callSite) map[string]map[string]bool {
	surfacesOf := map[string]map[string]bool{}
	for _, site := range sites {
		key := site.coreDir + "." + site.funcName
		if surfacesOf[key] == nil {
			surfacesOf[key] = map[string]bool{}
		}
		surfacesOf[key][site.surface] = true
	}
	for key, surfaces := range surfacesOf {
		if len(surfaces) < 2 {
			delete(surfacesOf, key)
		}
	}
	return surfacesOf
}

// readProducer reads one producer's declaration from its own package.
//
// A package-qualified name that resolves to a declared TYPE is turned away
// rather than read: it is a call expression syntactically and the caller's own
// value semantically, so there is no computed answer for two surfaces to carry
// differently, and reading it would report a finding about nothing.
//
// A name declared as a variable holding another package's function is followed
// one hop to that function, so a re-export reads as the thing it re-exports.
// Anything else a variable holds is reported unreadable rather than cleared: a
// function value could hand back a shared result and nothing here would see it.
//
// Deliberately NOT a criterion: whether the function returns an error. That was
// tried as a way to separate an operation from a value constructor and it is
// wrong — the shared computations that carry their diagnostics INSIDE the result
// return no error at all, and they are the ones this read most needs to see.
func readProducer(s *source, coreDir, funcName string, depth int) (producer, error) {
	p := producer{coreDir: coreDir, funcName: funcName, structResults: map[int]string{}}

	parsed, err := s.read(coreDir)
	if err != nil {
		return producer{}, err
	}

	var decl *ast.FuncDecl
	var alias ast.Expr
	var aliasFile *ast.File
	for _, file := range parsed.Files {
		for _, d := range file.Decls {
			switch node := d.(type) {
			case *ast.FuncDecl:
				if node.Recv == nil && node.Name.Name == funcName {
					decl = node
				}
			case *ast.GenDecl:
				for _, spec := range node.Specs {
					switch declared := spec.(type) {
					case *ast.TypeSpec:
						if node.Tok == token.TYPE && declared.Name.Name == funcName {
							p.skip = true
						}
					case *ast.ValueSpec:
						if node.Tok != token.VAR {
							continue
						}
						for i, name := range declared.Names {
							if name.Name == funcName && i < len(declared.Values) {
								alias, aliasFile = declared.Values[i], file
							}
						}
					}
				}
			}
		}
	}
	if p.skip {
		return p, nil
	}
	if decl == nil {
		if alias != nil && depth < 1 {
			if selector, ok := alias.(*ast.SelectorExpr); ok {
				if qualifier, ok := selector.X.(*ast.Ident); ok {
					if target, inModule := s.packageDirsInScope(aliasFile)[qualifier.Name]; inModule {
						return readProducer(s, target, selector.Sel.Name, depth+1)
					}
				}
			}
		}
		p.unreadable = "reaches no top-level function or type of that name, so it is a function value whose result this read cannot enumerate"
		return p, nil
	}
	if decl.Type.Results == nil {
		p.skip = true
		return p, nil
	}

	index := 0
	for _, field := range decl.Type.Results.List {
		count := max(len(field.Names), 1)
		name := typeName(field.Type)
		local := typeQualifier(field.Type) == ""
		for range count {
			if local && name != "" && ast.IsExported(name) {
				p.structResults[index] = name
			}
			index++
		}
	}
	if len(p.structResults) == 0 {
		p.skip = true
	}
	return p, nil
}

// readResultType reads one struct's exported field set from the package
// declaring it.
func readResultType(s *source, coreDir, name string) (resultType, error) {
	rt := resultType{coreDir: coreDir, name: name}

	parsed, err := s.read(coreDir)
	if err != nil {
		return resultType{}, err
	}

	structs := map[string]*ast.StructType{}
	declared := map[string]bool{}
	for _, file := range parsed.Files {
		for _, d := range file.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				declared[ts.Name.Name] = true
				if st, ok := ts.Type.(*ast.StructType); ok {
					structs[ts.Name.Name] = st
				}
			}
		}
	}
	if !declared[name] {
		rt.unreadable = "the package declares no such type"
		return rt, nil
	}
	if _, ok := structs[name]; !ok {
		// A named non-struct result carries no fields, so there is nothing to
		// project and nothing to compare. That is not a defect and not an
		// unreadable state: it is simply outside this read's subject.
		return rt, nil
	}

	fields, incomplete := structFields(structs, name, map[string]bool{})
	slices.Sort(fields)
	rt.fields = slices.Compact(fields)
	rt.unreadable = incomplete
	return rt, nil
}

// structFields are a struct's exported field names, promoting the fields of any
// struct it embeds from the same package.
//
// An embedded type from another package is reported as incomplete rather than
// approximated. Its promoted fields are readable through the outer value with no
// syntactic trace of where they came from, so a field set missing them would let
// a surface reading one of them look like a surface reading a field the struct
// does not have — and a set that may be short cannot support a claim that some
// other surface dropped something.
func structFields(structs map[string]*ast.StructType, name string, seen map[string]bool) (fields []string, incomplete string) {
	if seen[name] {
		return nil, ""
	}
	seen[name] = true

	st, ok := structs[name]
	if !ok {
		return nil, ""
	}
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			embedded := typeName(field.Type)
			if typeQualifier(field.Type) != "" {
				return nil, fmt.Sprintf("it embeds %s from another package, whose promoted fields this read cannot enumerate",
					embedded)
			}
			promoted, deeper := structFields(structs, embedded, seen)
			if deeper != "" {
				return nil, deeper
			}
			fields = append(fields, promoted...)
			// An embedded struct is also addressable by its own type name, which
			// a surface may carry whole rather than field by field.
			if ast.IsExported(embedded) {
				fields = append(fields, embedded)
			}
			continue
		}
		for _, ident := range field.Names {
			if ast.IsExported(ident.Name) {
				fields = append(fields, ident.Name)
			}
		}
	}
	return fields, ""
}

// flatParamNames are a function's parameter names in order, with an unnamed or
// blank parameter reported as empty so a caller can tell a named parameter from
// one the body cannot reach.
func flatParamNames(fn *ast.FuncDecl) (names []string, variadic bool) {
	if fn.Type.Params == nil {
		return nil, false
	}
	for _, field := range fn.Type.Params.List {
		if _, ok := field.Type.(*ast.Ellipsis); ok {
			variadic = true
		}
		if len(field.Names) == 0 {
			names = append(names, "")
			continue
		}
		for _, ident := range field.Names {
			if ident.Name == "_" {
				names = append(names, "")
				continue
			}
			names = append(names, ident.Name)
		}
	}
	return names, variadic
}

// funcDeclsByName indexes a package's top-level functions, so a value handed to
// a same-package helper can be followed one hop.
func funcDeclsByName(files []*ast.File) map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			out[fn.Name.Name] = fn
		}
	}
	return out
}
