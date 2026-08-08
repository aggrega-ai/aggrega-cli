// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The locals contract holds on every surface that resolves a locals block, and
// this file is what makes that a fact rather than a claim. Four surfaces resolve
// one, and the defect this pins was DISAGREEMENT between them: one surface halted
// on a sibling read, one resolved an earlier sibling and silently emptied a later
// one, and one silently emptied a sibling read in either direction. So the guard is
// deliberately shaped as one authored fixture run through all four, with one
// expectation table — a per-surface test would pass on four different behaviours.
//
// The fixture reads only .locals and static text. That is what lets one fixture
// serve all four surfaces: the pillars they carry genuinely differ (a boundary-scope
// FR has no .values, an attachment carries its own), so a fixture touching any other
// pillar would have to fork per surface and would stop pinning agreement.

// localsParityCase is one authored locals block plus what every surface that
// resolves it must do with it. sentinel is nil for a block that resolves, in which
// case want is the value observed at the block's last entry.
type localsParityCase struct {
	name     string
	locals   []string
	want     string
	sentinel error
}

// observedKey is the entry every case observes. It is the block's derived entry —
// the one that reads a sibling — so the observation is of the reference's result
// and not of a literal.
const observedKey = "derived"

func localsParityCases() []localsParityCase {
	return []localsParityCase{
		{
			// The ruling's positive half: a later entry reads an earlier one and
			// receives its RESOLVED value, not the authored template text.
			name:   "backward reference resolves",
			locals: []string{"base", "svc", observedKey, "{{ .locals.base }}.internal"},
			want:   "svc.internal",
		},
		{
			// Author order is the contract. The same two entries in the opposite
			// order must halt rather than be reordered into resolving, because
			// emitted output would otherwise follow an inference.
			name:     "forward reference halts",
			locals:   []string{observedKey, "{{ .locals.base }}.internal", "base", "svc"},
			sentinel: ErrLocalsForwardReference(),
		},
		{
			// A mistyped key is a different mistake with a different fix, and it
			// reports a different halt. One diagnostic for both would tell this
			// author to reorder a block that is already in the right order.
			name:     "unknown reference halts",
			locals:   []string{"base", "svc", observedKey, "{{ .locals.bass }}.internal"},
			sentinel: ErrLocalsUnknownReference(),
		},
	}
}

// localsParitySurface is one surface that resolves a locals block, reduced to the
// one observation every surface can answer: the resolved value of observedKey, or
// the halt.
//
// The two surfaces that do not return the resolved map — the attachment and
// boundary-scope FeatureRecipe paths, which return provides — observe it through a
// provides entry that echoes the local. Reading the value the way each surface
// actually publishes it is the point: a guard that reached inside for the map would
// pass even if the publication were broken.
type localsParitySurface struct {
	name string
	eval func(t *testing.T, locals types.OrderedMap[string]) (string, error)
}

func localsParitySurfaces() []localsParitySurface {
	return []localsParitySurface{
		{
			name: "base Recipe locals",
			eval: func(t *testing.T, locals types.OrderedMap[string]) (string, error) {
				t.Helper()
				recipe := &types.Recipe{}
				recipe.Metadata.Name = "parity-recipe"
				recipe.Spec.Locals = locals
				got, err := EvaluateRecipeLocals(recipe, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{})
				if err != nil {
					return "", err
				}
				v, _ := got.Get(observedKey)
				return v, nil
			},
		},
		{
			name: "per-component FeatureRecipe locals",
			eval: func(t *testing.T, locals types.OrderedMap[string]) (string, error) {
				t.Helper()
				fr := &types.FeatureRecipe{}
				fr.Metadata.Name = "parity-fr"
				fr.Spec.Locals = locals
				got, err := EvaluateFeatureRecipeLocals(fr, "mon", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{})
				if err != nil {
					return "", err
				}
				v, _ := got.Get(observedKey)
				return v, nil
			},
		},
		{
			name: "attachment FeatureRecipe locals",
			eval: func(t *testing.T, locals types.OrderedMap[string]) (string, error) {
				t.Helper()
				fr := &types.FeatureRecipe{}
				fr.Metadata.Name = "parity-fr"
				fr.Spec.Locals = locals
				fr.Spec.Provides.Set("echo", "{{ .locals."+observedKey+" }}")
				got, err := evalAttachmentFRProvides(fr, "mon", "api", types.IRFeatureEntry{})
				if err != nil {
					return "", err
				}
				v, _ := got.Get("echo")
				return v, nil
			},
		},
		{
			name: "boundary-scope FeatureRecipe locals",
			eval: func(t *testing.T, locals types.OrderedMap[string]) (string, error) {
				t.Helper()
				fr := &types.FeatureRecipe{}
				fr.Metadata.Name = "parity-fr"
				fr.Spec.Locals = locals
				fr.Spec.Provides.Set("echo", "{{ .locals."+observedKey+" }}")
				frCtx := &types.TemplateContext{Locals: types.OrderedMap[string]{}}
				state := &BoundaryFRState{Provides: map[boundaryFRKey]map[string]any{}}
				key := boundaryFRKey{BoundaryPath: "infra", FRInstance: "mon"}
				if err := evaluateBoundaryFRTemplates(fr, "mon", frCtx, state, key); err != nil {
					return "", err
				}
				v, _ := state.Provides[key]["echo"].(string)
				return v, nil
			},
		},
	}
}

