// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package viewnarrowing

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"slices"
	"strings"
)

// checkTypeName is the only identifier this package anchors on. It is the type
// whose declarations are read, so knowing its name is unavoidable; everything
// else — the base of the view chain, the rungs, and which facts sit at each — is
// derived from source rather than named here.
const checkTypeName = "Check"

// Rung is one point on the view chain: the view interface addressable there, and
// the facts that interface declares itself.
//
// Facts are the accessor names declared directly on the interface, sorted. The
// facts a check may read at a rung are those plus every earlier rung's, which is
// what the embedding expresses; [Ladder.FactsAvailableAt] is that union.
type Rung struct {
	// Name is the view interface addressable at this rung.
	Name string

	// Facts are the accessor names that interface declares itself, sorted.
	Facts []string
}

// Ladder is the view chain as read from the source that declares it: the rungs
// in order, innermost first, and the rung at which each fact becomes readable.
//
// A Ladder is derived, never written. [ReadLadder] reads it from the interfaces
// and the embedding between them, so a fact moving to a different rung moves
// every verdict computed from it in the same edit that moves the fact.
type Ladder struct {
	base      string
	baseFacts []string
	rungs     []Rung

	// firstRung is the earliest rung index at which a fact is readable. A fact
	// declared on the base is readable at every rung, so it maps to 0.
	firstRung map[string]int
}

// Base is the interface every view embeds — the contract naming no fact of its
// own. It is not an addressable rung: a check declaring it declares that it
// reads nothing.
func (l Ladder) Base() string { return l.base }

// Rungs are the addressable points on the chain, innermost first.
func (l Ladder) Rungs() []Rung { return slices.Clone(l.rungs) }

// Index reports the position of a view on the chain, and whether the chain
// carries it at all. The base is not a rung and is reported as absent.
func (l Ladder) Index(view string) (int, bool) {
	for i, rung := range l.rungs {
		if rung.Name == view {
			return i, true
		}
	}
	return 0, false
}

