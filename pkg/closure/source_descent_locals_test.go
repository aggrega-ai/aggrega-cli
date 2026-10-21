// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// helmSource returns a structured helm-oci-chart source with the given version
// expression (templated or literal).
func helmSource(version string) map[string]any {
	return map[string]any{"kind": "helm-oci-chart", "url": "oci://r/c", "version": version}
}

// TestDescendSkipsRenderTimeOnlyLocal is the payoff: a render-time-only local
// (a deterministic name keyed by `.context.instance_id`, consumed by
// valuesMapping — not by `source:`) does NOT abort lock-time source-descent. The
// source resolves from its own fields; the unresolvable local is skipped.
func TestDescendSkipsRenderTimeOnlyLocal(t *testing.T) {
	t.Parallel()
	uri := "mariadb-bitnami:1.0.0"
	r := makeRecipe(uri, helmSource("1.0.0"))
	r.Spec.Locals.Set("secret_target_root_password", `{{ .context.instance_id }}-root-password`)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("render-time-only local must not abort source-descent; got %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("artifacts = %d; want 1 (source resolved despite the .context local)", len(got))
	}
}

// TestDescendFailsWhenSourceNeedsAnUnresolvableLocal: if the `source:` template
// itself references a local that could not resolve at lock time, source eval is
// the gate — it fails, naming the source field.
func TestDescendFailsWhenSourceNeedsAnUnresolvableLocal(t *testing.T) {
	t.Parallel()
	uri := "x:1.0.0"
	r := makeRecipe(uri, helmSource(`{{ .locals.v }}`))
	r.Spec.Locals.Set("v", `{{ .context.instance_id }}`) // skipped at lock time
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime()) {
		t.Fatalf("source referencing a skipped local should halt with ErrSourceTemplateAtLockTime; got %v", err)
	}
	if !contains(err.Error(), "spec.source.version") {
		t.Errorf("error should name the source field; got %v", err)
	}
	// The field's own failure names only the key it found missing; the entry's
	// own cause is what says why, so it is quoted beside it.
	if !contains(err.Error(), "it reads x:1.0.0.spec.locals.v, which could not be evaluated here") ||
		!contains(err.Error(), `"context"`) {
		t.Errorf("error should quote why the entry the field reads holds no value; got %v", err)
	}
}

// descendOne runs source descent over one Recipe used by one instance carrying
// infra, through the exported entry a pull reaches it by.
func descendOne(t *testing.T, r *types.Recipe, infra map[string]any) ([]closure.SourceArtifact, error) {
	t.Helper()
	uri := r.Metadata.Name
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}
	used := map[string][]closure.ComponentSourceInfra{uri: {{Component: "web", Infra: infra}}}
	return closure.DescendRecipeSources(used, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
}

// TestDescendKeepsParseErrorLoud: a malformed local template the source reads is
// invalid at any phase and stays loud at lock time (not skipped), naming the entry
// and the source field it was evaluated for.
func TestDescendKeepsParseErrorLoud(t *testing.T) {
	t.Parallel()
	uri := "x:1.0.0"
	r := makeRecipe(uri, helmSource(`{{ .locals.bad }}`))
	r.Spec.Locals.Set("bad", `{{ .infra.x `) // unclosed action -> parse error
	_, err := descendOne(t, r, map[string]any{})
	if !errors.Is(err, closure.ErrSourceTemplateParse()) {
		t.Fatalf("malformed local should be ErrSourceTemplateParse (loud); got %v", err)
	}
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime()) {
		t.Errorf("parse error should still wrap ErrSourceTemplateAtLockTime; got %v", err)
	}
	for _, want := range []string{"x:1.0.0.spec.locals.bad", "read by x:1.0.0.spec.source.version"} {
		if !contains(err.Error(), want) {
			t.Errorf("the diagnostic omits %q; got %v", want, err)
		}
	}
}

