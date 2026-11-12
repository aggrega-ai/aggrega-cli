// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestExecutePhaseA_SimpleInjectMapping(t *testing.T) {
	t.Parallel()
	provides := types.OrderedMap[string]{}
	provides.Set("host", "db.namespace.svc.cluster.local")
	env := types.OrderedMap[string]{}
	env.Set("DB_URL", "original")
	all := types.OrderedMap[string]{}
	all.Set("DB_URL", "original")
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("my-db", types.EnrichedEdge{Target: "db", Provides: provides, Annotations: &types.AnnotationBuckets{Release: func() types.OrderedMap[string] {
		m := types.OrderedMap[string]{}
		m.Set("namespace", "payments")
		return m

	}()}})
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  env,
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  all,
			},
			Uses: uses,
		},
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "dns-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "k8s-dns-resolver"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"env": {"DB_URL": "{{ (index .context.uses "my-db").provides.host }}"}}`,
				},
			},
		},
	}

	_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DB_URL should be overwritten by the injectMapping
	dbURL, _ := ctx.Context.Inject.Env.Get("DB_URL")
	if dbURL != "db.namespace.svc.cluster.local" {
		t.Errorf("DB_URL = %q, want %q", dbURL, "db.namespace.svc.cluster.local")
	}
	// .all should be rebuilt
	allDBURL, _ := ctx.Context.Inject.All.Get("DB_URL")
	if allDBURL != "db.namespace.svc.cluster.local" {
		t.Errorf("all.DB_URL = %q", allDBURL)
	}
}

func TestExecutePhaseA_NoInjectMapping(t *testing.T) {
	t.Parallel()
	env := types.OrderedMap[string]{}
	env.Set("X", "1")
	all := types.OrderedMap[string]{}
	all.Set("X", "1")

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env: env,
				All: all,
			},
		},
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "empty",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "no-inject"},
				Spec:     types.FeatureRecipeSpec{},
			},
		},
	}

	_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, _ := ctx.Context.Inject.Env.Get("X"); v != "1" {
		t.Error("inject should be unchanged")
	}
}

// TestExecutePhaseA_LastWins pins the phase's own merge: a later mapping's key
// replaces whatever stood under it. That is a property of the phase in isolation
// and not of a build — two attachments' mappings writing one key are refused by
// the rule the render asks right after the phase, which this test does not reach.
// The merge itself stays last-wins because interception of a key the component
// or a connection delivered relies on it.
func TestExecutePhaseA_LastWins(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "first",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "first-recipe"},
				Spec:     types.FeatureRecipeSpec{InjectMapping: `{"env": {"HOST": "first-value"}}`},
			},
		},
		{
			InstanceName: "second",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "second-recipe"},
				Spec:     types.FeatureRecipeSpec{InjectMapping: `{"env": {"HOST": "second-value"}}`},
			},
		},
	}

	_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	host, _ := ctx.Context.Inject.Env.Get("HOST")
	if host != "second-value" {
		t.Errorf("HOST = %q, want 'second-value' (last wins)", host)
	}
}

func TestExecutePhaseA_FeatureLocalsOverlay(t *testing.T) {
	t.Parallel()
	locals :=

		// Base Recipe locals (already evaluated by orchestrator preamble in real flow).
		types.OrderedMap[string]{}
	locals.Set("host", "db-base.svc")
	locals.Set("port", "5432")
	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set("mon", types.TemplateAttachment{Type: "core/monitoring"})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{Env: types.OrderedMap[string]{}, File: types.OrderedMap[string]{}, Arg: types.OrderedMap[string]{}, All: types.OrderedMap[string]{}},
		},
		Locals:   locals,
		Features: features,
	}
	locals2 :=

		// FR with own locals — "host" collides (FR wins), "scrape_path" is new.
		// injectMapping references .locals to prove the merged map is visible.
		types.OrderedMap[string]{}
	locals2.Set("host", "db-fr.svc")
	locals2.Set("scrape_path", "/metrics")

	refs := []FeatureRecipeRef{
		{
			InstanceName: "mon",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "mon"},
				Spec: types.FeatureRecipeSpec{
					Locals:        locals2,
					InjectMapping: `{"env": {"SCRAPE_HOST": "{{ .locals.host }}", "SCRAPE_PATH": "{{ .locals.scrape_path }}"}}`,
				},
			},
		},
	}

	if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// FR's own "host" overrides base
	if got, _ := ctx.Context.Inject.Env.Get("SCRAPE_HOST"); got != "db-fr.svc" {
		t.Errorf("SCRAPE_HOST: got %q, want %q (FR locals should override base)", got, "db-fr.svc")
	}
	// FR-only key "scrape_path" visible
	if got, _ := ctx.Context.Inject.Env.Get("SCRAPE_PATH"); got != "/metrics" {
		t.Errorf("SCRAPE_PATH: got %q, want %q", got, "/metrics")
	}
	// Parent ctx.Locals untouched (shallow-copy contract)
	if got, _ := ctx.Locals.Get("host"); got != "db-base.svc" {
		t.Errorf("parent ctx.Locals was mutated: got %q", got)
	}
	if _, exists := ctx.Locals.Get("scrape_path"); exists {
		t.Error("FR-only local leaked into parent ctx")
	}
}

