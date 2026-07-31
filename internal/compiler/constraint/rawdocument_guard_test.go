// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// The no-raw-document guard.
//
// A view exposes named facts and never a raw document, and that single property
// is what makes fact re-derivation impossible: a check handed no raw structure
// has nothing to derive from, so its declared view describes what it reads
// exhaustively rather than approximately. Placement, caller coverage and
// traversal are all computed from that declaration, so they are only as sound as
// it is exhaustive.
//
// Three surfaces defend the property, with different jobs and different timing.
// The generated documentation explains it — read before someone violates it,
// which is exactly when they are not reading it. The covering record justifies
// it. This test intercepts, and it is the only one of the three present at the
// moment the mistake is made, which is why its failure message names the
// invariant and the consequence rather than only the offending signature.
//
// The rule the guard implements is transitive on both sides. A return type is
// refused when it MENTIONS an authored-entity type anywhere — by pointer or by
// value, inside a container, or as a type argument — and a type counts as
// authored-entity when it is reachable by field traversal from an entity a user
// writes. A guard phrased to refuse only a pointer would pass an ordered
// container of an authored-entity type, which hands a check the same authoring
// detail through a value.

const (
	generatedViewsFile = "views_gen.go"
	authoredTypesDir   = "../../../pkg/types"
	authoredTypesPkg   = "types"
)

// diagnosticShapeNames are the type names bound to a diagnostic shape in this
// compiler. An accessor returning any of them is refused wherever it is declared,
// so the rule keys on the name rather than on a package list.
//
// Measured rather than guessed: four types named Diagnostic exist, one per
// boundary that emits one — the plugin wire shape, the render context, the
// editor surface and the tolerant lockfile load — plus the schema validator's
// Finding and this package's own Objection.
var diagnosticShapeNames = map[string]bool{
	"Diagnostic": true,
	"Finding":    true,
	"Objection":  true,
}

var predeclaredTypeNames = map[string]bool{
	"any": true, "bool": true, "byte": true, "complex128": true, "complex64": true,
	"error": true, "float32": true, "float64": true, "int": true, "int16": true,
	"int32": true, "int64": true, "int8": true, "rune": true, "string": true,
	"uint": true, "uint16": true, "uint32": true, "uint64": true, "uint8": true,
	"uintptr": true,
}

