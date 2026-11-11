// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package guestwire_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"testing"
)

// draftNamer is the plugin kit's one function naming a drafted Aggregate and the
// Release beside it, as an importer's source calls it.
const (
	kitPackage = "importer"
	draftNamer = "DraftName"
)

// draftedEntity is one drafted Aggregate or Release an importer's source builds:
// where it is built, its kind, and why its name is not the kit's, empty where it
// is.
type draftedEntity struct {
	at, kind, bypass string
}

// TestEveryImporterNamesItsDraftThroughTheKit parses the source of every
// importer the plugins tree holds — each plugins/<name>-importer guest and each
// plugins/internal/<name>import core — and holds every drafted Aggregate and
// Release it builds, an EmittedEntity whose Kind is Aggregate or Release and whose
// Mode is not reference, to a name the plugin kit's DraftName returned: the call
// itself, or a variable every assignment of which, in the function building the
// entity, is that call. A name an importer composes itself — the namespace
// setting and a segment of its own — is refused, naming the file and the line.
//
// The importers are read from the tree rather than listed here, so an importer
// added tomorrow is held to the rule by being added. The cores drafting their
// own Aggregate are the ones listed in wantDrafting: a set difference names any
// that is not found drafting one, so a scan that read nothing, or that stopped
// recognising the literal, fails rather than passing over nothing. And the
// check is first run over a planted source holding each form it reads — the
// literal written with its package, bare, and with its type elided inside a
// composite of them, each composing its own name and required to be refused,
// and one whose var declaration takes the name from DraftName and is required
// to pass.
//
// The plugin kit holds its own drafts to the same rule by a twin check in its
// importer package, TestEveryDraftOfThePackageIsNamedThroughDraftName: the kit is
// a module published apart that imports nothing from this one, so the check is
// written once on each side.
//
// 🔴 PLANTED DEFECT: in Import (plugins/internal/composeimport/composeimport.go),
// write the name as importer.TargetNamespace(in.Settings) + "/flow": this test
// reddens naming composeimport.go and the line of each literal using it.
func TestEveryImporterNamesItsDraftThroughTheKit(t *testing.T) {
	t.Parallel()
	const planted = `package p

func (p *planted) selected(in aggrega.ImportInput) aggrega.EmittedEntity {
	return aggrega.EmittedEntity{Kind: "Aggregate", Name: importer.TargetNamespace(in.Settings) + "/flow", Mode: "draft"}
}

func (p *planted) bare(in aggrega.ImportInput) EmittedEntity {
	name := importer.TargetNamespace(in.Settings) + "/flow"
	return EmittedEntity{Kind: "Release", Name: name, Mode: "draft"}
}

func (p *planted) elided(in aggrega.ImportInput) []aggrega.EmittedEntity {
	var name = importer.TargetNamespace(in.Settings) + "/flow"
	return []aggrega.EmittedEntity{{Kind: "Aggregate", Name: name, Mode: "draft"}}
}

func (p *planted) declared(in aggrega.ImportInput) aggrega.EmittedEntity {
	var name = importer.DraftName(in)
	return aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft"}
}
`
	control := draftedEntities(t, "planted.go", []byte(planted))
	var refused []bool
	for _, e := range control {
		refused = append(refused, e.bypass != "")
	}
	if want := []bool{true, true, true, false}; !slices.Equal(refused, want) {
		t.Fatalf("the check found %+v in the planted source; want the selected, bare and elided literals refused "+
			"and the one whose var declaration calls %s.%s passed", control, kitPackage, draftNamer)
	}

	root := moduleRoot(t)
	guests, err := filepath.Glob(filepath.Join(root, "plugins", "*-importer"))
	if err != nil {
		t.Fatalf("glob the importer guests: %v", err)
	}
	cores, err := filepath.Glob(filepath.Join(root, "plugins", "internal", "*import"))
	if err != nil {
		t.Fatalf("glob the importer cores: %v", err)
	}
	dirs := append(guests, cores...)
	sort.Strings(dirs)

	drafting := map[string]bool{}
	for _, dir := range dirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatalf("relativize %s: %v", dir, err)
		}
		rel = filepath.ToSlash(rel)
		for _, path := range nonTestGoFiles(t, dir) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for _, e := range draftedEntities(t, rel+"/"+filepath.Base(path), src) {
				if e.bypass != "" {
					t.Errorf("%s builds a drafted %s whose name %s; an importer names its drafted Aggregate and "+
						"Release through %s.%s, so one source is named one way by every importer", e.at, e.kind,
						e.bypass, kitPackage, draftNamer)
				}
				if e.kind == "Aggregate" {
					drafting[rel] = true
				}
			}
		}
	}

	wantDrafting := []string{
		"plugins/internal/adkimport",
		"plugins/internal/agentframeworkimport",
		"plugins/internal/composeimport",
		"plugins/internal/crewaiimport",
		"plugins/internal/langgraphimport",
		"plugins/internal/mcpimport",
	}
	for _, want := range wantDrafting {
		if !drafting[want] {
			t.Errorf("found no drafted Aggregate in %s, which drafts one; the scan is not reading what it checks", want)
		}
	}
}