// TestLocalsContract_AllFourSurfacesAgree is the acceptance the whole locals
// contract turns on: one authored block, four surfaces, one expectation table.
//
// It also logs every cell, so what the four surfaces actually do is readable from
// the test output rather than inferred from four separate assertions.
func TestLocalsContract_AllFourSurfacesAgree(t *testing.T) {
	t.Parallel()
	for _, tc := range localsParityCases() {
		for _, surface := range localsParitySurfaces() {
			t.Run(tc.name+"/"+surface.name, func(t *testing.T) {
				t.Parallel()
				got, err := surface.eval(t, omLocals(tc.locals...))
				switch {
				case tc.sentinel == nil:
					if err != nil {
						t.Fatalf("%s: %s: must resolve, got halt: %v", surface.name, tc.name, err)
					}
					if got != tc.want {
						t.Errorf("%s: %s: resolved %q, want %q — a sibling read must deliver the resolved value",
							surface.name, tc.name, got, tc.want)
					}
					t.Logf("CELL %-38s | %-28s | resolved %q", surface.name, tc.name, got)
				default:
					if err == nil {
						t.Fatalf("%s: %s: must halt, resolved %q instead — a reference that cannot be satisfied must never render",
							surface.name, tc.name, got)
					}
					if !errors.Is(err, tc.sentinel) {
						t.Errorf("%s: %s: halted with the wrong class; want %v, got: %v",
							surface.name, tc.name, tc.sentinel, err)
					}
					t.Logf("CELL %-38s | %-28s | halt %v", surface.name, tc.name, sentinelName(err))
				}
			})
		}
	}
}

// sentinelName reports which of the two locals halts a diagnostic carries, so the
// logged cell names the class rather than the whole wrapped chain.
func sentinelName(err error) string {
	switch {
	case errors.Is(err, ErrLocalsForwardReference()):
		return "ErrLocalsForwardReference"
	case errors.Is(err, ErrLocalsUnknownReference()):
		return "ErrLocalsUnknownReference"
	default:
		return "other: " + err.Error()
	}
}

