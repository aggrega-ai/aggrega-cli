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
//
// A second rule closes the same hole by the other route. A compilation PRODUCT
// type sits deliberately outside the authored-entity closure — facts over
// compilation products must remain declarable, and most of the graph, hydrated
// and rendered stages read one — so a return handing back a product struct
// passes the rule above cleanly while handing a check every field of that
// struct, including fields other facts already declare separately with their own
// placements. A check could then re-derive a declared fact from a product
// exactly as it could from a document. So a return may not mention a type whose
// field some declared fact names as its carrier.
//
// That rule keys on the registry's own carrier set rather than on what kind of
// type it is, and that is what makes it shippable. Every fact returns a value
// read FROM a carrier — that is what a fact is — so a rule phrased against
// relating to a carrier would refuse the whole fact set. A carrier appears in a
// fact's declared source, which states provenance; it appears in a fact's RESULT
// only when the accessor hands the carrier itself back. The rule reads the
// result and nothing else, so handing out a carrier is refused while reading a
// value out of one is admitted. Classifying by type provenance instead would
// refuse the graph stage entirely.

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

	carriers, carriersDeclared := declaredFieldCarriers(t, file)

	// Calibration for the owned-carrier rule's instrument, on the same reasoning
	// as the calibration above. The carrier set is read out of the accessors'
	// own godoc, which is wrapped at a fixed width, so an extractor that did not
	// normalize that wrapping — or one reading a prose shape that has since
	// changed — returns an empty set, passes every accessor, and is
	// indistinguishable from a clean tree. Require four known carriers to be
	// placed correctly first, each proving a different property of the extractor.
	if got := carriers["types.IRFeatureEntry"]; !got["Type"] || !got["ValuesProvenance"] {
		t.Fatalf("the carrier set does not record both declared fields of types.IRFeatureEntry (got %v), so "+
			"it is not a working instrument and its verdict on the accessors means nothing. This is the "+
			"carrier two facts read separately, at one stage, with their own placements — the case the rule "+
			"exists for — and reading it requires following a carrier that the fixed-width comment wrapping "+
			"puts on a line of its own", sortedKeys(got))
	}
	if got := carriers["topology.TopologyNode"]; !got["ResolvedParameters"] {
		t.Fatalf("the carrier set does not reach a carrier outside the authoring package (got %v for "+
			"topology.TopologyNode), so it is scoped to one package by accident and would miss every "+
			"carrier declared on another", sortedKeys(got))
	}
	if got, ok := carriers["types.IRGraph"]; ok {
		t.Fatalf("the carrier set records types.IRGraph as owning the field(s) %v, but no declared fact "+
			"reads a field of it. The set is meant to be derived from what the facts declare, not to be a "+
			"list of compilation-product types: a rule keyed on the latter would refuse every fact over a "+
			"compilation product, which is most of the later stages", sortedKeys(got))
	}
	if got, ok := carriers["renderer.NodeProvides"]; ok {
		t.Fatalf("the carrier set records the bare type renderer.NodeProvides as owning the field(s) %v. A "+
			"carrier named without a field owns no field, and treating it as owning every field would ban "+
			"its whole type from every result — the blanket ban this rule exists instead of", sortedKeys(got))
	}
	t.Logf("declared field carriers: %d types own a field some fact reads", len(carriers))

	var accessors, exceptions int
	var authoredViolations, carrierViolations, diagnosticViolations int
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

			var authoredHits, diagnosticHits, carrierHits []string
			for _, m := range mentions {
				pkg, sel, qualified := strings.Cut(m, ".")
				switch {
				case qualified && pkg == authoredTypesPkg && authored[sel]:
					authoredHits = append(authoredHits, m)
				case diagnosticShapeNames[sel], !qualified && diagnosticShapeNames[m]:
					diagnosticHits = append(diagnosticHits, m)
				}
				// Tested independently of the switch above: a type can be both
				// an authored entity and a declared carrier, and the two are
				// separate rules over one signature rather than alternatives.
				if fields := carriers[m]; len(fields) > 0 {
					carrierHits = append(carrierHits,
						m+" (carrying "+strings.Join(sortedKeys(fields), ", ")+")")
				}
			}

			if len(diagnosticHits) > 0 {
				diagnosticViolations++
				t.Errorf("%s mentions the diagnostic shape %s.\n"+
					"INVARIANT: an accessor returns a fact and never a diagnostic of any shape. A check "+
					"hands back a sentinel, a subject reference and a detail; the framework builds whatever "+
					"shape each caller needs.\n"+
					"CONSEQUENCE: a check that could obtain a diagnostic could assemble its own provenance, "+
					"and provenance must arrive from the subject so that it cannot participate in the "+
					"placement computation. A check reading late facts purely to build a locator would "+
					"otherwise be placed at the latest stage its diagnostic needs rather than the earliest "+
					"its predicate allows, and the computed placement would be wrong for the whole corpus "+
					"with nothing able to detect it.",
					name, strings.Join(diagnosticHits, ", "))
			}

			if len(carrierHits) > 0 {
				carrierViolations++
				if declaredException {
					t.Logf("REPORTED EXCEPTION — %s hands out the declared carrier %s. Declared as an "+
						"exception in the registry, so the guard reports it rather than failing on it. The "+
						"shape is open, not settled.", name, strings.Join(carrierHits, ", "))
				} else {
					t.Errorf("%s hands out the declared carrier %s.\n"+
						"INVARIANT: a result may not mention a type whose field some declared fact names as "+
						"its carrier. Reading a value out of a carrier is what every fact does; handing the "+
						"carrier itself back is what this refuses.\n"+
						"CONSEQUENCE: a check holding that structure can read the very field another fact "+
						"declares separately, at its own stage, with its own placement — so it can re-derive "+
						"a declared fact, and its own declaration stops describing what it reads. That is the "+
						"same re-derivation the authored-document rule closes, reached through a compilation "+
						"product instead of through a document, and the measured cost of it is a check that "+
						"recomputed an edge-creation rule, could not see bindings introduced further down the "+
						"pipeline, and rejected valid input.\n"+
						"Return the named fact instead: the scalar, name list or opaque ordered payload the "+
						"rule actually reads. Where a rule genuinely cannot be expressed that way, declare "+
						"the fact as a reported exception in the registry — never widen this guard to admit "+
						"it silently.",
						name, strings.Join(carrierHits, ", "))
				}
			}

			if len(authoredHits) == 0 {
				if declaredException && len(carrierHits) == 0 {
					t.Errorf("%s declares a reported exception to the no-raw-document rule, but its "+
						"signature mentions neither an authored-entity type nor a declared carrier.\n"+
						"A stale exception is worse than none: it reads as a known hole where there is "+
						"none, and it exempts the accessor from the guard for as long as it stands. "+
						"Remove the exception from the fact's declaration in the registry.", name)
				}
				continue
			}

			authoredViolations++
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
	if carriersDeclared != accessors {
		t.Errorf("%d of %d accessors declare the carrier they read from. Every accessor is meant to name "+
			"exactly one, and the owned-carrier rule is keyed on the set of them, so an accessor that names "+
			"none is a hole in the rule's own input rather than a documentation lapse.",
			carriersDeclared, accessors)
	}
	t.Logf("inspected %d accessors, all naming their source carrier; %d mention an authored-entity type, "+
		"%d hand out a declared carrier, %d mention a diagnostic shape, %d declare a reported exception",
		accessors, authoredViolations, carrierViolations, diagnosticViolations, exceptions)
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

