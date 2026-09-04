// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// templateScopes is every scope in the package that declares a narrowing, keyed by
// the variable that holds it. A narrowing is held to its context type only by the
// guard that walks this list, so a scope declaring one and absent here would be
// unguarded — which is why the guard also cross-checks this list against the
// package's own source rather than trusting it to be complete.
var templateScopes = map[string]templateScope{
	"providesScope":                 providesScope,
	"boundaryScopeFRScope":          boundaryScopeFRScope,
	"pathTemplateDirectoryScope":    pathTemplateDirectoryScope,
	"pathTemplateSubDirectoryScope": pathTemplateSubDirectoryScope,
	"selfInjectScope":               selfInjectScope,
	"recipeLocalsScope":             recipeLocalsScope,
	"featureRecipeLocalsScope":      featureRecipeLocalsScope,
	"attachmentFRScope":             attachmentFRScope,
	"attachmentFRRenderScope":       attachmentFRRenderScope,
}

// scopeSurfaces is every context type a template in this package is evaluated
// against. The depth-2 gate is derived from the type, so this is the full set of
// types whose derivation the tests below have to hold for.
func scopeSurfaces(t *testing.T) []struct {
	name string
	data any
} {
	t.Helper()
	return []struct {
		name string
		data any
	}{
		{"types.TemplateContext", populatedTemplateContext(t)},
		{"ProvidesContext", ProvidesContext{}},
		{"LocalsContext", LocalsContext{}},
		{"SelfInjectContext", SelfInjectContext{}},
		{"PathTemplateContext", PathTemplateContext{}},
		{"AssertionContext", AssertionContext{}},
	}
}

// The open/closed split is read off the context type, never declared. This prints
// the whole derivation for every surface so what the gate actually admits is
// readable from the test log rather than inferred from the types, and pins the
// exact gated-root set for the render context — the claim the whole mechanism
// rests on.
func TestTemplateScope_DerivationIsReadOffTheType(t *testing.T) {
	t.Parallel()
	for _, surface := range scopeSurfaces(t) {
		scope := templateScope{data: surface.data}
		roots, ok := scope.admittedRoots()
		if !ok {
			t.Fatalf("%s: root set must be derivable from the type", surface.name)
		}
		var gated, open []string
		for _, root := range roots {
			keys, isGated := scope.admittedKeys(root)
			if !isGated {
				open = append(open, root)
				continue
			}
			gated = append(gated, root)
			t.Logf("%s: GATED .%s admits %s", surface.name, root, strings.Join(keys, ", "))
		}
		t.Logf("%s: gated roots  = %s", surface.name, strings.Join(gated, ", "))
		t.Logf("%s: open roots   = %s", surface.name, strings.Join(open, ", "))

		if surface.name != "types.TemplateContext" {
			continue
		}
		wantGated := []string{"context", "metadata", "inject", "workload", "boundary"}
		if strings.Join(gated, ",") != strings.Join(wantGated, ",") {
			t.Errorf("render-context gated roots = %v, want exactly %v", gated, wantGated)
		}
		// The pillars every catalog optional-key idiom reads through must stay
		// open. A gate on any of them refuses keys the author legitimately wrote.
		for _, mustBeOpen := range []string{"values", "infra", "locals", "provides", "features", "steps"} {
			if slices.Contains(gated, mustBeOpen) {
				t.Errorf(".%s is gated; an ordered-map pillar must stay open so its optional keys keep resolving", mustBeOpen)
			}
		}
	}
}

// An OrderedMap is a Go struct with two unexported fields, so deciding closedness
// by Kind alone would gate every ordered-map pillar with an EMPTY admitted set and
// halt on every key the author wrote. The discriminator has to come from what the
// context walk delivers, which is the marker interface — asserted here directly on
// the derivation so a Kind-based regression fails at the type level rather than as
// a catalog-wide halt.
func TestClosedRootKeys_OrderedMapIsOpenDespiteBeingAStruct(t *testing.T) {
	t.Parallel()
	if keys, closed := closedRootKeys(reflect.TypeOf(types.OrderedMap[any]{})); closed {
		t.Errorf("OrderedMap[any] must report open; reported closed with keys %v", keys)
	}
	if keys, closed := closedRootKeys(reflect.TypeOf(&types.OrderedMap[any]{})); closed {
		t.Errorf("*OrderedMap[any] must report open; reported closed with keys %v", keys)
	}
	if keys, closed := closedRootKeys(reflect.TypeOf(types.OrderedMap[types.TemplateAttachment]{})); closed {
		t.Errorf("OrderedMap[TemplateAttachment] must report open; reported closed with keys %v", keys)
	}
	// The positive arm: a plain struct is closed, and a pointer to one is closed
	// after the pointer is peeled.
	keys, closed := closedRootKeys(reflect.TypeOf(&types.TemplateMetadata{}))
	if !closed {
		t.Fatal("*TemplateMetadata must report closed")
	}
	if !slices.Contains(keys, "release_name") {
		t.Errorf("closed key set %v omits release_name", keys)
	}
}

