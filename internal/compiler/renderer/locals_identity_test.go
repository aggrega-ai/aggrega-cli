// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A locals block reads two identities, and they answer two different questions:
// .context.instance_id is the HOST COMPONENT, on every surface alike, and
// .attachment_name is the attachment instance being resolved. The defect this file
// pins was the first name meaning the attachment on the two FeatureRecipe locals
// paths while meaning the host everywhere else — so the guard is deliberately shaped
// as one authored block, read for BOTH names, run through both paths, with the host
// id and the instance name held to deliberately different strings. A test that reused
// one string for both could not tell a correct binding from the defect.
//
// The block also carries a positive control on each path — an entry reading a pillar
// this change does not touch — so a uniformly-broken harness cannot produce a passing
// result by rendering everything empty.

const (
	identityHostID       = "checkout-api"
	identityInstanceName = "scrape_target"
)

func identityLocalsFR() *types.FeatureRecipe {
	locals := types.OrderedMap[string]{}
	locals.Set("host", `{{ .context.instance_id }}`)
	locals.Set("attached_as", `{{ .attachment_name }}`)
	// Positive control: a pillar this change does not touch. If the harness were
	// rendering everything empty, this would be empty too.
	locals.Set("control", `{{ .values.probe }}`)
	// And a sibling read, so the prefix publication is exercised on the same block
	// that carries the two identities.
	locals.Set("derived", `{{ .locals.host }}-{{ .locals.attached_as }}`)
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "monitoring"},
		Spec:     types.FeatureRecipeSpec{Locals: locals},
	}
	return fr
}

func identityLocalsValues() types.OrderedMap[any] {
	values := types.OrderedMap[any]{}
	values.Set("probe", "control-value")
	return values
}

// assertIdentityLocals holds one resolved block to the denotation on whichever path
// produced it, so both paths are checked against one expectation rather than two.
func assertIdentityLocals(t *testing.T, path string, got types.OrderedMap[string]) {
	t.Helper()
	for _, want := range []struct{ key, value, why string }{
		{"host", identityHostID, ".context.instance_id is the host component"},
		{"attached_as", identityInstanceName, ".attachment_name is the attachment instance"},
		{"control", "control-value", "the positive control must resolve, or the harness is rendering nothing"},
		{"derived", identityHostID + "-" + identityInstanceName, "a sibling read receives resolved values"},
	} {
		v, ok := got.Get(want.key)
		if !ok {
			t.Errorf("%s: locals[%q] absent — %s", path, want.key, want.why)
			continue
		}
		if v != want.value {
			t.Errorf("%s: locals[%q] = %q, want %q — %s", path, want.key, v, want.value, want.why)
			continue
		}
		t.Logf("%-28s locals[%-12s] = %q", path, want.key, v)
	}
}

// The per-component attachment path, at its own entry point.
func TestLocalsIdentity_PerComponentPath(t *testing.T) {
	t.Parallel()
	got, err := EvaluateFeatureRecipeLocals(
		identityLocalsFR(), identityInstanceName, identityHostID, "",
		identityLocalsValues(), types.OrderedMap[any]{}, resolver.SensitiveLeaves{})

	if err != nil {
		t.Fatalf("the attachment's locals must resolve: %v", err)
	}
	assertIdentityLocals(t, "per-component path", got)
}

// The Phase A interception path, reached through ExecutePhaseA so the resolved
// values are observed where that path actually publishes them — the delivery pipe —
// rather than by reaching inside for the map.
func TestLocalsIdentity_PhaseAPath(t *testing.T) {
	t.Parallel()
	fr := identityLocalsFR()
	fr.Spec.InjectMapping = `env:
  HOST: "{{ .locals.host }}"
  ATTACHED_AS: "{{ .locals.attached_as }}"
  CONTROL: "{{ .locals.control }}"
  DERIVED: "{{ .locals.derived }}"
`
	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set(identityInstanceName, types.TemplateAttachment{
		Type:   "core/monitoring",
		Values: identityLocalsValues(),
	})
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: identityHostID,
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
		Locals:   types.OrderedMap[string]{},
		Features: features,
	}
	if _, err := ExecutePhaseA(ctx, []FeatureRecipeRef{
		{InstanceName: identityInstanceName, FeatureRecipe: fr},
	}, hostWithheld{}); err != nil {
		t.Fatalf("Phase A must resolve the attachment's locals: %v", err)
	}

	delivered := types.OrderedMap[string]{}
	for _, pair := range []struct{ localsKey, envKey string }{
		{"host", "HOST"},
		{"attached_as", "ATTACHED_AS"},
		{"control", "CONTROL"},
		{"derived", "DERIVED"},
	} {
		v, ok := ctx.Context.Inject.Env.Get(pair.envKey)
		if !ok {
			t.Errorf("Phase A path: inject.env.%s absent", pair.envKey)
			continue
		}
		delivered.Set(pair.localsKey, v)
	}
	assertIdentityLocals(t, "Phase A path", delivered)
}

