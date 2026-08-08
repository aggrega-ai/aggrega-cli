// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"errors"
	"testing"

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
}

// TestDescendKeepsParseErrorLoud: a malformed local template is invalid at any
// phase and stays loud at lock time (not skipped).
func TestDescendKeepsParseErrorLoud(t *testing.T) {
	t.Parallel()
	uri := "x:1.0.0"
	r := makeRecipe(uri, helmSource("1.0.0"))
	r.Spec.Locals.Set("bad", `{{ .infra.x `) // unclosed action -> parse error
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateParse()) {
		t.Errorf("malformed local should be ErrSourceTemplateParse (loud); got %v", err)
	}
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime()) {
		t.Errorf("parse error should still wrap ErrSourceTemplateAtLockTime; got %v", err)
	}
}

// TestDescendKeepsProvidesLocalLoud: a local referencing `.provides` is an
// invalid reverse reference (locals feed provides, not the reverse) and stays
// loud — even when `source:` does not reference that local.
func TestDescendKeepsProvidesLocalLoud(t *testing.T) {
	t.Parallel()
	uri := "x:1.0.0"
	r := makeRecipe(uri, helmSource("1.0.0")) // source does NOT use the provides local
	r.Spec.Locals.Set("p", `{{ .provides.X }}`)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateProvidesRef()) {
		t.Errorf(".provides local should be ErrSourceTemplateProvidesRef (loud) regardless of source usage; got %v", err)
	}
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime()) {
		t.Errorf(".provides reverse-ref should still wrap ErrSourceTemplateAtLockTime; got %v", err)
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
