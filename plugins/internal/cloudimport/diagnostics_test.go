// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// diagnosticBuilders returns the position of each place in files that makes a
// diagnostic outside Report: a literal of aggrega.Diagnostic, a list literal
// of them, or an assignment to a Severity or Code field.
func diagnosticBuilders(fset *token.FileSet, files []*ast.File) []string {
	isDiagnostic := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "aggrega" && sel.Sel.Name == "Diagnostic"
	}
	var found []string
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "Report" {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CompositeLit:
					if isDiagnostic(n.Type) {
						found = append(found, fset.Position(n.Pos()).String())
					}
					if list, ok := n.Type.(*ast.ArrayType); ok && isDiagnostic(list.Elt) && len(n.Elts) > 0 {
						found = append(found, fset.Position(n.Pos()).String())
					}
				case *ast.AssignStmt:
					for _, lhs := range n.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Severity" || sel.Sel.Name == "Code") {
							found = append(found, fset.Position(lhs.Pos()).String())
						}
					}
				}
				return true
			})
		}
	}
	return found
}

func TestVocabularySeverities(t *testing.T) {
	// The severity of each code, as the vocabulary's reference table states it.
	want := []struct {
		code     Code
		severity string
	}{
		{CodeSourceRefused, aggrega.SeverityHalt},
		{CodeParameterFilesNotRead, aggrega.SeverityWarn},
		{CodeLinkedSourceNotRead, aggrega.SeverityWarn},
		{CodeSubstrateLeftToOperator, aggrega.SeverityWarn},
		{CodeResourceNotCarried, aggrega.SeverityWarn},
		{CodeResourceFolded, aggrega.SeverityInfo},
		{CodeResourceDraftedFromSource, aggrega.SeverityInfo},
		{CodeReferenceNotWired, aggrega.SeverityWarn},
		{CodeValueNotCarried, aggrega.SeverityWarn},
		{CodeCredentialFlow, aggrega.SeverityWarn},
		{CodeDiscriminatorUnresolved, aggrega.SeverityWarn},
		{CodeRepetitionNotExpanded, aggrega.SeverityWarn},
		{CodeConditionNotCarried, aggrega.SeverityWarn},
		{CodeDeploymentScopeMerged, aggrega.SeverityWarn},
		{CodeAVMModuleRecognised, aggrega.SeverityInfo},
		{CodeModuleVersionUnpinned, aggrega.SeverityWarn},
		{CodeComponentExists, aggrega.SeverityWarn},
		{CodeProviderConfigurationNotCarried, aggrega.SeverityWarn},
	}
	spelled := map[Code]string{
		CodeSourceRefused: "source-refused", CodeParameterFilesNotRead: "parameter-files-not-read",
		CodeLinkedSourceNotRead: "linked-source-not-read", CodeSubstrateLeftToOperator: "substrate-left-to-operator",
		CodeResourceNotCarried: "resource-not-carried", CodeResourceFolded: "resource-folded",
		CodeResourceDraftedFromSource: "resource-drafted-from-source", CodeReferenceNotWired: "reference-not-wired",
		CodeValueNotCarried: "value-not-carried", CodeCredentialFlow: "credential-flow",
		CodeDiscriminatorUnresolved: "discriminator-unresolved", CodeRepetitionNotExpanded: "repetition-not-expanded",
		CodeConditionNotCarried: "condition-not-carried", CodeDeploymentScopeMerged: "deployment-scope-merged",
		CodeAVMModuleRecognised: "avm-module-recognised", CodeModuleVersionUnpinned: "module-version-unpinned",
		CodeComponentExists: "component-exists", CodeProviderConfigurationNotCarried: "provider-configuration-not-carried",
	}

	t.Run("each code has its severity", func(t *testing.T) {
		for _, w := range want {
			d := Report(w.code, "fix it")
			if d.Code != spelled[w.code] || d.Severity != w.severity || d.Message != "fix it" {
				t.Errorf("Report(%s) = %+v, want code %q severity %s", w.code, d, spelled[w.code], w.severity)
			}
		}
		if len(vocabulary) != len(want) {
			t.Errorf("the vocabulary holds %d codes, the reference table %d", len(vocabulary), len(want))
		}
		for _, entry := range vocabulary {
			if _, ok := spelled[entry.code]; !ok {
				t.Errorf("the vocabulary holds %s, which the reference table does not", entry.code)
			}
		}
	})

	t.Run("a code outside the vocabulary halts", func(t *testing.T) {
		if d := Report("no-such-code", "fix it"); d.Severity != aggrega.SeverityHalt {
			t.Fatalf("Report(no-such-code) = %+v, want a halt", d)
		}
	})

	t.Run("Report is the only constructor", func(t *testing.T) {
		fset := token.NewFileSet()
		planted, err := parser.ParseFile(fset, "planted.go", `package p
import "github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
func Report() aggrega.Diagnostic { return aggrega.Diagnostic{Code: "ok"} }
func literal() aggrega.Diagnostic { return aggrega.Diagnostic{Code: "x"} }
func pointer() *aggrega.Diagnostic { return &aggrega.Diagnostic{} }
func list() []aggrega.Diagnostic { return []aggrega.Diagnostic{{Code: "x"}} }
func field(d *aggrega.Diagnostic) { d.Severity = "warn" }
`, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := diagnosticBuilders(fset, []*ast.File{planted}); len(got) != 4 {
			t.Fatalf("the check finds %q in the planted source, want each of its four builders", got)
		}

		names, err := filepath.Glob("*.go")
		if err != nil {
			t.Fatal(err)
		}
		var files []*ast.File
		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		if len(files) == 0 {
			t.Fatal("no source file of the package was read")
		}
		if got := diagnosticBuilders(fset, files); len(got) != 0 {
			t.Fatalf("diagnostics made outside Report at %q", got)
		}
	})

	t.Run("messages say what to fix and quote no value", func(t *testing.T) {
		const planted = "7731"
		units := []Unit{{Path: "a.json"}, {Path: "b.json"}}
		_, several := ReadSettings(json.RawMessage(`{"root": ` + planted + `}`))
		_, _, none := SelectRoot(units, "")
		_, _, unnamed := SelectRoot(units, "planted-"+planted+".json")
		db := &Node{ID: "db", Name: Literal{JSON: json.RawMessage(`"planted-` + planted + `"`)},
			Body:   []Prop{{Name: "sku", Value: Literal{JSON: json.RawMessage(`"planted-` + planted + `"`)}}},
			Repeat: &Repetition{}}
		repeated := Expand(&Graph{Nodes: []*Node{db}})
		if several == nil || none == nil || unnamed == nil || len(repeated) != 1 {
			t.Fatalf("diagnostics = %+v, %+v, %+v, %+v", several, none, unnamed, repeated)
		}
		for _, c := range []struct {
			message string
			fix     []string
		}{
			{several.Message, []string{"root"}},
			{none.Message, []string{"root", "a.json", "b.json"}},
			{unnamed.Message, []string{"root", "a.json", "b.json"}},
			{repeated[0].Message, []string{"db", "Aggregate"}},
		} {
			if strings.Contains(c.message, planted) {
				t.Errorf("message %q quotes a value", c.message)
			}
			for _, fix := range c.fix {
				if !strings.Contains(c.message, fix) {
					t.Errorf("message %q does not name %s", c.message, fix)
				}
			}
		}
	})
}