// reflect.Type.Elem is defined only for Array, Chan, Map, Pointer and Slice, so
// peeling an interface-typed field panics. Two render-context pillars are declared
// as bare interfaces, so the derivation walks one on every render of a populated
// context; this pins that they resolve as open instead of panicking.
func TestClosedRootKeys_InterfaceTypedRootIsOpenAndDoesNotPanic(t *testing.T) {
	t.Parallel()
	scope := templateScope{data: populatedTemplateContext(t)}
	for _, root := range []string{"source", "inject_raw"} {
		keys, gated := scope.admittedKeys(root)
		if gated {
			t.Errorf(".%s is declared as an interface and must report open; reported gated with keys %v", root, keys)
		}
	}
	// Whatever the interface actually holds at render time is irrelevant to the
	// derivation, which reads the declared type — including a struct value, the
	// case a value-based derivation would wrongly report closed.
	ctx := populatedTemplateContext(t)
	ctx.Source = types.TemplateMetadata{ReleaseName: "rel"}
	if keys, gated := (templateScope{data: ctx}).admittedKeys("source"); gated {
		t.Errorf(".source must stay open when it happens to hold a struct; reported gated with keys %v", keys)
	}
	if _, err := EvalTemplate("probe", `{{ .source.whatever }}{{ .inject_raw.anything }}`, ctx); err != nil {
		t.Errorf("a reference under an interface-typed pillar must not halt: %v", err)
	}
}

// A mistyped key under a closed pillar used to reach the emitted artifact as an
// empty value at exit 0. Each of these now halts, and the diagnostic names the
// authored path and the keys the pillar does define.
func TestEvalTemplate_MistypedKeyUnderClosedPillarHalts(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	cases := map[string]struct {
		tmpl     string
		wantPath string
		wantSaid string
	}{
		"context":  {`{{ .context.nmae }}`, ".context.nmae", ".context.instance_id"},
		"metadata": {`{{ .metadata.typo }}`, ".metadata.typo", ".metadata.release_name"},
		"workload": {`{{ .workload.containres }}`, ".workload.containres", ".workload.containers"},
		"boundary": {`{{ .boundary.nmae }}`, ".boundary.nmae", ".boundary.boundary_name"},
		"inject":   {`{{ .inject.nosuch }}`, ".inject.nosuch", ".inject.env"},
		// The wrapped spelling is the one that used to launder the typo into an
		// empty value, so it has to halt identically to the bare one.
		"wrapped": {`{{ (default dict .context.nmae).deeper }}`, ".context.nmae", ".context.instance_id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := EvalTemplate("probe", tc.tmpl, ctx)
			if err == nil {
				t.Fatalf("expected halt, rendered %q at no error", out)
			}
			if !errors.Is(err, ErrTemplateUnknownRoot()) {
				t.Errorf("expected ErrTemplateUnknownRoot, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("diagnostic does not name the authored path %s: %v", tc.wantPath, err)
			}
			if !strings.Contains(err.Error(), tc.wantSaid) {
				t.Errorf("diagnostic does not offer %s as an available key: %v", tc.wantSaid, err)
			}
			t.Logf("diagnostic: %v", err)
		})
	}
}

// The anti-requirement of the depth-2 gate: an absent KEY under an OPEN pillar is
// normal authoring and keeps its missingkey=zero optionality untouched. A gate on
// an open pillar would break every one of these.
func TestEvalTemplate_OpenPillarOptionalityIsUntouched(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	forms := []string{
		`{{ .values.absent | default "x" }}`,
		`{{ optional .infra "absent" 1 }}`,
		`{{ .locals.absent }}`,
		`{{ .provides.absent }}`,
		`{{ .values.absent }}`,
		`{{ (default dict .values.absent).deeper }}`,
		`{{ .features.nosuchattachment }}`,
		`{{ .steps.nosuchstep }}`,
		`{{ index .values "absent" }}`,
		// A whole-root reference is admitted whenever its root is, closed pillars
		// included — refusing one would break every defensive wrapper.
		`{{ default dict .context }}`,
		`{{ default dict .workload }}`,
		// A key that IS declared on a closed pillar renders, at any depth below.
		`{{ .context.instance_id }}`,
		`{{ .context.uses.db.target }}`,
		`{{ .metadata.release_name }}`,
	}
	for _, tmpl := range forms {
		if _, err := EvalTemplate("probe", tmpl, ctx); err != nil {
			t.Errorf("form %q must not halt, got: %v", tmpl, err)
		}
	}
}

// Every narrowing a surface declares has to exist in its context type. A pillar
// that is renamed or retired leaves a narrowing naming nothing, which silently
// changes what the surface admits — so the narrowing is held to the type here
// rather than by review.
func TestTemplateScope_DeclaredNarrowingsExistInTheirUniverse(t *testing.T) {
	t.Parallel()
	for _, name := range sortedScopeNames() {
		if problems := templateScopes[name].validate(); len(problems) > 0 {
			t.Errorf("%s declares a narrowing its context type does not define:\n  %s",
				name, strings.Join(problems, "\n  "))
		}
	}
}