// TestDescendLeavesAnUnreadMalformedLocalToTheRender: an entry no source field
// reads is not parsed here, so a malformed one does not stop the coordinate from
// resolving. The render's own pass is where it is refused.
func TestDescendLeavesAnUnreadMalformedLocalToTheRender(t *testing.T) {
	t.Parallel()
	r := makeRecipe("x:1.0.0", helmSource("1.0.0"))
	r.Spec.Locals.Set("bad", `{{ .infra.x `)
	got, err := descendOne(t, r, map[string]any{})
	if err != nil {
		t.Fatalf("an unread malformed local must not halt source descent; got %v", err)
	}
	if len(got) != 1 || got[0].PinnedVersion != "1.0.0" {
		t.Fatalf("artifacts = %+v; want the literal version pinned", got)
	}
}

// TestDescendKeepsProvidesLocalLoud: a local the source reads referencing
// `.provides` is an invalid reverse reference (locals feed provides, not the
// reverse) and stays loud.
func TestDescendKeepsProvidesLocalLoud(t *testing.T) {
	t.Parallel()
	r := makeRecipe("x:1.0.0", helmSource(`{{ .locals.p }}`))
	r.Spec.Locals.Set("p", `{{ .provides.X }}`)
	_, err := descendOne(t, r, map[string]any{})
	if !errors.Is(err, closure.ErrSourceTemplateProvidesRef()) {
		t.Fatalf(".provides local should be ErrSourceTemplateProvidesRef (loud); got %v", err)
	}
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime()) {
		t.Errorf(".provides reverse-ref should still wrap ErrSourceTemplateAtLockTime; got %v", err)
	}
	if !contains(err.Error(), "x:1.0.0.spec.locals.p, read by x:1.0.0.spec.source.version") {
		t.Errorf("the diagnostic should name the entry and the field reading it; got %v", err)
	}
}

// TestDescendLeavesAnUnreadProvidesLocalToTheRender: the same reverse reference in
// an entry no source field reads cannot reach a coordinate, so it is not looked
// at here; the render refuses it.
func TestDescendLeavesAnUnreadProvidesLocalToTheRender(t *testing.T) {
	t.Parallel()
	r := makeRecipe("x:1.0.0", helmSource("1.0.0"))
	r.Spec.Locals.Set("p", `{{ .provides.X }}`)
	if _, err := descendOne(t, r, map[string]any{}); err != nil {
		t.Fatalf("an unread .provides local must not halt source descent; got %v", err)
	}
}

// TestDescendSkipSetIsDeterministic: re-running with a skipped render-time-only
// local yields identical source artifacts (deterministic skip-set).
func TestDescendSkipSetIsDeterministic(t *testing.T) {
	t.Parallel()
	uri := "mariadb-bitnami:1.0.0"
	build := func() []closure.SourceArtifact {
		r := makeRecipe(uri, helmSource("1.0.0"))
		r.Spec.Locals.Set("a", `{{ .context.instance_id }}-a`)
		r.Spec.Locals.Set("b", `{{ .context.instance_id }}-b`)
		loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
		cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}
		got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return got
	}
	a, b := build(), build()
	if len(a) != 1 || len(b) != 1 || a[0].URL != b[0].URL || a[0].PinnedVersion != b[0].PinnedVersion {
		t.Errorf("non-deterministic source-descent: %+v vs %+v", a, b)
	}
}

// TestDescendRefusesAForwardSiblingReference: an entry the source reads, reading a
// sibling the block declares BELOW it, is an author-order fault decidable from the
// block alone, and is refused rather than skipped.
//
// The condition is matched through the render package's own accessor. That is the
// assertion that one authoring fault carries one condition wherever it is
// reported: a second condition minted for this surface would leave this match
// failing while the halt still occurred.
func TestDescendRefusesAForwardSiblingReference(t *testing.T) {
	t.Parallel()
	uri := "x:1.0.0"
	r := makeRecipe(uri, helmSource(`{{ .locals.derived }}`))
	r.Spec.Locals.Set("derived", `{{ .locals.base }}.internal`)
	r.Spec.Locals.Set("base", "svc")
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, renderer.ErrLocalsForwardReference()) {
		t.Fatalf("a forward sibling reference should halt with the ordering condition; got %v", err)
	}
	if errors.Is(err, closure.ErrSourceTemplateAtLockTime()) {
		t.Errorf("the halt should not also report a coordinate-resolution failure: "+
			"the fault is in the authored block and is the same at every phase; got %v", err)
	}
	// Clause by clause, what the diagnostic owes an author: the entry at fault, the
	// reference it wrote, where the definition actually sits, and what to change.
	msg := err.Error()
	for _, want := range []string{
		"spec.locals.derived",
		`".locals.base"`,
		`defines "base" later`,
		"entries resolve in author order",
		`define "base" above "derived" in the locals block`,
		"read by x:1.0.0.spec.source.version",
	} {
		if !contains(msg, want) {
			t.Errorf("the diagnostic omits %q; got %v", want, msg)
		}
	}
}