func TestNoAccessorMentionsAnAuthoredEntityType(t *testing.T) {
	t.Parallel()

	authored := authoredEntityTypeNames(t)

	// Calibration. A guard whose classifier silently returned nothing would
	// pass every accessor, and the pass would be indistinguishable from a
	// clean tree. So require the classifier to place four known types
	// correctly before believing anything it says about the accessors.
	for _, want := range []string{"ComponentInstance", "FeatureInstance", "VerboseUsesEntry"} {
		if !authored[want] {
			t.Fatalf("the authored-entity classifier does not reach types.%s, so it is not a working "+
				"instrument and its verdict on the accessors means nothing", want)
		}
	}
	for _, notWant := range []string{"IRGraph", "IRFeatureEntry", "FieldProvenance", "OrderedMap"} {
		if authored[notWant] {
			t.Fatalf("the authored-entity classifier reaches types.%s, which is a compilation product or "+
				"a container rather than something a user authors; the classifier is too broad and would "+
				"refuse correct accessors", notWant)
		}
	}
	t.Logf("authored-entity classifier: %d types reachable from the entity roots", len(authored))

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, generatedViewsFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", generatedViewsFile, err)
	}
	locals := localTypeDecls(file)

	var accessors, exceptions, violations int
	for _, iface := range interfaceDecls(file) {
		for _, method := range iface.spec.Type.(*ast.InterfaceType).Methods.List {
			if len(method.Names) == 0 {
				continue // an embedded view, not an accessor
			}
			fn, ok := method.Type.(*ast.FuncType)
			if !ok {
				continue
			}
			accessors++
			name := iface.name + "." + method.Names[0].Name
			declaredException := strings.Contains(method.Doc.Text(), "Reported exception:")
			if declaredException {
				exceptions++
			}

			mentions, unknown := mentionedTypeNames(fn, locals)
			if len(unknown) > 0 {
				t.Errorf("%s mentions %s, which this guard cannot classify.\n"+
					"An accessor's signature is admitted only when every type in it is a predeclared type, a "+
					"record declared beside the views, or a type from the authoring package — because those "+
					"are the shapes the guard can decide. A type it cannot decide is not proof of a "+
					"violation and is not proof of safety either, which is the state the guard exists to "+
					"prevent. Either express the fact in an admitted shape, or extend the classifier "+
					"deliberately.", name, strings.Join(unknown, ", "))
				continue
			}

			var authoredHits, diagnosticHits []string
			for _, m := range mentions {
				pkg, sel, qualified := strings.Cut(m, ".")
				switch {
				case qualified && pkg == authoredTypesPkg && authored[sel]:
					authoredHits = append(authoredHits, m)
				case diagnosticShapeNames[sel], !qualified && diagnosticShapeNames[m]:
					diagnosticHits = append(diagnosticHits, m)
				}
			}

			if len(diagnosticHits) > 0 {
				violations++
				t.Errorf("%s mentions the diagnostic shape %s.\n"+
					"INVARIANT: an accessor returns a fact and never a diagnostic of any shape. A check "+
					"hands back a sentinel, a subject reference and a detail; the framework builds whatever "+
					"shape each caller needs.\n"+
					"CONSEQUENCE: a check that could obtain a diagnostic could assemble its own provenance, "+
					"and provenance must arrive from the subject so that it cannot participate in the "+
					"placement computation. A check reading late facts purely to build a locator would "+
					"otherwise be placed at the latest rung its diagnostic needs rather than the earliest "+
					"its predicate allows, and the computed placement would be wrong for the whole corpus "+
					"with nothing able to detect it.",
					name, strings.Join(diagnosticHits, ", "))
			}

			if len(authoredHits) == 0 {
				if declaredException {
					t.Errorf("%s declares a reported exception to the no-raw-document rule, but its "+
						"signature mentions no authored-entity type.\n"+
						"A stale exception is worse than none: it reads as a known hole where there is "+
						"none, and it exempts the accessor from the guard for as long as it stands. "+
						"Remove the exception from the fact's declaration in the registry.", name)
				}
				continue
			}

			violations++
			if declaredException {
				t.Logf("REPORTED EXCEPTION — %s mentions %s. Declared as an exception in the registry, so "+
					"the guard reports it rather than failing on it. The shape is open, not settled.",
					name, strings.Join(authoredHits, ", "))
				continue
			}
			t.Errorf("%s mentions the authored-entity type(s) %s.\n"+
				"INVARIANT: a view exposes named facts and never a raw document. No accessor mentions an "+
				"authored-entity type — not by pointer, not by value, not inside a container, and not as a "+
				"type argument.\n"+
				"CONSEQUENCE: a check handed an authored entity can read every field of it and re-derive "+
				"any fact from it, so its declared view stops describing what it reads. The declaration is "+
				"what placement, caller coverage and traversal are computed from, so one accessor like this "+
				"reopens fact re-derivation for every check in the framework — not only for the one that "+
				"calls it.\n"+
				"Return a named fact instead: a scalar, a name list, or an opaque ordered payload whose "+
				"entries carry no authoring type. Where a rule genuinely cannot be expressed that way, "+
				"declare the fact as a reported exception in the registry — never widen this guard to "+
				"admit it silently.",
				name, strings.Join(authoredHits, ", "))
		}
	}

	if accessors == 0 {
		t.Fatalf("no accessor was inspected in %s, so a clean result certifies nothing", generatedViewsFile)
	}
	t.Logf("inspected %d accessors; %d mention an authored-entity type, %d declare a reported exception",
		accessors, violations, exceptions)
}

type interfaceDecl struct {
	name string
	spec *ast.TypeSpec
}

func interfaceDecls(file *ast.File) []interfaceDecl {
	var out []interfaceDecl
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
			if _, ok := ts.Type.(*ast.InterfaceType); ok {
				out = append(out, interfaceDecl{name: ts.Name.Name, spec: ts})
			}
		}
	}
	return out
}

func localTypeDecls(file *ast.File) map[string]*ast.TypeSpec {
	out := map[string]*ast.TypeSpec{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				out[ts.Name.Name] = ts
			}
		}
	}
	return out
}

