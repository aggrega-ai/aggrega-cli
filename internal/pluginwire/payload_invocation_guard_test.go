// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginwire

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The payload-carrying invocations of a guest export, by what they accept. Two
// take an export as a value the host-export lists produce; two take a name, and
// exist for an export authored content names. The argument index is the export's
// position in the call.
const (
	invocationListed invocationKind = iota
	invocationAuthored
)

type invocationKind int

// invocationEntryPoints maps each payload-carrying invocation to its kind and to
// the position of every export argument it takes. InvokeAuthoredExport takes two:
// the listed default, and the name authored content gave.
var invocationEntryPoints = map[string][]invocationArg{
	"InvokeExport":            {{index: 3, kind: invocationListed}},
	"InvokeExportStatus":      {{index: 3, kind: invocationListed}},
	"InvokeAuthoredExport":    {{index: 3, kind: invocationListed}, {index: 4, kind: invocationAuthored}},
	"InvokeNamedExportStatus": {{index: 3, kind: invocationAuthored}},
}

type invocationArg struct {
	index int
	kind  invocationKind
}

// invocationSite is one export argument of one payload-carrying invocation: where
// it is, which entry point it is passed to, what kind of export that position
// accepts, and how the argument names the export.
type invocationSite struct {
	where string
	entry string
	kind  invocationKind
	// form is how the argument names its export: "literal" (a string literal),
	// "listed" (a wasm-package host-export identifier, or its name), "constructed"
	// (a value built at the call), or "bound" (a name or field bound elsewhere,
	// which the invocation's own type, or the authored field it reads, answers for).
	form string
	// export is the export a literal or listed argument names; empty otherwise.
	export string
}

// payloadInvocationSites walks every non-test Go file in the module and returns
// one entry per export argument of every payload-carrying invocation.
//
// Test files are excluded for the reason the decode census excludes them: a test
// drives an entry point directly, with whatever a fixture declares, and including
// them would report the fixture rather than a socket.
func payloadInvocationSites(t *testing.T, root string) []invocationSite {
	t.Helper()
	roles := governanceRoleIdents(t, root)
	exports := exportNameIdents(t, root, roles, pluginTypeIdents(t, root))

	var sites []invocationSite
	walkModuleSource(t, root, func(rel string, fset *token.FileSet, file *ast.File) {
		sites = append(sites, fileInvocationSites(fset, file, rel, exports)...)
	})
	sort.Slice(sites, func(i, j int) bool { return sites[i].where < sites[j].where })
	return sites
}

// fileInvocationSites returns one entry per export argument of every
// payload-carrying invocation in one parsed file. It reads the file it is handed
// and nothing else, so a synthetic source can show what it reports.
func fileInvocationSites(fset *token.FileSet, file *ast.File, rel string, exports map[string]string) []invocationSite {
	var sites []invocationSite
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		entry := calleeName(call.Fun)
		args, ok := invocationEntryPoints[entry]
		if !ok {
			return true
		}
		for _, a := range args {
			if a.index >= len(call.Args) {
				continue
			}
			form, export := classifyExportArg(call.Args[a.index], exports)
			sites = append(sites, invocationSite{
				where:  fmt.Sprintf("%s:%d", rel, fset.Position(call.Args[a.index].Pos()).Line),
				entry:  entry,
				kind:   a.kind,
				form:   form,
				export: export,
			})
		}
		return true
	})
	return sites
}

// classifyExportArg reads how one export argument names its export.
func classifyExportArg(arg ast.Expr, exports map[string]string) (form, export string) {
	switch v := arg.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			if err == nil {
				return "literal", s
			}
		}
		return "constructed", ""
	case *ast.CompositeLit:
		return "constructed", ""
	case *ast.SelectorExpr:
		if e, ok := exports[v.Sel.Name]; ok {
			return "listed", e
		}
		return "bound", ""
	case *ast.CallExpr:
		// The name of a listed identifier — wasm.X.Name() — names the listed export.
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
			if inner, ok := sel.X.(*ast.SelectorExpr); ok {
				if e, ok := exports[inner.Sel.Name]; ok {
					return "listed", e
				}
			}
		}
		return "bound", ""
	default:
		return "bound", ""
	}
}

// invocationComplaints returns one complaint per export argument that does not
// resolve to the host-export lists the way its position requires. It is a pure
// function so a planted site can prove it fires.
//
// A listed position must name a value the lists produced: a host-export
// identifier, or a name bound to one elsewhere — which the invocation's own type
// answers for, since no other value of that type exists. A value built at the
// call is the one thing the type admits that no list produced. An authored
// position must carry what authored content gave: a literal is a name the socket
// chose itself, and a listed export is one the socket should call through the
// listed entry point, where the type keeps it honest.
func invocationComplaints(sites []invocationSite) []string {
	var out []string
	for _, s := range sites {
		switch s.kind {
		case invocationListed:
			if s.form == "constructed" || s.form == "literal" {
				out = append(out, fmt.Sprintf(
					"the payload invocation at %s passes %s an export built at the call rather than one the "+
						"plugin-type registry's host-export lists produced: the only values of the host-export "+
						"type are the identifiers generated from those lists", s.where, s.entry))
			}
		case invocationAuthored:
			switch s.form {
			case "literal":
				out = append(out, fmt.Sprintf(
					"the payload invocation at %s passes %s the literal export %q: that entry point exists for "+
						"an export authored content names, and a name the socket spells itself is one a host-export "+
						"list must hold, called through InvokeExport", s.where, s.entry, s.export))
			case "listed":
				out = append(out, fmt.Sprintf(
					"the payload invocation at %s passes %s the listed export %q: a listed export is called "+
						"through InvokeExport, where its type keeps it one the lists hold, and that entry point is "+
						"for an export authored content names", s.where, s.entry, s.export))
			}
		}
	}
	sort.Strings(out)
	return out
}