// The guard above only reaches the scopes templateScopes lists, so a scope that
// declares a narrowing and is not registered would be unguarded. This reads the
// package's own source and requires every package-level scope variable to be
// registered — the check a census of the registry itself structurally cannot make.
//
// Registration is required for every package-level scope, not only for one that
// declares a narrowing, because "declares a narrowing" is exactly the property that
// changes over time: a surface that narrows nothing today and gains an exclusion
// tomorrow would otherwise become unguarded by the edit that gave it something to
// guard. The narrowing distinction is still counted, for the one thing it decides:
// a narrowing written inside a function body is held by no variable, so no
// registration can reach it.
func TestTemplateScope_EveryPackageLevelScopeIsRegistered(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	declaredScope := map[string]bool{}     // package-level var name → holds a scope literal
	declaredNarrowing := map[string]bool{} // …and that literal declares a narrowing
	totalNarrowing := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, v := range value.Values {
					if i >= len(value.Names) || !isTemplateScopeLit(v) {
						continue
					}
					declaredScope[value.Names[i].Name] = true
					if declaresNarrowing(v) {
						declaredNarrowing[value.Names[i].Name] = true
					}
				}
			}
		}
		// Every narrowing-declaring literal anywhere in the file, so one written
		// inside a function — where no variable name can register it — is caught
		// by the count rather than missed by the shape.
		ast.Inspect(file, func(n ast.Node) bool {
			if isTemplateScopeLit(n) && declaresNarrowing(n) {
				totalNarrowing++
			}
			return true
		})
	}
	for name := range declaredScope {
		if _, ok := templateScopes[name]; !ok {
			t.Errorf("%s is a package-level template scope but is not registered in templateScopes, so no guard holds it to its context type", name)
		}
	}
	for _, name := range sortedScopeNames() {
		if !declaredScope[name] {
			t.Errorf("templateScopes registers %q, but no package-level variable of that name holds a scope literal — the registry has drifted from the source", name)
		}
	}
	inlineCount := totalNarrowing - len(declaredNarrowing)
	if inlineCount != 0 {
		t.Errorf("%d template-scope narrowing(s) are declared somewhere other than a package-level variable; a narrowing no variable holds cannot be registered and cannot be guarded", inlineCount)
	}
}

// isTemplateScopeLit reports whether n is a templateScope composite literal.
func isTemplateScopeLit(n ast.Node) bool {
	lit, ok := n.(*ast.CompositeLit)
	if !ok {
		return false
	}
	ident, ok := lit.Type.(*ast.Ident)
	return ok && ident.Name == "templateScope"
}

// declaresNarrowing reports whether a scope literal states an exclusion or a
// depth-2 narrowing. A literal carrying only data narrows nothing and has nothing
// beyond its context type for validate to check.
func declaresNarrowing(n ast.Node) bool {
	lit, ok := n.(*ast.CompositeLit)
	if !ok {
		return false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if key.Name == "exclude" || key.Name == "subGate" {
			return true
		}
	}
	return false
}

// The two escape hatches this mechanism closed existed because nothing stopped a
// surface from constructing its own template and executing it outside the
// evaluator, with its own reference rules or none. The guard is over the IMPORT
// rather than over any construction call, because a constructor name is one
// spelling among several — Must, a Clone of an existing template, an aliased
// import — and a filter written to catch the spellings already in the tree cannot
// represent the next one. Nothing in this package can construct or execute a
// template without importing text/template, so the import is the property with no
// variant form.
//
// text/template/parse is deliberately NOT covered: a parse tree cannot be executed
// on its own, and the reference walk is defined over trees.
func TestNoTemplateConstructionOutsideTheEvaluator(t *testing.T) {
	t.Parallel()
	// The evaluator itself, the shared parse helper, and the one file that declares
	// the function set. Every other file in the package reaches templates through
	// them.
	allowed := map[string]string{
		"template.go":       "declares the one evaluator every template surface runs through",
		"template_roots.go": "declares the shared parse helper the pre-checks and the field-access census use",
		"funcmap.go":        "declares the function set, whose type is text/template's own",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var offenders []string
	var covered []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		covered = append(covered, name)
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil || path != "text/template" {
				continue
			}
			if _, ok := allowed[name]; ok {
				continue
			}
			offenders = append(offenders, name)
		}
	}
	slices.Sort(offenders)
	if len(offenders) > 0 {
		t.Errorf("these files import text/template and so can construct a template outside the one evaluator: %s.\n"+
			"Route the surface through templateEval instead; if it genuinely cannot, add it to this guard's allowed set with the reason.",
			strings.Join(offenders, ", "))
	}
	if len(covered) < 2 {
		t.Fatalf("the guard walked %d files, so it is not reading the package it claims to guard", len(covered))
	}
	t.Logf("guard covered %d non-test files; %d permitted importers: %v", len(covered), len(allowed), sortedKeys(allowed))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sortedScopeNames() []string {
	names := make([]string, 0, len(templateScopes))
	for name := range templateScopes {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
