// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// phaseALocalsFixture builds a node whose base Recipe locals are already
// resolved and which carries one attachment with its own values and infra. frLocals
// is the attachment's authored locals block; mapping is its injectMapping.
func phaseALocalsFixture(frLocals types.OrderedMap[string], mapping string) (*types.TemplateContext, []FeatureRecipeRef) {
	base := types.OrderedMap[string]{}
	base.Set("db_host", "db-base.svc")

	attValues := types.OrderedMap[any]{}
	attValues.Set("port", 9090)
	attValues.Set("path_suffix", "metrics")
	attInfra := types.OrderedMap[any]{}
	attInfra.Set("region", "eu-west-1")

	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set("mon", types.TemplateAttachment{
		Type:   "core/monitoring",
		Values: attValues,
		Infra:  attInfra,
	})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "checkout-api",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
		Locals:   base,
		Features: features,
	}

	refs := []FeatureRecipeRef{{
		InstanceName: "mon",
		FeatureRecipe: &types.FeatureRecipe{
			Metadata: types.FeatureRecipeMetadata{Name: "core/monitoring/prometheus"},
			Spec: types.FeatureRecipeSpec{
				Locals:        frLocals,
				InjectMapping: mapping,
			},
		},
	}}
	return ctx, refs
}

// TestExecutePhaseA_FeatureLocalsAreEvaluated is the end-to-end witness that an
// attachment's own locals carrying template actions reach injectMapping as
// resolved values. Without evaluation the authored text travels verbatim into the
// delivery pipe and from there into an emitted artifact, so each assertion below
// names a value that could only exist if the template ran.
func TestExecutePhaseA_FeatureLocalsAreEvaluated(t *testing.T) {
	t.Parallel()
	frLocals := types.OrderedMap[string]{}
	frLocals.Set("scrape_port", `{{ .values.port }}`)
	// Reads an EARLIER sibling and the host component's identity — the scrape URL
	// addresses the component being monitored, not the attachment doing the
	// monitoring.
	frLocals.Set("scrape_url", `http://{{ .context.instance_id }}:{{ .locals.scrape_port }}/{{ .values.path_suffix }}`)
	frLocals.Set("region_tag", `{{ .infra.region }}`)
	// The attachment's own identity travels on its own name.
	frLocals.Set("attached_as", `{{ .attachment_name }}`)

	ctx, refs := phaseALocalsFixture(frLocals,
		`{"env": {"SCRAPE_URL": "{{ .locals.scrape_url }}", "SCRAPE_PORT": "{{ .locals.scrape_port }}", "REGION": "{{ .locals.region_tag }}", "DB_HOST": "{{ .locals.db_host }}", "ATTACHED_AS": "{{ .locals.attached_as }}"}}`)

	if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, tc := range []struct{ key, want string }{
		{"SCRAPE_PORT", "9090"},                            // .values read
		{"REGION", "eu-west-1"},                            // .infra read
		{"SCRAPE_URL", "http://checkout-api:9090/metrics"}, // sibling read, in author order; the host component
		{"ATTACHED_AS", "mon"},                             // the attachment instance, on its own name
		{"DB_HOST", "db-base.svc"},                         // base local still visible to injectMapping
	} {
		got, _ := ctx.Context.Inject.Env.Get(tc.key)
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestExecutePhaseA_FeatureLocalsForwardReferenceHalts holds the author-order
// contract on this surface: a later sibling is not resolved yet and reading one
// halts rather than rendering empty.
func TestExecutePhaseA_FeatureLocalsForwardReferenceHalts(t *testing.T) {
	t.Parallel()
	frLocals := types.OrderedMap[string]{}
	frLocals.Set("reader", `{{ .locals.later }}`)
	frLocals.Set("later", `resolved`)

	ctx, refs := phaseALocalsFixture(frLocals, `{"env": {"X": "{{ .locals.reader }}"}}`)

	_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err == nil {
		t.Fatal("expected a halt on a forward sibling reference, got nil")
	}
	if !errors.Is(err, ErrLocalsForwardReference()) {
		t.Errorf("error does not carry the forward-reference sentinel: %v", err)
	}
	if _, exists := ctx.Context.Inject.Env.Get("X"); exists {
		t.Error("a halted attachment still wrote to the delivery pipe")
	}
}

// TestExecutePhaseA_FeatureLocalsCannotReadBaseLocal pins the deliberate refusal.
// An attachment's locals resolve against the attachment's own scope; the base
// Recipe's locals merge over the result AFTER it resolves, so they are not in
// scope while an attachment local evaluates. The refusal is the same one the
// attachment's render path gives, and the second half of this test holds the two
// surfaces to that one answer.
func TestExecutePhaseA_FeatureLocalsCannotReadBaseLocal(t *testing.T) {
	t.Parallel()
	frLocals := types.OrderedMap[string]{}
	frLocals.Set("mirror", `{{ .locals.db_host }}`)

	ctx, refs := phaseALocalsFixture(frLocals, `{"env": {"MIRROR": "{{ .locals.mirror }}"}}`)

	_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err == nil {
		t.Fatal("expected a halt reading a base Recipe local from an attachment local, got nil")
	}
	if !errors.Is(err, ErrLocalsUnknownReference()) {
		t.Errorf("error does not carry the unknown-reference sentinel: %v", err)
	}

	// Same block, the attachment render path: the answer must not differ.
	_, perr := EvaluateFeatureRecipeLocals(refs[0].FeatureRecipe, "mon", "checkout-api", "", types.OrderedMap[any]{}, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})

	if !errors.Is(perr, ErrLocalsUnknownReference()) {
		t.Errorf("the two attachment-locals surfaces disagree: interception halt %v, render path %v", err, perr)
	}
}

// TestExecutePhaseA_FeatureLocalsBrokenTemplateHalts is the negative control: a
// local whose template cannot evaluate stops the build instead of delivering its
// unevaluated text to the pipe.
func TestExecutePhaseA_FeatureLocalsBrokenTemplateHalts(t *testing.T) {
	t.Parallel()
	frLocals := types.OrderedMap[string]{}
	frLocals.Set("scrape_port", `{{ .values.port | noSuchFunction }}`)

	ctx, refs := phaseALocalsFixture(frLocals, `{"env": {"SCRAPE_PORT": "{{ .locals.scrape_port }}"}}`)

	_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
	if err == nil {
		t.Fatal("expected a halt on an unevaluable local, got nil")
	}
	if _, exists := ctx.Context.Inject.Env.Get("SCRAPE_PORT"); exists {
		t.Error("a halted attachment still wrote to the delivery pipe")
	}
}

// TestExecutePhaseA_FeatureLocalsScopeMatchesRenderPath is the parity guard. Both
// attachment-locals surfaces build their evaluation context from the same type, so
// they admit and refuse the same roots; this test fails if either surface is later
// given a context the other does not have. It compares the ACCEPT/REFUSE verdict
// rather than the diagnostic text, because the two paths legitimately locate a
// halt differently.
func TestExecutePhaseA_FeatureLocalsScopeMatchesRenderPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		template string
		admitted bool
	}{
		{"values", `{{ .values.port }}`, true},
		{"infra", `{{ .infra.region }}`, true},
		{"instance identity", `{{ .context.instance_id }}`, true},
		{"provides", `{{ .provides.anything }}`, false},
		{"features", `{{ .features.mon.type }}`, false},
		{"delivery pipe", `{{ .context.inject.env.FOO }}`, false},
		{"edges", `{{ .context.uses.db.host }}`, false},
		{"pipeline steps", `{{ .steps.x.outputs.y }}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			frLocals := types.OrderedMap[string]{}
			frLocals.Set("probe", tc.template)

			ctx, refs := phaseALocalsFixture(frLocals, `{"env": {"P": "{{ .locals.probe }}"}}`)
			_, interceptErr := ExecutePhaseA(ctx, refs, hostWithheld{})
			if (interceptErr == nil) != tc.admitted {
				t.Errorf("interception surface: admitted=%v, want %v (err: %v)", interceptErr == nil, tc.admitted, interceptErr)
			}

			vals := types.OrderedMap[any]{}
			vals.Set("port", 9090)
			infra := types.OrderedMap[any]{}
			infra.Set("region", "eu-west-1")
			_, renderErr := EvaluateFeatureRecipeLocals(refs[0].FeatureRecipe, "mon", "checkout-api", "", vals, infra, resolver.SensitiveLeaves{})
			if (renderErr == nil) != (interceptErr == nil) {
				t.Errorf("the two attachment-locals surfaces disagree on %q: interception err=%v, render err=%v", tc.template, interceptErr, renderErr)
			}
		})
	}
}