// FactsAvailableAt are every fact a check declaring the rung at index may read:
// the rung's own facts, every earlier rung's, and any the base declares. Sorted.
func (l Ladder) FactsAvailableAt(index int) []string {
	out := slices.Clone(l.baseFacts)
	for i := 0; i <= index && i < len(l.rungs); i++ {
		out = append(out, l.rungs[i].Facts...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// NarrowestSufficient is the earliest rung at which every named fact is
// readable, which is where a check reading exactly those facts belongs.
//
// An empty fact set is satisfied at the innermost rung: a check reading no fact
// is placed by nothing, so the earliest rung is as good an answer as any and the
// caller is expected to report that shape rather than the index.
//
// unknown names the facts no rung declares, sorted. When it is non-empty the
// index means nothing and the caller must not draw a verdict from it: a fact the
// chain does not carry cannot be placed.
func (l Ladder) NarrowestSufficient(facts []string) (index int, unknown []string) {
	for _, fact := range facts {
		at, ok := l.firstRung[fact]
		if !ok {
			unknown = append(unknown, fact)
			continue
		}
		index = max(index, at)
	}
	slices.Sort(unknown)
	return index, slices.Compact(unknown)
}

// ReadLadder reads the view chain from the Go sources in dir.
//
// It finds the check type's declaration, takes the base of the chain from that
// type's first type-parameter constraint, collects every interface in dir that
// transitively embeds the base, and orders them by the embedding.
//
// It refuses rather than guesses. A chain that branches — two views embedding
// the same view, or one view embedding two — has no single narrowest sufficient
// answer for a check reading facts from both branches, so it is an error here
// rather than an arbitrary verdict later.
func ReadLadder(dir string) (Ladder, error) {
	files, err := parsePackageDir(dir)
	if err != nil {
		return Ladder{}, err
	}

	base, err := baseViewName(files)
	if err != nil {
		return Ladder{}, fmt.Errorf("read the base of the view chain from %s: %w", dir, err)
	}

	interfaces := map[string]ifaceShape{}
	for _, file := range files {
		for name, shape := range interfaceShapes(file) {
			interfaces[name] = shape
		}
	}
	if _, ok := interfaces[base]; !ok {
		return Ladder{}, fmt.Errorf("the check type constrains its view parameter by %s, which is declared as no interface in %s",
			base, dir)
	}

	views := viewClosure(base, interfaces)
	chain, err := orderChain(base, views, interfaces)
	if err != nil {
		return Ladder{}, err
	}

	ladder := Ladder{
		base:      base,
		baseFacts: interfaces[base].facts,
		firstRung: map[string]int{},
	}
	for _, fact := range ladder.baseFacts {
		ladder.firstRung[fact] = 0
	}
	for i, name := range chain {
		facts := interfaces[name].facts
		ladder.rungs = append(ladder.rungs, Rung{Name: name, Facts: facts})
		for _, fact := range facts {
			// A fact name declared at two rungs is readable from the earlier of
			// them, so the earliest declaration wins. Go admits the shape when
			// the signatures are identical, which is why it is handled here
			// rather than assumed away.
			if at, seen := ladder.firstRung[fact]; seen && at <= i {
				continue
			}
			ladder.firstRung[fact] = i
		}
	}
	if len(ladder.rungs) == 0 {
		return Ladder{}, fmt.Errorf("no interface in %s embeds %s, so the view chain has no rung and nothing can be placed on it",
			dir, base)
	}
	return ladder, nil
}

type ifaceShape struct {
	facts    []string
	embedded []string
}

func interfaceShapes(file *ast.File) map[string]ifaceShape {
	out := map[string]ifaceShape{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			iface, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			var shape ifaceShape
			for _, member := range iface.Methods.List {
				if len(member.Names) == 0 {
					if name := typeName(member.Type); name != "" {
						shape.embedded = append(shape.embedded, name)
					}
					continue
				}
				for _, name := range member.Names {
					shape.facts = append(shape.facts, name.Name)
				}
			}
			slices.Sort(shape.facts)
			slices.Sort(shape.embedded)
			out[ts.Name.Name] = shape
		}
	}
	return out
}

// baseViewName reads the base of the chain from the check type's first
// type-parameter constraint, so that the base is derived from the declaration
// the checks are written against rather than named here.
func baseViewName(files []*ast.File) (string, error) {
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != checkTypeName {
					continue
				}
				if ts.TypeParams == nil || len(ts.TypeParams.List) == 0 {
					return "", fmt.Errorf("%s declares no type parameter, so it names no view contract", checkTypeName)
				}
				first := ts.TypeParams.List[0]
				name := typeName(first.Type)
				if name == "" {
					return "", fmt.Errorf("%s's first type parameter is constrained by an unnamed type", checkTypeName)
				}
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("no declaration of %s", checkTypeName)
}

func viewClosure(base string, interfaces map[string]ifaceShape) map[string]bool {
	views := map[string]bool{base: true}
	for grew := true; grew; {
		grew = false
		for name, shape := range interfaces {
			if views[name] {
				continue
			}
			for _, embedded := range shape.embedded {
				if views[embedded] {
					views[name] = true
					grew = true
					break
				}
			}
		}
	}
	return views
}

// orderChain returns the rungs from the base outward, refusing any shape that is
// not a single line.
func orderChain(base string, views map[string]bool, interfaces map[string]ifaceShape) ([]string, error) {
	// Sorted, so that a chain with more than one defect reports the same defect
	// on every run rather than whichever one map iteration reached first.
	names := make([]string, 0, len(views))
	for name := range views {
		names = append(names, name)
	}
	slices.Sort(names)

	successors := map[string][]string{}
	for _, name := range names {
		if name == base {
			continue
		}
		var parents []string
		for _, embedded := range interfaces[name].embedded {
			if views[embedded] {
				parents = append(parents, embedded)
			}
		}
		slices.Sort(parents)
		if len(parents) == 0 {
			return nil, fmt.Errorf("%s is read as a view but embeds none, so it has no place on the chain", name)
		}
		if len(parents) > 1 {
			return nil, fmt.Errorf("%s embeds %s, so the view chain is not linear: a check reading facts from two branches has no single narrowest sufficient view",
				name, strings.Join(parents, " and "))
		}
		successors[parents[0]] = append(successors[parents[0]], name)
	}
	for _, parent := range names {
		children := successors[parent]
		if len(children) > 1 {
			slices.Sort(children)
			return nil, fmt.Errorf("%s is embedded by %s, so the view chain branches: a check reading facts from two branches has no single narrowest sufficient view",
				parent, strings.Join(children, " and "))
		}
	}

	var chain []string
	seen := map[string]bool{base: true}
	for at := base; ; {
		children := successors[at]
		if len(children) == 0 {
			break
		}
		next := children[0]
		if seen[next] {
			return nil, fmt.Errorf("the view chain cycles at %s", next)
		}
		seen[next] = true
		chain = append(chain, next)
		at = next
	}
	if len(chain)+1 != len(views) {
		var stranded []string
		for name := range views {
			if !seen[name] {
				stranded = append(stranded, name)
			}
		}
		slices.Sort(stranded)
		return nil, fmt.Errorf("%s embed %s but are not on the chain from it, so the chain is not a single line",
			strings.Join(stranded, " and "), base)
	}
	return chain, nil
}

// parsePackageDir parses dir's non-test Go sources. A check declared only in a
// test is not a shipped check, and a view declared only in a test is not part of
// the chain checks are written against.
func parsePackageDir(dir string) ([]*ast.File, error) {
	fset := token.NewFileSet()
	return parsePackageDirWith(fset, dir)
}

func parsePackageDirWith(fset *token.FileSet, dir string) ([]*ast.File, error) {
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dir, err)
	}
	var files []*ast.File
	var names []string
	byName := map[string]*ast.File{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			names = append(names, name)
			byName[name] = file
		}
	}
	// Sorted, because ParseDir hands back maps and every result derived from
	// this slice must not depend on their iteration order.
	slices.Sort(names)
	for _, name := range names {
		files = append(files, byName[name])
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s holds no non-test Go source", dir)
	}
	return files, nil
}

// typeName is the bare name of a type expression, ignoring any package
// qualifier, pointer, or type arguments — so that a view named plainly and the
// same view named through its package qualifier read alike.
func typeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.StarExpr:
		return typeName(e.X)
	case *ast.IndexExpr:
		return typeName(e.X)
	case *ast.IndexListExpr:
		return typeName(e.X)
	case *ast.ParenExpr:
		return typeName(e.X)
	}
	return ""
}

var errNoTypeArgs = errors.New("no type arguments")

// typeArgs are the type arguments of a generic instantiation.
func typeArgs(expr ast.Expr) ([]ast.Expr, error) {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return []ast.Expr{e.Index}, nil
	case *ast.IndexListExpr:
		return e.Indices, nil
	}
	return nil, errNoTypeArgs
}