// mentionedTypeNames walks an accessor's signature and returns every type it
// mentions, following local record declarations so that a record's fields are
// mentions of the accessor that returns it. Names it cannot resolve are returned
// separately rather than assumed safe.
func mentionedTypeNames(fn *ast.FuncType, locals map[string]*ast.TypeSpec) (mentions, unknown []string) {
	found := map[string]bool{}
	unresolved := map[string]bool{}
	visited := map[string]bool{}

	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := x.X.(*ast.Ident); ok {
					found[pkg.Name+"."+x.Sel.Name] = true
					return false
				}
				unresolved["a selector on a non-package expression"] = true
				return false
			case *ast.Ident:
				switch {
				case predeclaredTypeNames[x.Name]:
				case diagnosticShapeNames[x.Name]:
					// Classified rather than left unresolved, so the
					// refusal carries the diagnostic-shape reasoning
					// rather than the weaker "cannot classify".
					found[x.Name] = true
				case locals[x.Name] != nil:
					found[x.Name] = true
					if !visited[x.Name] {
						visited[x.Name] = true
						walk(locals[x.Name].Type)
					}
				default:
					unresolved[x.Name] = true
				}
				return false
			case *ast.Field:
				walk(x.Type)
				return false
			}
			return true
		})
	}

	for _, group := range []*ast.FieldList{fn.Params, fn.Results} {
		if group == nil {
			continue
		}
		for _, f := range group.List {
			walk(f.Type)
		}
	}
	return sortedKeys(found), sortedKeys(unresolved)
}

// authoredEntityTypeNames returns the names of the authoring package's types
// reachable by field traversal from an entity a user authors.
//
// The seed is derived rather than listed. An entity root is a struct declaring
// both an apiVersion and a kind WITH yaml tags, because YAML is the authoring
// surface: the one struct in the package that declares those two fields without
// yaml tags is the compilation's own graph, which is a product nobody authors and
// which must stay out of the closure so that facts over compilation products are
// not mistaken for facts over documents.
//
// A type counts as authored-entity when it is reachable, carries no type
// parameters and is a struct. Excluding parameterised types is what lets an
// opaque ordered payload be a fact while an ordered container of an
// authored-entity type is not: judgement lands on the type argument, which the
// traversal reaches, rather than on the container.
func authoredEntityTypeNames(t *testing.T) map[string]bool {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, authoredTypesDir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the authoring package at %s: %v", authoredTypesDir, err)
	}

	decls := map[string]*ast.TypeSpec{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for name, spec := range localTypeDecls(file) {
				decls[name] = spec
			}
		}
	}
	if len(decls) == 0 {
		t.Fatalf("no type declarations found at %s, so the classifier has nothing to work from",
			authoredTypesDir)
	}

	var queue []string
	for name, spec := range decls {
		if st, ok := spec.Type.(*ast.StructType); ok && declaresYAMLTaggedEnvelope(st) {
			queue = append(queue, name)
		}
	}
	if len(queue) == 0 {
		t.Fatalf("no authored entity root found at %s; the seed criterion (an apiVersion and a kind, "+
			"both yaml-tagged) no longer matches the authoring types, so the closure would be empty and "+
			"the guard would pass everything", authoredTypesDir)
	}
	sort.Strings(queue)
	t.Logf("authored-entity roots: %s", strings.Join(queue, ", "))

	reached := map[string]bool{}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if reached[name] {
			continue
		}
		reached[name] = true
		spec, ok := decls[name]
		if !ok {
			continue
		}
		ast.Inspect(spec.Type, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && decls[id.Name] != nil && !reached[id.Name] {
				queue = append(queue, id.Name)
			}
			return true
		})
	}

	authored := map[string]bool{}
	for name := range reached {
		spec := decls[name]
		if spec == nil || spec.TypeParams != nil {
			continue
		}
		if _, ok := spec.Type.(*ast.StructType); ok {
			authored[name] = true
		}
	}
	return authored
}

func declaresYAMLTaggedEnvelope(st *ast.StructType) bool {
	var apiVersion, kind bool
	for _, f := range st.Fields.List {
		if f.Tag == nil || !strings.Contains(f.Tag.Value, `yaml:"`) {
			continue
		}
		for _, n := range f.Names {
			switch n.Name {
			case "APIVersion":
				apiVersion = true
			case "Kind":
				kind = true
			}
		}
	}
	return apiVersion && kind
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