// declaredFieldCarriers folds every accessor's declared source carrier into the
// set of product fields the fact set reads: the carrier type, mapped to the field
// names some declared fact names on it. It also reports how many accessors
// declared a carrier at all, so an accessor naming none cannot pass unnoticed.
//
// The carriers are read from the generated accessors rather than from a second
// source, because each accessor's godoc already names the carrier its fact reads,
// beside the fact it belongs to. A typed copy of the same set, or a second parse
// of the registry, would be a second place for the answer to live.
func declaredFieldCarriers(t *testing.T, file *ast.File) (map[string]map[string]bool, int) {
	t.Helper()

	owned := map[string]map[string]bool{}
	declared := 0
	for _, iface := range interfaceDecls(file) {
		for _, method := range iface.spec.Type.(*ast.InterfaceType).Methods.List {
			if len(method.Names) == 0 {
				continue
			}
			if _, ok := method.Type.(*ast.FuncType); !ok {
				continue
			}
			name := iface.name + "." + method.Names[0].Name

			carrier, occurrences := declaredCarrier(method.Doc.Text())
			switch {
			case occurrences == 0:
				continue
			case occurrences > 1:
				t.Errorf("%s names its source carrier %d times, so which one the rule reads is ambiguous. "+
					"An accessor names exactly one carrier; a second mention means prose elsewhere in the "+
					"godoc collides with the phrase the carrier is announced by.", name, occurrences)
				continue
			}
			declared++

			owner, field, ownsField, classified := carrierField(carrier)
			if !classified {
				t.Errorf("%s names the source carrier %q, which this guard cannot classify.\n"+
					"Three carrier shapes are admitted: a package-qualified type and field, a function "+
					"carrier, and a bare package-qualified type. A fourth shape is not proof that it owns a "+
					"field and not proof that it owns none, so the carrier set would be silently short by "+
					"one and the rule keyed on it correspondingly weaker. Classify the shape deliberately.",
					name, carrier)
				continue
			}
			if !ownsField {
				continue
			}
			if owned[owner] == nil {
				owned[owner] = map[string]bool{}
			}
			owned[owner][field] = true
		}
	}
	return owned, declared
}