// draftedEntities parses one Go source and returns every drafted Aggregate and
// Release it builds, each with why its name is not the kit's, empty where it is.
func draftedEntities(t *testing.T, at string, src []byte) []draftedEntity {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, at, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", at, err)
	}
	var out []draftedEntity
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		assigned := assignments(fn.Body)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isEmittedEntity(lit.Type) {
				return true
			}
			fields := keyedFields(lit)
			kind := stringLiteral(fields["Kind"])
			if (kind != "Aggregate" && kind != "Release") || stringLiteral(fields["Mode"]) == "reference" {
				return true
			}
			e := draftedEntity{at: at + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line), kind: kind}
			e.bypass = nameBypass(fields["Name"], assigned)
			out = append(out, e)
			return true
		})
	}
	return out
}

// isEmittedEntity reports whether a composite literal's type is the kit's
// EmittedEntity, or is elided, as an element of a slice of them writes it — an
// elided literal is checked as one, since the Kind it is given says which it is.
func isEmittedEntity(typ ast.Expr) bool {
	switch t := typ.(type) {
	case nil:
		return true
	case *ast.SelectorExpr:
		return t.Sel.Name == "EmittedEntity"
	case *ast.Ident:
		return t.Name == "EmittedEntity"
	}
	return false
}

// keyedFields returns a composite literal's keyed elements by key.
func keyedFields(lit *ast.CompositeLit) map[string]ast.Expr {
	out := map[string]ast.Expr{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok {
			out[key.Name] = kv.Value
		}
	}
	return out
}

// stringLiteral returns the value of a string literal, empty for any other
// expression. It is a per-package test helper, kept local because no shared
// home exists for the helpers of source-reading guards.
func stringLiteral(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// assignments returns, for each variable a function body assigns by name, every
// expression assigned to it, in a := or = statement assigning one value per name
// or in a var declaration.
func assignments(body *ast.BlockStmt) map[string][]ast.Expr {
	out := map[string][]ast.Expr{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				var rhs ast.Expr
				if len(s.Rhs) == len(s.Lhs) {
					rhs = s.Rhs[i]
				}
				out[id.Name] = append(out[id.Name], rhs)
			}
		case *ast.ValueSpec:
			for i, id := range s.Names {
				var rhs ast.Expr
				if i < len(s.Values) {
					rhs = s.Values[i]
				}
				out[id.Name] = append(out[id.Name], rhs)
			}
		}
		return true
	})
	return out
}

// nameBypass says why a drafted entity's Name is not one the kit's DraftName
// returned, and is empty where it is.
func nameBypass(name ast.Expr, assigned map[string][]ast.Expr) string {
	switch e := name.(type) {
	case nil:
		return "is not set"
	case *ast.CallExpr:
		if isDraftNameCall(e) {
			return ""
		}
	case *ast.Ident:
		values := assigned[e.Name]
		if len(values) == 0 {
			return "is the variable " + e.Name + ", which the function does not assign"
		}
		if slices.ContainsFunc(values, func(v ast.Expr) bool {
			call, ok := v.(*ast.CallExpr)
			return !ok || !isDraftNameCall(call)
		}) {
			return "is the variable " + e.Name + ", assigned other than from " + kitPackage + "." + draftNamer
		}
		return ""
	}
	return "is composed in place rather than returned by " + kitPackage + "." + draftNamer
}

// isDraftNameCall reports whether a call is the kit's DraftName.
func isDraftNameCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != draftNamer {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == kitPackage
}
