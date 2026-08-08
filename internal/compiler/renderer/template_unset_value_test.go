// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// unsetValueFixture carries one authored entry under each pillar a value template
// reads, so every absent-key case below has a present-key sibling to control
// against. A suite that only asserted emptiness would pass just as well against a
// harness that rendered nothing at all.
func unsetValueFixture() *types.TemplateContext {
	values := types.OrderedMap[any]{}
	values.Set("present", "vp")
	infra := types.OrderedMap[any]{}
	infra.Set("present", "ip")
	locals := types.OrderedMap[string]{}
	locals.Set("present", "lp")
	provides := types.OrderedMap[string]{}
	provides.Set("present", "pp")
	return &types.TemplateContext{
		Context:  types.ContextBlock{InstanceID: "db"},
		Values:   values,
		Infra:    infra,
		Locals:   locals,
		Provides: provides,
	}
}

// A value template reading a key no author set renders nothing. What it must not
// render is the placeholder text/template writes for a value it could not resolve:
// that text reaches the emitted artifact, and an artifact carrying it is wrong in a
// way exit 0 does not say.
func TestEvalTemplate_UnsetValueRendersEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want string
	}{
		{`{{ .values.missing }}`, ""},
		{`{{ .infra.missing }}`, ""},
		{`{{ .locals.missing }}`, ""},
		{`{{ .provides.missing }}`, ""},
		{`{{ .values.missing }}-suffix`, "-suffix"},
		{`[{{ .values.missing }}]`, "[]"},
		{`{{ printf "%s" .values.missing }}`, ""},
		{`{{ printf "%v" .values.missing }}`, ""},
		// A pillar entry the conversion drops as empty is absent from the delivered
		// map exactly as a key no author wrote is, and renders the same way.
		{`{{ .context.labels }}`, ""},
		// Positive controls: an authored entry under each pillar still renders.
		{`{{ .values.present }}`, "vp"},
		{`{{ .infra.present }}`, "ip"},
		{`{{ .locals.present }}`, "lp"},
		{`{{ .provides.present }}`, "pp"},
		{`{{ .context.instance_id }}`, "db"},
	}
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("t", tc.tmpl, unsetValueFixture())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// The optional-value vocabulary the catalog is written in keeps working: an unset
// value is as empty and as falsy as it ever was, so a default pipe supplies its
// fallback and an if guard takes its else branch. This is the property that makes
// rendering empty a correction rather than a behaviour change — a halt here would
// have taken every one of these idioms with it.
func TestEvalTemplate_UnsetValueKeepsGuardIdiomsWorking(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want string
	}{
		{`{{ .values.missing | default "fb" }}`, "fb"},
		{`{{ .values.present | default "fb" }}`, "vp"},
		{`{{ if .values.missing }}y{{ else }}n{{ end }}`, "n"},
		{`{{ if .values.present }}y{{ else }}n{{ end }}`, "y"},
		// An unset value now compares equal to the empty string, which is what it
		// means. Nothing shipped compares a pillar entry against "" today.
		{`{{ eq .values.missing "" }}`, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("t", tc.tmpl, unsetValueFixture())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// Iterating an absent reference is not a read of an unset value: range needs a
// container, and an absent reference already iterates as nothing, which is what an
// author ranging over an optional list wants. Supplying an empty value there would
// fail the execution instead — so nothing is supplied, and every one of these
// renders as it always did.
func TestEvalTemplate_IteratedReferenceIsLeftAlone(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("list", []any{"a", "b"})
	ctx := &types.TemplateContext{Context: types.ContextBlock{InstanceID: "db"}, Values: values}
	cases := []struct {
		name string
		tmpl string
		want string
	}{
		{"absent list iterates nothing", `{{ range .values.absent_list }}x{{ end }}`, ""},
		{"absent list takes the else branch", `{{ range .values.absent_list }}x{{ else }}none{{ end }}`, "none"},
		{"present list still iterates", `{{ range .values.list }}{{ . }}{{ end }}`, "ab"},
		{"a rebound field inside the body is not a reference", `{{ range .values.list }}{{ . }}-{{ end }}`, "a-b-"},
		{"with over an absent key still takes its else branch", `{{ with .values.absent }}y{{ else }}n{{ end }}`, "n"},
		// A path read as a value in one place and iterated in another keeps the
		// iterated treatment, because withholding can only ever leave today's
		// behaviour in place while supplying could break an iteration that works.
		// This is the residue of that choice, pinned so it is a decision rather
		// than a surprise.
		{"iterating a path elsewhere withholds it from the value read", `{{ range .values.absent_list }}x{{ end }}|{{ .values.absent_list }}`, "|<no value>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("t", tc.tmpl, ctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// A read whose PARENT is absent stays a failure. text/template cannot descend
// through nothing, and supplying the missing parent so the descent could proceed
// would trade that loud failure for a silently empty result — the opposite of what
// rendering an unset LEAF empty is for.
func TestEvalTemplate_AbsentParentStillFails(t *testing.T) {
	t.Parallel()
	for _, tmpl := range []string{
		`{{ .values.absent_parent.child }}`,
		`{{ .context.labels.k }}`,
	} {
		t.Run(tmpl, func(t *testing.T) {
			t.Parallel()
			if _, err := EvalTemplate("t", tmpl, unsetValueFixture()); err == nil {
				t.Fatal("a read through an absent parent must fail, not render empty")
			}
		})
	}
}

// Rendering an unset key empty must not reach a mistyped key under a closed
// pillar: that reference is refused before the template executes, and it stays
// refused.
func TestEvalTemplate_UnsetValueDoesNotAdmitClosedPillarTypo(t *testing.T) {
	t.Parallel()
	for _, tmpl := range []string{
		`{{ .context.nmae }}`,
		`{{ .nosuchpillar.k }}`,
	} {
		t.Run(tmpl, func(t *testing.T) {
			t.Parallel()
			_, err := EvalTemplate("t", tmpl, unsetValueFixture())
			if err == nil {
				t.Fatal("an undefined reference must halt")
			}
			if !errors.Is(err, ErrTemplateUnknownRoot()) {
				t.Errorf("halt must be the unknown-reference one, got: %v", err)
			}
		})
	}
}

// missingKeyHaltReported reports whether the missing-key policy is what produced
// this error. Each strict surface states the halt in its own wording and carries
// the engine's own message as the cause, so the cause is where the policy is
// visible; asserting on the outer wording would pass just as well for a halt that
// came from somewhere else entirely.
func missingKeyHaltReported(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), "map has no entry for key") {
			return true
		}
	}
	return false
}

// The surfaces whose policy is to halt on an absent key keep halting. A path
// template's output is a filesystem path and an assertion's output is a predicate;
// tolerating an unset value on either would defeat the reason each chose the
// stricter policy.
func TestUnsetValue_StrictSurfacesStillHalt(t *testing.T) {
	t.Parallel()

	t.Run("path template halts on an absent parameter", func(t *testing.T) {
		t.Parallel()
		_, err := EvalPathTemplate("t", "subDirectory", "web", `{{ .parameters.missing }}`, pathTemplateFixture())
		if err == nil {
			t.Fatal("a path template reading an absent key must halt")
		}
		if !missingKeyHaltReported(err) {
			t.Errorf("halt must come from the missing-key policy, got: %v", err)
		}
	})

	t.Run("path template still renders a present parameter", func(t *testing.T) {
		t.Parallel()
		got, err := EvalPathTemplate("t", "subDirectory", "web", `{{ .parameters.region }}`, pathTemplateFixture())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "eu-west-1" {
			t.Errorf("rendered %q, want %q", got, "eu-west-1")
		}
	})

	t.Run("assertion halts on an absent key", func(t *testing.T) {
		t.Parallel()
		err := EvaluateAssertion("t", `{{ .values.missing }}`, AssertionContext{Values: map[string]any{"present": "p"}})
		if err == nil {
			t.Fatal("an assertion reading an absent key must halt")
		}
		if !missingKeyHaltReported(err) {
			t.Errorf("halt must come from the missing-key policy, got: %v", err)
		}
	})

	t.Run("assertion still reads a present key", func(t *testing.T) {
		t.Parallel()
		if err := EvaluateAssertion("t", `{{ .values.present }}`, AssertionContext{Values: map[string]any{"present": "p"}}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// A component-level self-inject value: template carries the same missing-key
// policy as every other value template, so it renders an unset value the same way.
// The behaviour follows the surface's policy rather than its call site, which is
// what keeps the two from drifting.
func TestEvalSelfInjectValue_UnsetValueRendersEmpty(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("present", "vp")
	ctx := SelfInjectContext{Values: values, Context: SelfInjectContextBlock{InstanceID: "db"}}

	got, err := evalSelfInjectValue("t", `{{ .values.missing }}`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("rendered %q, want empty", got)
	}

	got, err = evalSelfInjectValue("t", `{{ .values.present }}`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "vp" {
		t.Errorf("rendered %q, want %q", got, "vp")
	}
}

// Rendering an unset key empty is confined to the one execution that read it: the
// conversion allocates a fresh tree per evaluation, so nothing written there
// reaches the caller's context or a later evaluation against it. The optional
// helper is the sharpest probe available, because it reports presence rather than
// value — if the key had leaked, it would find it.
func TestEvalTemplate_UnsetValueDoesNotOutliveOneExecution(t *testing.T) {
	t.Parallel()
	ctx := unsetValueFixture()

	if _, err := EvalTemplate("first", `{{ .values.missing }}`, ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := EvalTemplate("second", `{{ optional .values "missing" "fb" }}`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fb" {
		t.Errorf("rendered %q, want %q — a key read in one evaluation must not be present in the next", got, "fb")
	}
	if _, present := ctx.Values.Get("missing"); present {
		t.Error("the caller's own pillar must be unchanged")
	}
}

// The optional helper reports presence, so a key rendered empty for one template
// counts as present to an optional call in that same template. Reading a key one
// way per template keeps the two answers the same. This pins the interaction rather
// than leaving it to be rediscovered as a defect.
func TestEvalTemplate_OptionalSeesAKeyTheSameTemplateAlsoReadsAsAChain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tmpl string
		want string
	}{
		{
			name: "optional alone still supplies its fallback",
			tmpl: `{{ optional .values "missing" "fb" }}`,
			want: "fb",
		},
		{
			name: "a chained read of the same key in the same template makes it present",
			tmpl: `{{ .values.missing }}|{{ optional .values "missing" "fb" }}`,
			want: "|",
		},
		{
			name: "a chained read of a different key leaves optional alone",
			tmpl: `{{ .values.other }}|{{ optional .values "missing" "fb" }}`,
			want: "|fb",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("t", tc.tmpl, unsetValueFixture())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// A value template has no .parameters pillar: parameter fulfillment is interpolated
// into the Aggregate before the render context exists, so the reference names a
// root this context does not define and is refused rather than rendered empty.
func TestEvalTemplate_ParametersIsNotAValueTemplateRoot(t *testing.T) {
	t.Parallel()
	_, err := EvalTemplate("t", `{{ .parameters.db_size }}`, unsetValueFixture())
	if err == nil {
		t.Fatal("a value template reading .parameters must halt")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("halt must be the unknown-reference one, got: %v", err)
	}
}