// TestDescendRefusesAnUnknownLocalsReference: a key the block never declares is a
// NAMING fault, and it reports the naming condition with the declared set rather
// than telling the author to reorder a block that is already in order.
func TestDescendRefusesAnUnknownLocalsReference(t *testing.T) {
	t.Parallel()
	uri := "x:1.0.0"
	r := makeRecipe(uri, helmSource(`{{ .locals.derived }}`))
	r.Spec.Locals.Set("base", "svc")
	r.Spec.Locals.Set("derived", `{{ .locals.bass }}.internal`)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, renderer.ErrLocalsUnknownReference()) {
		t.Fatalf("a reference to an undeclared key should halt with the naming condition; got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{`declares no "bass"`, ".locals.base", "correct the key name"} {
		if !contains(msg, want) {
			t.Errorf("the diagnostic omits %q; got %v", want, msg)
		}
	}
	if contains(msg, "above") {
		t.Errorf("a naming fault must not tell the author to reorder the block; got %v", msg)
	}
}

// TestDescendSelfReferenceRemedyDoesNotSayReorder: a self-reference is the same
// ordering condition, and no reordering fixes it. The remedy and the position
// sentence are both functions of the occurrence for that reason, so this asserts
// the pair a registered sentence would have got wrong.
func TestDescendSelfReferenceRemedyDoesNotSayReorder(t *testing.T) {
	t.Parallel()
	uri := "x:1.0.0"
	r := makeRecipe(uri, helmSource(`{{ .locals.loop }}`))
	r.Spec.Locals.Set("loop", `{{ .locals.loop }}-x`)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, renderer.ErrLocalsForwardReference()) {
		t.Fatalf("a self reference should halt with the ordering condition; got %v", err)
	}
	msg := err.Error()
	if !contains(msg, "a locals entry cannot read itself") {
		t.Errorf("the self-reference remedy is missing; got %v", msg)
	}
	if contains(msg, `defines "loop" later`) {
		t.Errorf("the diagnostic says the key is defined LATER, which is false for a self "+
			"reference — it is defined at the referencing entry; got %v", msg)
	}
	if !contains(msg, `defines "loop" at this entry`) {
		t.Errorf("the diagnostic should say where the key actually sits; got %v", msg)
	}
}

// TestDescendAdmitsAReadOfASkippedEarlierSibling is the narrowing's own regression
// guard, and it guards the mistake the narrowing most easily makes.
//
// The second entry reads the first, which is legitimately skipped here. That is a
// BACKWARD reference and the block is correctly ordered, so it must stay admitted:
// classifying against the keys that actually RESOLVED — rather than against the
// declared order's prefix — would report an ordering fault and refuse a document
// the render accepts.
//
// The source reads the second entry, so both are classified here, and reads it on
// a branch it does not take, so the coordinate itself resolves.
func TestDescendAdmitsAReadOfASkippedEarlierSibling(t *testing.T) {
	t.Parallel()
	uri := "mariadb-bitnami:1.0.0"
	r := makeRecipe(uri, helmSource(`{{ if optional .values "never" false }}{{ .locals.derived_secret_name }}{{ else }}1.0.0{{ end }}`))
	r.Spec.Locals.Set("secret_target_root_password", `{{ .context.instance_id }}-root-password`)
	r.Spec.Locals.Set("derived_secret_name", `{{ .locals.secret_target_root_password }}-v2`)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("a backward read of a skipped render-time-only sibling must stay admitted; got %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("artifacts = %d; want 1 (the source resolved despite both skipped entries)", len(got))
	}
}