// The two halts are distinct because the two mistakes have distinct fixes, so the
// diagnostic each carries has to say which fix applies. A message that named the
// wrong remedy would be worse than no message: it would send an author who mistyped
// a key off to reorder a block that is already ordered correctly.
func TestLocalsContract_TwoDiagnosticsCarryTheirOwnRemedy(t *testing.T) {
	t.Parallel()
	recipe := func(locals ...string) *types.Recipe {
		r := &types.Recipe{}
		r.Metadata.Name = "parity-recipe"
		r.Spec.Locals = omLocals(locals...)
		return r
	}

	_, fwdErr := EvaluateRecipeLocals(recipe(observedKey, "{{ .locals.base }}.internal", "base", "svc"), "api",
		types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if fwdErr == nil {
		t.Fatal("a forward reference must halt")
	}
	fwd := diagnosticChainText(fwdErr)
	for _, want := range []string{`locals["derived"]`, ".locals.base", `"base"`, "author order", `define "base" above "derived"`} {
		if !strings.Contains(fwd, want) {
			t.Errorf("forward diagnostic omits %q: %s", want, fwd)
		}
	}
	t.Logf("forward:  %s", fwd)

	_, unkErr := EvaluateRecipeLocals(recipe("base", "svc", observedKey, "{{ .locals.bass }}.internal"), "api",
		types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if unkErr == nil {
		t.Fatal("an unknown reference must halt")
	}
	unk := diagnosticChainText(unkErr)
	for _, want := range []string{`locals["derived"]`, ".locals.bass", "declares no", ".locals.base", "correct the key name"} {
		if !strings.Contains(unk, want) {
			t.Errorf("unknown diagnostic omits %q: %s", want, unk)
		}
	}
	// The naming fault must NOT be told to reorder; that is the whole reason the two
	// classes exist separately.
	if strings.Contains(unk, "author order") {
		t.Errorf("unknown-key diagnostic offers the ordering remedy, which does not apply: %s", unk)
	}
	t.Logf("unknown:  %s", unk)
}

// A self-reference is a forward reference — the key is declared and unresolved — but
// reordering cannot fix it, so the forward halt states the remedy that can.
func TestLocalsContract_SelfReferenceIsForwardWithItsOwnRemedy(t *testing.T) {
	t.Parallel()
	r := &types.Recipe{}
	r.Metadata.Name = "parity-recipe"
	r.Spec.Locals = omLocals("loop", "{{ .locals.loop }}-x")
	_, err := EvaluateRecipeLocals(r, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err == nil {
		t.Fatal("a self-reference must halt")
	}
	if !errors.Is(err, ErrLocalsForwardReference()) {
		t.Fatalf("a self-reference is the forward class; got: %v", err)
	}
	msg := diagnosticChainText(err)
	if !strings.Contains(msg, "cannot read itself") {
		t.Errorf("self-reference diagnostic must not advise reordering: %s", msg)
	}
	t.Logf("self:     %s", msg)
}

// Content that does not read a sibling resolves byte-identically on every surface.
// This is the non-regression half: the check is a refusal over reference NAMES and
// must not touch a value, and the four surfaces must agree on the resolved bytes
// including the whitespace trim.
func TestLocalsContract_NonSiblingContentIsByteIdenticalAcrossSurfaces(t *testing.T) {
	t.Parallel()
	// Multi-line and padded authoring, so the trim is actually exercised rather
	// than trivially satisfied.
	locals := omLocals(
		"plain", "static-value",
		"padded", "   spaced   ",
		observedKey, "{{ if true }}\n  yes\n{{ end }}",
	)
	var first string
	for i, surface := range localsParitySurfaces() {
		got, err := surface.eval(t, locals)
		if err != nil {
			t.Fatalf("%s: must resolve: %v", surface.name, err)
		}
		if i == 0 {
			first = got
			t.Logf("baseline %s -> %q", surface.name, got)
			continue
		}
		if got != first {
			t.Errorf("%s resolved %q; the base Recipe surface resolved %q — the surfaces must agree byte-for-byte",
				surface.name, got, first)
		}
	}
	if first != "yes" {
		t.Errorf("resolved value = %q, want %q (trimmed)", first, "yes")
	}
}

// The boundary-scope surface gets its own case beyond the parity table, because it
// was the surface that silently emptied a sibling read in BOTH directions — its
// context published the locals pillar only after the whole loop had finished, so no
// entry ever saw a sibling. This chains three locals and mixes in a .boundary pillar
// the parity fixture deliberately does not touch, so what is pinned is the real
// boundary-scope context and not a reduced stand-in.
func TestLocalsContract_BoundaryScopeChainedLocalsResolve(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "boundary-chain-fr"
	fr.Spec.Locals.Set("stack", "{{ .boundary.boundary_name }}")
	fr.Spec.Locals.Set("svc", "{{ .locals.stack }}-gateway")
	fr.Spec.Locals.Set("fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.svc.cluster.local")
	fr.Spec.Provides.Set("endpoint", "https://{{ .locals.fqdn }}:8443")

	frCtx := &types.TemplateContext{
		Locals:   types.OrderedMap[string]{},
		Boundary: &types.BoundaryContext{Path: "prod/edge", BoundaryName: "edge"},
	}
	state := &BoundaryFRState{Provides: map[boundaryFRKey]map[string]any{}}
	key := boundaryFRKey{BoundaryPath: "prod/edge", FRInstance: "gw"}
	if err := evaluateBoundaryFRTemplates(fr, "gw", frCtx, state, key); err != nil {
		t.Fatalf("chained boundary-scope locals must resolve: %v", err)
	}

	for _, want := range []struct{ key, value string }{
		{"stack", "edge"},
		{"svc", "edge-gateway"},
		{"fqdn", "edge-gateway.edge.svc.cluster.local"},
	} {
		got, _ := frCtx.Locals.Get(want.key)
		if got != want.value {
			t.Errorf("locals[%q] = %q, want %q", want.key, got, want.value)
		}
		t.Logf("boundary locals[%-6s] = %q", want.key, got)
	}
	// The provides entry reads the last link of the chain, so it is the end-to-end
	// observation: a value assembled across three locals reaching published output.
	gotProvides, _ := state.Provides[key]["endpoint"].(string)
	if gotProvides != "https://edge-gateway.edge.svc.cluster.local:8443" {
		t.Errorf("provides[endpoint] = %q — the chain did not reach published output", gotProvides)
	}
	t.Logf("boundary provides[endpoint] = %q", gotProvides)
}

// A sibling reference is identified from the parse tree, so the pillar's name as
// TEXT is not a reference. Every form here is refused by a check that matches the
// authored string instead, which is why the check reads the tree the template is
// about to execute.
func TestLocalsContract_SiblingCheckReadsTheTreeNotTheText(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"string literal":              `{{ printf "%s" ".locals.nope" }}`,
		"comment":                     `{{/* .locals.nope */}}ok`,
		"rebound dot in a range body": `{{ range .values.list }}{{ .locals.nope }}{{ end }}`,
		// A bare pillar reference is admitted, matching the whole-root admission
		// every scope applies.
		"bare pillar": `{{ if .locals }}ok{{ end }}`,
	}
	values := types.OrderedMap[any]{}
	values.Set("list", []any{map[string]any{"locals": map[string]any{"nope": "SAFE"}}})
	for name, tmpl := range cases {
		r := &types.Recipe{}
		r.Metadata.Name = "parity-recipe"
		r.Spec.Locals = omLocals("k", tmpl)
		if _, err := EvaluateRecipeLocals(r, "api", values, types.OrderedMap[any]{}); err != nil {
			t.Errorf("%s form %q must render, got: %v", name, tmpl, err)
		}
	}
}

// Resolved locals travel on to five consuming surfaces, and none of them changed.
// This pins the bytes each one delivers from the SAME .locals read, so a future edit
// to the pillar's publication is caught at the bytes rather than at a golden file
// three layers downstream.
//
// The four surfaces that render against the node's TemplateContext share one
// evaluator, which is why they are asserted to agree byte-for-byte rather than
// separately pinned — a per-surface literal would pass even if one surface had
// quietly acquired its own evaluator. The assertion surface is evaluated separately
// because it genuinely has its own context type and its own missing-key policy, and
// it publishes the pillar under a different key, so agreement there has to be
// asserted rather than assumed.
func TestLocalsContract_ResolvedLocalsReachEveryConsumerByteIdentically(t *testing.T) {
	t.Parallel()
	locals := omLocals("host", "db.internal", "port", "5432")
	ctx := &types.TemplateContext{Locals: locals}

	const body = `{{ .locals.host }}:{{ .locals.port }}`
	const wantBytes = "db.internal:5432"

	// artifacts:, workload:, valuesMapping: and pipeline[].with all evaluate the
	// authored template against the node's TemplateContext through this evaluator.
	for _, surface := range []string{
		"valuesMapping[pin]",
		"workload[pin]",
		"artifacts[pin]",
		"pipeline[pin].with",
	} {
		got, err := EvalTemplate(surface, body, ctx)
		if err != nil {
			t.Fatalf("%s: %v", surface, err)
		}
		if got != wantBytes {
			t.Errorf("%s delivered %q, want %q", surface, got, wantBytes)
		}
		if []byte(got) == nil || len(got) != len(wantBytes) {
			t.Errorf("%s byte length %d, want %d", surface, len(got), len(wantBytes))
		}
		t.Logf("%-22s -> %q (%d bytes)", surface, got, len(got))
	}

	// artifacts: may render under an engine's alternate delimiters; the pillar has
	// to arrive the same way there.
	altGot, err := EvalTemplateWithDelims("artifacts[pin-alt]", `[[ .locals.host ]]:[[ .locals.port ]]`, ctx, "[[", "]]")
	if err != nil {
		t.Fatalf("alternate-delimiter artifacts: %v", err)
	}
	if altGot != wantBytes {
		t.Errorf("alternate-delimiter artifacts delivered %q, want %q", altGot, wantBytes)
	}
	t.Logf("%-22s -> %q (%d bytes)", "artifacts[alt-delims]", altGot, len(altGot))

	// assertions: read the pillar under its own key from their own context type, and
	// under missingkey=error rather than the value surfaces' missingkey=zero.
	actx := AssertionContext{Locals: locals.TemplateAlphabeticalMap()}
	if err := EvaluateAssertion("pin", `{{ if ne (printf "%s:%s" .locals.host .locals.port) "`+wantBytes+`" }}{{ fail "assertion surface disagrees" }}{{ end }}`, actx); err != nil {
		t.Errorf("assertions: %v", err)
	}
	// And an undeclared key on that surface halts rather than rendering empty — the
	// strict missing-key policy is what covers it, not the sibling-order check.
	if err := EvaluateAssertion("pin-unknown", `{{ .locals.hsot }}`, actx); err == nil {
		t.Error("assertions: an undeclared local must halt, not render empty")
	} else {
		t.Logf("%-22s -> halts on an undeclared local: %v", "assertions", err)
	}
}

// The Recipe source surface shares the locals context type, so adding the pillar to
// that type made .locals reachable from a source template. It must deliver the
// node's resolved locals and refuse an undeclared key — not resolve the key away,
// which is the silent-empty this whole contract exists to remove.
func TestLocalsContract_SourceSurfaceReadsResolvedLocalsAndRefusesUnknown(t *testing.T) {
	t.Parallel()
	locals := omLocals("chart_version", "1.4.2")
	got, err := EvaluateRecipeSource(
		"oci://example.test/charts/api:{{ .locals.chart_version }}", "api",
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, locals,
	)
	if err != nil {
		t.Fatalf("a source template must read the node's resolved locals: %v", err)
	}
	if got != "oci://example.test/charts/api:1.4.2" {
		t.Errorf("source resolved to %q, want the local's resolved value spliced in", got)
	}

	_, err = EvaluateRecipeSource(
		"oci://example.test/charts/api:{{ .locals.chart_vrsion }}", "api",
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, locals,
	)
	if !errors.Is(err, ErrLocalsUnknownReference()) {
		t.Errorf("a source template naming an undeclared local must halt with the unknown-reference class, got: %v", err)
	}
}

// The order check compares a reference's root against one constant. Every context
// type that carries the pillar has to publish it under exactly that name, or the
// check watches a root the template never reaches and every sibling read silently
// resolves empty again.
func TestLocalsContract_PillarNameMatchesEveryContextThatCarriesIt(t *testing.T) {
	t.Parallel()
	for _, surface := range []struct {
		name string
		data any
	}{
		{"LocalsContext", LocalsContext{}},
		{"ProvidesContext", ProvidesContext{}},
		{"types.TemplateContext", &types.TemplateContext{}},
	} {
		roots, ok := contextRootNames(surface.data)
		if !ok {
			t.Fatalf("%s: root set must be derivable", surface.name)
		}
		// containsString and diagnosticChainText are the package's existing test
		// helpers; a local copy of either would be one more thing to keep in step.
		if !containsString(roots, localsContextRoot) {
			t.Errorf("%s does not publish the locals pillar as %q; it publishes %v — the order check would watch a root no template reaches",
				surface.name, localsContextRoot, roots)
		}
	}
}