func TestExecutePhaseA_NilRecipe(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{Env: types.OrderedMap[string]{}},
		},
	}

	_, err := ExecutePhaseA(ctx, []FeatureRecipeRef{{InstanceName: "nil", FeatureRecipe: nil}}, hostWithheld{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestExecutePhaseA_CustomMechanismFlowsToAdditional pins the user-trap-
// closure invariant at the rendering layer. A Developer authoring a custom
// mechanism (ssm_param) on the Component-level inject block sees the data
// preserved end-to-end when the matched FR's injectMapping template produces
// a corresponding top-level key.
func TestExecutePhaseA_CustomMechanismFlowsToAdditional(t *testing.T) {
	t.Parallel()

	provides := types.OrderedMap[string]{}
	provides.Set("password", "secret-arn-1234")
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("my-secret", types.EnrichedEdge{Target: "vault", Provides: provides})

	authoredInject := &types.InjectBlock{}
	authoredInject.Additional.Set("ssm_param", []any{map[string]any{"in": "DB_PASSWORD", "from": "password"}})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
			Uses: uses,
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "secret-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "aws-ssm-resolver"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"ssm_param": {"DB_PASSWORD": "{{ (index .context.uses "my-secret").provides.password }}"}}`,
				},
			},
		},
	}

	if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ssm, ok := ctx.Context.Inject.Additional.Get("ssm_param")
	if !ok {
		t.Fatalf("expected ssm_param in Additional, got keys=%v", ctx.Context.Inject.Additional.Keys())
	}
	dbPwd, _ := ssm.Get("DB_PASSWORD")
	if dbPwd != "secret-arn-1234" {
		t.Errorf("ssm_param.DB_PASSWORD = %q, want %q", dbPwd, "secret-arn-1234")
	}
}

// TestExecutePhaseA_UnmappedCustomMechanismIsRecorded pins the half of the
// dangling-reference invariant this phase owns: a Developer authors a custom
// mechanism (ssm_param) on the Component-level inject block, the matched FR's
// injectMapping template produces only env keys, and the phase RECORDS the
// mechanism as unmapped rather than reaching a verdict on it.
//
// The verdict is the declared rule's, over exactly this record — the refusal
// rows for it are in checks_inject_mapping_mechanism_test.go and the row driving
// a whole build through it is in inject_mapping_mechanism_render_test.go. What
// this row proves is that the two halves the rule joins reach the record
// together and are attributed to the right documents: the mechanism to the
// component that declared it, and the FeatureRecipe to the mapping that did not
// name it.
//
// The attachment instance and the FeatureRecipe are deliberately named
// differently. They are independently authored — an instance name by whoever
// attached the Feature and a recipe name by whoever published it — and a record
// carrying the instance where it promises the recipe would read correctly on
// every fixture that lets the two coincide.
func TestExecutePhaseA_UnmappedCustomMechanismIsRecorded(t *testing.T) {
	t.Parallel()

	authoredInject := &types.InjectBlock{}
	authoredInject.Additional.Set("ssm_param", []any{map[string]any{"in": "DB_PASSWORD", "from": "password"}})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "wrong-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "env-only-resolver"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"env": {"OTHER_VAR": "value"}}`,
				},
			},
		},
	}

	delivered, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err != nil {
		t.Fatalf("the phase reaches no verdict of its own; got %v", err)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected one delivery report; got %d", len(delivered))
	}
	got := delivered[0].Mechanisms
	if len(got) != 1 {
		t.Fatalf("expected one recorded mechanism pairing; got %v", got)
	}
	if got[0].Mechanism != "ssm_param" {
		t.Errorf("Mechanism = %q; want ssm_param", got[0].Mechanism)
	}
	if got[0].Mapped {
		t.Errorf("ssm_param has no key in the output, so it must be recorded unmapped")
	}
	if got[0].Recipe != "env-only-resolver" {
		t.Errorf("Recipe = %q; want the FeatureRecipe name env-only-resolver, not the instance name", got[0].Recipe)
	}
}