// The two paths iterate the same authored source, so a block reached through both in
// one build must resolve to the same values on each. The defect was reachable exactly
// there: one authored block evaluated twice under two identities.
func TestLocalsIdentity_BothPathsAgreeOnOneBlock(t *testing.T) {
	t.Parallel()
	perComponent, err := EvaluateFeatureRecipeLocals(
		identityLocalsFR(), identityInstanceName, identityHostID, "",
		identityLocalsValues(), types.OrderedMap[any]{}, resolver.SensitiveLeaves{})

	if err != nil {
		t.Fatalf("per-component path: %v", err)
	}

	fr := identityLocalsFR()
	fr.Spec.InjectMapping = `env:
  HOST: "{{ .locals.host }}"
`
	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set(identityInstanceName, types.TemplateAttachment{
		Type:   "core/monitoring",
		Values: identityLocalsValues(),
	})
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: identityHostID,
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
		Locals:   types.OrderedMap[string]{},
		Features: features,
	}
	if _, err := ExecutePhaseA(ctx, []FeatureRecipeRef{
		{InstanceName: identityInstanceName, FeatureRecipe: fr},
	}, hostWithheld{}); err != nil {
		t.Fatalf("Phase A path: %v", err)
	}
	phaseAHost, _ := ctx.Context.Inject.Env.Get("HOST")
	renderHost, _ := perComponent.Get("host")
	if phaseAHost != renderHost {
		t.Errorf("the two attachment-locals paths disagree on .context.instance_id: Phase A %q, per-component %q",
			phaseAHost, renderHost)
	}
	t.Logf("both paths resolved .context.instance_id to %q", renderHost)
}

// A base Recipe's locals surface refuses .attachment_name: no attachment is being
// resolved there, so the reference HALTS rather than rendering empty. The base local
// reading .context.instance_id in the same block is the positive control — it must
// resolve, or the halt below would prove nothing about the narrowing.
func TestLocalsIdentity_BaseRecipeRefusesAttachmentName(t *testing.T) {
	t.Parallel()
	recipeWith := func(locals types.OrderedMap[string]) *types.Recipe {
		return &types.Recipe{
			Metadata: types.RecipeMetadata{Name: "api-recipe"},
			Spec:     types.RecipeSpec{Locals: locals},
		}
	}

	// Positive control, first: the surface renders, and .context.instance_id on it is
	// the node id.
	control := types.OrderedMap[string]{}
	control.Set("host", `{{ .context.instance_id }}`)
	got, err := EvaluateRecipeLocals(recipeWith(control), identityHostID, types.OrderedMap[any]{}, types.OrderedMap[any]{}, redact.Keys{})

	if err != nil {
		t.Fatalf("control: a base local reading .context.instance_id must resolve: %v", err)
	}
	if v, _ := got.Get("host"); v != identityHostID {
		t.Fatalf("control: base locals[host] = %q, want %q", v, identityHostID)
	}
	t.Logf("control: base locals[host] = %q", identityHostID)

	// The refusal.
	refused := types.OrderedMap[string]{}
	refused.Set("who", `{{ .attachment_name }}`)
	_, err = EvaluateRecipeLocals(recipeWith(refused), identityHostID, types.OrderedMap[any]{}, types.OrderedMap[any]{}, redact.Keys{})

	if err == nil {
		t.Fatal("a base Recipe local reading .attachment_name must halt, not render empty")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".attachment_name") {
		t.Errorf("diagnostic does not name the authored reference: %s", diagnosticChainText(err))
	}
	t.Logf("base locals refusal: %s", diagnosticChainText(err))

	// A fallback wrapper cannot launder the refused root into an empty value.
	wrapped := types.OrderedMap[string]{}
	wrapped.Set("who", `{{ default "x" .attachment_name }}`)
	if _, err := EvaluateRecipeLocals(recipeWith(wrapped), identityHostID, types.OrderedMap[any]{}, types.OrderedMap[any]{}, redact.Keys{}); !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("a wrapped .attachment_name must halt on the base surface too, got: %v", err)
	}
}

// The Recipe source surface shares the base locals scope, so it refuses the same
// root for the same reason — and its own positive control resolves.
func TestLocalsIdentity_RecipeSourceRefusesAttachmentName(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource(
		`oci://example.test/charts/{{ .context.instance_id }}:1.0.0`, identityHostID,
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{}, redact.Keys{})

	if err != nil {
		t.Fatalf("control: a source template reading .context.instance_id must resolve: %v", err)
	}
	if got != "oci://example.test/charts/"+identityHostID+":1.0.0" {
		t.Fatalf("control: source resolved to %v", got)
	}

	_, err = EvaluateRecipeSource(
		`oci://example.test/charts/{{ .attachment_name }}:1.0.0`, identityHostID,
		types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{}, redact.Keys{})

	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("a source template reading .attachment_name must halt, got: %v", err)
	}
}
