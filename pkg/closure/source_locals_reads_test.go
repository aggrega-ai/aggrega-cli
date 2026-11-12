// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The source-coordinate pass evaluates the locals entries a `source:` field reads,
// and those reached from them, and no others. These tests pin the three answers
// that contract gives, each driven through the entry a pull reaches the pass by:
// an entry nothing reads is left alone however it is written; an entry a field
// reads is evaluated with the surface's own function set and halts naming the
// field, the entry and the cause; and a field reading the block in a way no static
// reading resolves gets every entry evaluated.

// TestSourceLocalsLeavesAnUnreadRenderOnlyLocalAlone: the version reads infra
// alone, and the block carries an entry computing a port with functions only a
// render registers, beside one reading a value only a render has. Neither is read
// by the source, so the coordinate resolves exactly as it would without them.
func TestSourceLocalsLeavesAnUnreadRenderOnlyLocalAlone(t *testing.T) {
	t.Parallel()
	r := makeRecipe("probe:1.0.0", helmSource("{{ optional .infra `source_version` \"0.1.0\" }}"))
	r.Spec.Locals.Set("gateway_address", "127.0.0.1")
	r.Spec.Locals.Set("gateway_port", "{{ add1 (int .infra.port) }}")
	r.Spec.Locals.Set("config_map_name", "{{ .context.instance_id }}-config")

	got, err := descendOne(t, r, map[string]any{"port": 8080})
	if err != nil {
		t.Fatalf("locals no source field reads must not halt source descent; got %v", err)
	}
	if len(got) != 1 || got[0].PinnedVersion != "0.1.0" {
		t.Fatalf("artifacts = %+v; want the default version pinned", got)
	}

	// The same entries, now with the version set from infra, still pin what infra
	// names: the narrowing changes which entries are evaluated, never a coordinate.
	got, err = descendOne(t, r, map[string]any{"port": 8080, "source_version": "0.2.0"})
	if err != nil || len(got) != 1 || got[0].PinnedVersion != "0.2.0" {
		t.Fatalf("with infra.source_version set: artifacts %+v, err %v; want 0.2.0 pinned", got, err)
	}
}

// TestSourceLocalsHaltsOnARenderOnlyFunctionTheSourceReads: the version reads an
// entry that reads another calling a function only a render registers. The entry
// is reached, so it is evaluated, and it does not parse here.
//
// The halt is ONE failure naming the field, the entry and the function, headed by
// the parse condition's own short code. Each of those is asserted, and so is the
// shape: a failure whose chain forks into a join is read by every refusal surface
// as several findings, each printed by its bare sentence, which is exactly how the
// field and the cause stop reaching the reader.
func TestSourceLocalsHaltsOnARenderOnlyFunctionTheSourceReads(t *testing.T) {
	t.Parallel()
	r := makeRecipe("probe:1.0.0", helmSource("{{ .locals.chart_version }}"))
	r.Spec.Locals.Set("chart_minor", "{{ add1 (int .infra.port) }}")
	r.Spec.Locals.Set("chart_version", "0.{{ .locals.chart_minor }}.0")

	_, err := descendOne(t, r, map[string]any{"port": 8080})
	if !errors.Is(err, closure.ErrSourceTemplateParse()) {
		t.Fatalf("want the parse condition; got %v", err)
	}
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime()) {
		t.Errorf("the parse condition is one case of the lock-time condition and must still match it; got %v", err)
	}
	for _, want := range []string{
		"probe:1.0.0.spec.locals.chart_minor",
		"read by probe:1.0.0.spec.source.version",
		`function "add1" not defined`,
		"Aggrega's own helpers only",
	} {
		if !contains(err.Error(), want) {
			t.Errorf("the diagnostic omits %q; got %v", want, err)
		}
	}
	if code, _ := cerrors.ShortCodeOf(err); code != "E0111" {
		t.Errorf("the head should carry the parse condition's short code E0111; got %q", code)
	}
	assertOneChain(t, err)
}