func countKind(sites []invocationSite, kind invocationKind) int {
	n := 0
	for _, s := range sites {
		if s.kind == kind {
			n++
		}
	}
	return n
}

// hostExportConstructionComplaints reports every place a value of the host-export
// type is built outside the one constructor that builds it from the lists. That
// constructor is the reason the type's values are the lists' values, so a second
// construction site inside the wasm package — an exported function turning a
// string into one, say — would reopen exactly what the type closes.
func hostExportConstructionComplaints(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	walkModuleSource(t, root, func(rel string, fset *token.FileSet, file *ast.File) {
		out = append(out, fileHostExportConstructions(fset, file, rel)...)
	})
	sort.Strings(out)
	return out
}

// hostExportConstructor is the one function allowed to build a host-export value,
// and the file it lives in.
const (
	hostExportConstructor     = "listedExport"
	hostExportConstructorFile = "internal/wasm/hostexport.go"
)

// fileHostExportConstructions returns one complaint per host-export value built in
// one parsed file outside the constructor.
func fileHostExportConstructions(fset *token.FileSet, file *ast.File, rel string) []string {
	var out []string
	for _, decl := range file.Decls {
		allowed := false
		if fn, ok := decl.(*ast.FuncDecl); ok {
			allowed = rel == hostExportConstructorFile && fn.Recv == nil && fn.Name.Name == hostExportConstructor
		}
		if allowed {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var name string
			switch ty := lit.Type.(type) {
			case *ast.Ident:
				name = ty.Name
			case *ast.SelectorExpr:
				name = ty.Sel.Name
			}
			if name == "HostExport" {
				out = append(out, fmt.Sprintf(
					"a host-export value is built at %s:%d, outside %s in %s: the type's values are the lists' "+
						"values only while that constructor is the one place that builds them",
					rel, fset.Position(lit.Pos()).Line, hostExportConstructor, hostExportConstructorFile))
			}
			return true
		})
	}
	return out
}

// walkModuleSource parses every non-test Go file in the module, skipping the
// directories the decode census skips, and hands each to visit.
func walkModuleSource(t *testing.T, root string, visit func(rel string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		visit(filepath.ToSlash(rel), fset, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk module source: %v", err)
	}
}

// TestPayloadInvocationCensusCanFail proves the invocation census and the
// construction census can refuse. The real tree agrees with both today, so the
// only way to show either fires is a synthetic source carrying each disagreement
// beside the forms that pass.
func TestPayloadInvocationCensusCanFail(t *testing.T) {
	t.Parallel()

	exports := map[string]string{"LintRulesExport": "lint_rules"}
	const src = `package synthetic

func listedIdent() { wasm.InvokeExport(ctx, p, d, wasm.LintRulesExport, payload) }

func listedParam(export wasm.HostExport) { wasm.InvokeExport(ctx, p, d, export, payload) }

func built() { wasm.InvokeExport(ctx, p, d, wasm.HostExport{}, payload) }

func authoredField() { wasm.InvokeAuthoredExport(ctx, p, d, wasm.LintRulesExport, ref.Function, payload) }

func authoredLiteral() { wasm.InvokeAuthoredExport(ctx, p, d, wasm.LintRulesExport, "lint_chosen_here", payload) }

func authoredListed() { wasm.InvokeNamedExportStatus(ctx, p, d, wasm.LintRulesExport.Name(), payload) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse the synthetic source: %v", err)
	}

	sites := fileInvocationSites(fset, file, "synthetic.go", exports)
	if countKind(sites, invocationListed) == 0 || countKind(sites, invocationAuthored) == 0 {
		t.Fatalf("the census read no listed or no authored argument out of the synthetic source: %+v", sites)
	}
	got := strings.Join(invocationComplaints(sites), "\n")
	for _, want := range []string{
		"synthetic.go:7 passes InvokeExport an export built at the call",
		`synthetic.go:11 passes InvokeAuthoredExport the literal export "lint_chosen_here"`,
		`synthetic.go:13 passes InvokeNamedExportStatus the listed export "lint_rules"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the census does not report %q; it reported:\n%s", want, got)
		}
	}
	for _, clean := range []string{"synthetic.go:3 ", "synthetic.go:5 ", "synthetic.go:9 "} {
		if strings.Contains(got, clean) {
			t.Errorf("the census reports a form that resolves to the lists (%s); it reported:\n%s", clean, got)
		}
	}

	constructions := fileHostExportConstructions(fset, file, "synthetic.go")
	if len(constructions) != 1 || !strings.Contains(constructions[0], "synthetic.go:7") {
		t.Errorf("the construction census reports %v; want exactly the value built at synthetic.go:7", constructions)
	}

	const constructor = `package wasm

func listedExport(t plugintype.Type, name string) HostExport { return HostExport{name: name} }

func FromString(name string) HostExport { return HostExport{name: name} }
`
	cfile, err := parser.ParseFile(fset, "hostexport.go", constructor, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse the synthetic constructor: %v", err)
	}
	got2 := fileHostExportConstructions(fset, cfile, hostExportConstructorFile)
	if len(got2) != 1 || !strings.Contains(got2[0], ":5") {
		t.Errorf("the construction census reports %v; want exactly the second constructor, at line 5", got2)
	}
}