// TestExecutePhaseA_MappedCustomMechanismIsRecordedMapped is the silent
// counterpart on the same shape: the mapping's output DOES carry a top-level key
// for the declared mechanism, so the pairing records it mapped and the rule over
// it raises nothing.
//
// It is here because a recording that reported every mechanism unmapped would
// satisfy the row above and refuse every build.
func TestExecutePhaseA_MappedCustomMechanismIsRecordedMapped(t *testing.T) {
	t.Parallel()

	authoredInject := &types.InjectBlock{}
	authoredInject.Additional.Set("ssm_param", []any{map[string]any{"in": "DB_PASSWORD", "from": "password"}})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "ssm-resolver"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"ssm_param": {"DB_PASSWORD": "/db/password"}}`,
				},
			},
		},
	}

	delivered, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err != nil {
		t.Fatalf("ExecutePhaseA: %v", err)
	}
	if len(delivered) != 1 || len(delivered[0].Mechanisms) != 1 {
		t.Fatalf("expected one delivery carrying one pairing; got %+v", delivered)
	}
	if !delivered[0].Mechanisms[0].Mapped {
		t.Errorf("the output carries an ssm_param key, so the mechanism is mapped")
	}
}

// TestExecutePhaseA_FlatOutputRecordsNoPairing pins the reading that keeps the
// env-only idiom legal: a flat delivery dictionary names no mechanism, so the
// phase records NO pairing at all rather than recording every declared mechanism
// unmapped.
//
// A recording that made the opposite reading would refuse every FeatureRecipe
// writing a flat output on a component that declares any injection block, and
// nothing else in this file would notice.
func TestExecutePhaseA_FlatOutputRecordsNoPairing(t *testing.T) {
	t.Parallel()

	authoredInject := &types.InjectBlock{}
	authoredInject.Additional.Set("ssm_param", []any{map[string]any{"in": "DB_PASSWORD", "from": "password"}})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "flat-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "flat-resolver-recipe"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"OTHER_VAR": "value"}`,
				},
			},
		},
	}

	delivered, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err != nil {
		t.Fatalf("ExecutePhaseA: %v", err)
	}
	if len(delivered) != 1 {
		t.Fatalf("expected one delivery report; got %d", len(delivered))
	}
	if got := delivered[0].Mechanisms; len(got) != 0 {
		t.Errorf("a flat output makes no claim about mechanisms; got %v", got)
	}
}

// TestExecutePhaseA_FlatMapFallbackUnchanged pins the legitimate flat-output
// idiom. A Developer authors only inject.env; the matched FR's injectMapping
// template produces a flat env-vars dictionary (no top-level env key); the
// flat-map fallback handles it without firing the dangling-reference halt.
func TestExecutePhaseA_FlatMapFallbackUnchanged(t *testing.T) {
	t.Parallel()

	authoredInject := &types.InjectBlock{
		Env: []any{map[string]any{"in": "DB_URL", "from": "host"}},
	}

	provides := types.OrderedMap[string]{}
	provides.Set("host", "db.svc.cluster.local")
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("my-db", types.EnrichedEdge{Target: "db", Provides: provides})

	env := types.OrderedMap[string]{}
	env.Set("DB_URL", "original")

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  env,
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
			Uses: uses,
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "flat-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "k8s-flat"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"DB_URL": "{{ (index .context.uses "my-db").provides.host }}"}`,
				},
			},
		},
	}

	if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
		t.Fatalf("unexpected error from flat-map fallback path: %v", err)
	}
	dbURL, _ := ctx.Context.Inject.Env.Get("DB_URL")
	if dbURL != "db.svc.cluster.local" {
		t.Errorf("DB_URL = %q, want %q", dbURL, "db.svc.cluster.local")
	}
}

// TestExecutePhaseA_InjectMappingReadsAttachmentName pins both identities an
// injectMapping template can name, read straight from the template rather than
// through a local. The two answer different questions and neither substitutes for
// the other: .context.instance_id is the host component the attachment resolves
// for, and .attachment_name is the attachment instance being resolved.
//
// HOST is the control. It resolves on this surface independently of the
// attachment name, so a harness that produced no output at all — an unevaluated
// template, an unmerged delivery pipe — fails it too, and a green ATTACHMENT
// cannot be an artifact of a uniformly broken fixture.
func TestExecutePhaseA_InjectMappingReadsAttachmentName(t *testing.T) {
	t.Parallel()

	// No locals: the reference under test is a root of the injectMapping
	// template's own context, not a value routed through a resolved local.
	ctx, refs := phaseALocalsFixture(types.OrderedMap[string]{},
		`{"env": {"HOST": "{{ .context.instance_id }}", "ATTACHMENT": "{{ .attachment_name }}"}}`)

	if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, tc := range []struct{ key, want string }{
		{"HOST", "checkout-api"}, // the host component, the control
		{"ATTACHMENT", "mon"},    // the attachment instance being resolved
	} {
		got, exists := ctx.Context.Inject.Env.Get(tc.key)
		if !exists {
			t.Errorf("%s absent from the delivery pipe", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
}