// TestSourceLocalsEvaluatesEveryEntryWhereTheSourceReadsTheBlockWhole: the version
// reads the block through index, which names no entry a static reading can see, so
// every entry is evaluated and the planted entry — read by nothing — halts.
//
// The control beside it reads the same key by name, and resolves: what makes the
// difference is the dynamic read, not the planted entry.
func TestSourceLocalsEvaluatesEveryEntryWhereTheSourceReadsTheBlockWhole(t *testing.T) {
	t.Parallel()
	build := func(version string) *types.Recipe {
		r := makeRecipe("probe:1.0.0", helmSource(version))
		r.Spec.Locals.Set("chart_version", "0.1.0")
		r.Spec.Locals.Set("planted", "{{ add1 (int .infra.port) }}")
		return r
	}

	_, err := descendOne(t, build(`{{ index .locals "chart_version" }}`), map[string]any{"port": 8080})
	if !errors.Is(err, closure.ErrSourceTemplateParse()) {
		t.Fatalf("a dynamic read of the block should evaluate every entry and halt on the planted one; got %v", err)
	}
	for _, want := range []string{
		"probe:1.0.0.spec.locals.planted",
		"evaluated for probe:1.0.0.spec.source.version, which reads the locals block as a whole",
	} {
		if !contains(err.Error(), want) {
			t.Errorf("the diagnostic omits %q; got %v", want, err)
		}
	}

	got, err := descendOne(t, build("{{ .locals.chart_version }}"), map[string]any{"port": 8080})
	if err != nil || len(got) != 1 || got[0].PinnedVersion != "0.1.0" {
		t.Fatalf("control, reading the key by name: artifacts %+v, err %v; want 0.1.0 pinned", got, err)
	}
}

// TestSourceLocalsReadsEachFieldNotOnlyTheVersion: every templated identity field
// is a reader, so an entry the chart name reads is evaluated as one the version
// reads would be.
func TestSourceLocalsReadsEachFieldNotOnlyTheVersion(t *testing.T) {
	t.Parallel()
	source := map[string]any{
		"kind":    "helm-https-chart",
		"url":     "https://charts.example.test",
		"chart":   "{{ .locals.chart }}",
		"version": "1.0.0",
	}
	r := makeRecipe("probe:1.0.0", source)
	r.Spec.Locals.Set("chart", "{{ upper .infra.name }}")

	_, err := descendOne(t, r, map[string]any{"name": "web"})
	if !errors.Is(err, closure.ErrSourceTemplateParse()) {
		t.Fatalf("an entry the chart field reads should be evaluated; got %v", err)
	}
	if !contains(err.Error(), "read by probe:1.0.0.spec.source.chart") {
		t.Errorf("the diagnostic should name the chart field; got %v", err)
	}
}

// TestSourceLocalsNamesTheFieldWhenTheFieldItselfCallsARenderOnlyFunction: a
// field calling such a function is its own failure, named by the field alone and
// carrying the same one-chain shape.
func TestSourceLocalsNamesTheFieldWhenTheFieldItselfCallsARenderOnlyFunction(t *testing.T) {
	t.Parallel()
	r := makeRecipe("probe:1.0.0", helmSource(`{{ default "1.0.0" .infra.source_version }}`))
	_, err := descendOne(t, r, map[string]any{})
	if !errors.Is(err, closure.ErrSourceTemplateParse()) {
		t.Fatalf("want the parse condition; got %v", err)
	}
	for _, want := range []string{"parse template at probe:1.0.0.spec.source.version:", `function "default" not defined`} {
		if !contains(err.Error(), want) {
			t.Errorf("the diagnostic omits %q; got %v", want, err)
		}
	}
	if contains(err.Error(), "read by") {
		t.Errorf("a field's own failure names no reader; got %v", err)
	}
	assertOneChain(t, err)
}

// assertOneChain fails when any link of err's chain carries several errors at
// once. A refusal surface reads such a link as a join of separate findings and
// prints each by its bare sentence, dropping the link's own text.
func assertOneChain(t *testing.T, err error) {
	t.Helper()
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		if _, multi := cur.(interface{ Unwrap() []error }); multi {
			t.Errorf("the failure forks into several errors at %T, so a refusal surface would print its "+
				"wrapped conditions as separate findings without its own text: %v", cur, err)
			return
		}
	}
}