// declaredCarrier extracts the source carrier an accessor's godoc names, and how
// many times it names one.
//
// The godoc is wrapped at a fixed width, so the carrier routinely lands on a line
// of its own and a reader matching against the raw text would miss it. Splitting
// on whitespace normalizes that away, and it is safe to do so because the wrap
// breaks between words: a carrier is one whitespace-free token, so it is never
// split.
func declaredCarrier(doc string) (string, int) {
	words := strings.Fields(doc)
	carrier, occurrences := "", 0
	for i := 0; i+2 < len(words); i++ {
		if words[i] == "read" && words[i+1] == "from" {
			occurrences++
			carrier = strings.TrimSuffix(words[i+2], ".")
		}
	}
	return carrier, occurrences
}

// carrierField splits a declared carrier into the type that owns a field and the
// field's name, for the one carrier shape that owns a field at all.
//
// The three shapes contribute differently, and the difference is what keeps the
// rule narrow. A package-qualified type and field is the only shape that owns a
// field, and it is the shape the rule is keyed on. A function carrier names a
// call and owns no field. A bare package-qualified type owns no field either:
// treating it as owning every field would ban its whole type from every result,
// which is the blanket ban this rule exists instead of.
//
// The leading segment is required to look like a package — lower-case initial —
// so that a type-and-field carrier written without its package is reported as
// unclassified rather than mistaken for a bare type, which would silently drop a
// field from the set.
func carrierField(carrier string) (owner, field string, ownsField, classified bool) {
	if strings.ContainsAny(carrier, "()") {
		return "", "", false, true
	}
	parts := strings.Split(carrier, ".")
	if len(parts) < 2 || parts[0] == "" || parts[0][0] < 'a' || parts[0][0] > 'z' {
		return "", "", false, false
	}
	switch len(parts) {
	case 2:
		return "", "", false, true
	case 3:
		return parts[0] + "." + parts[1], parts[2], true, true
	}
	return "", "", false, false
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
