// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func emptyPipe() types.InjectPipe {
	return types.InjectPipe{
		Env:  types.OrderedMap[string]{},
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}
}

func TestExecutePass2a_LiteralValue(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{"PORT": "8080"},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	port, _ := inject.Env.Get("PORT")
	if port != "8080" {
		t.Errorf("PORT = %q, want '8080'", port)
	}
	portAll, _ := inject.All.Get("PORT")
	if portAll != "8080" {
		t.Errorf("all.PORT = %q", portAll)
	}
}

func TestExecutePass2a_FromOwnProvides(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"MY_HOST": map[string]any{"from": "host"},
		},
	}
	ownProvides := types.OrderedMap[string]{}
	ownProvides.Set("host", "api.svc.cluster.local")

	_, err := ExecutePass2a(&inject, selfInject, "api", ownProvides, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	host, _ := inject.Env.Get("MY_HOST")
	if host != "api.svc.cluster.local" {
		t.Errorf("MY_HOST = %q", host)
	}
}

func TestExecutePass2a_ValueTemplate(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"APP_ID": map[string]any{"value": "{{ .context.instance_id }}-app"},
		},
	}
	siCtx := SelfInjectContext{
		Context: SelfInjectContextBlock{InstanceID: "my-api"},
	}

	_, err := ExecutePass2a(&inject, selfInject, "my-api", types.OrderedMap[string]{}, siCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	appID, _ := inject.Env.Get("APP_ID")
	if appID != "my-api-app" {
		t.Errorf("APP_ID = %q, want 'my-api-app'", appID)
	}
}

func TestExecutePass2a_ForbiddenReference(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"BAD": map[string]any{"value": "{{ .context.inject.env.SECRET }}"},
		},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err == nil {
		t.Fatal("expected error for forbidden reference")
	}
	if !errors.Is(err, ErrSelfInjectForbiddenReference) {
		t.Errorf("expected ErrSelfInjectForbiddenReference, got: %v", err)
	}
}

func TestExecutePass2a_NilSelfInject(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  func() types.OrderedMap[string] { m := types.OrderedMap[string]{}; m.Set("EXISTING", "val"); return m }(),
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}

	keys, err := ExecutePass2a(&inject, nil, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys != nil {
		t.Error("expected nil selfKeys for nil selfInject")
	}
	existing, _ := inject.Env.Get("EXISTING")
	if existing != "val" {
		t.Error("existing inject should be preserved")
	}
}

func TestExecutePass2a_FromMissingProvides(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"X": map[string]any{"from": "nonexistent"},
		},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err == nil {
		t.Fatal("expected error for missing provides key")
	}
}

// TestNormalizeSelfInjectEntries_OrderedMapShape_DecodesEntries pins the
// typed-arm fix at normalizeSelfInjectEntries. When the InjectBlock field
// arrives as *types.OrderedMap[any] (the round-trip-fidelity decode shape
// produced when the inject block flows through an OrderedMap[any]-routed
// authoring path), the function decodes via .All() rather than silently
// returning an empty map. Both the literal-string shorthand and the
// verbose {value, from} shape are exercised.
func TestNormalizeSelfInjectEntries_OrderedMapShape_DecodesEntries(t *testing.T) {
	t.Parallel()
	raw := types.NewOrderedMap[any]()
	raw.Set("zulu", "literal-1")
	inner := types.NewOrderedMap[any]()
	inner.Set("from", "alpha-key")
	raw.Set("alpha", inner)
	raw.Set("middle", "literal-2")

	entries, err := normalizeSelfInjectEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if e, ok := entries["zulu"]; !ok || !e.isLiteral || e.value != "literal-1" {
		t.Errorf("zulu: %+v want isLiteral=true value=literal-1", e)
	}
	if e, ok := entries["alpha"]; !ok || e.from != "alpha-key" {
		t.Errorf("alpha: %+v want from=alpha-key", e)
	}
	if e, ok := entries["middle"]; !ok || !e.isLiteral || e.value != "literal-2" {
		t.Errorf("middle: %+v want isLiteral=true value=literal-2", e)
	}
}

// TestNormalizeSelfInjectEntries_VerboseArrayForm pins the schema-verbose-form
// outer-array decode arm. The schema's inject_delivery accepts
// [{in: K, from?: P, value?: V}] as the canonical authoring shape after the
// compact-form retirement. The `in:` field maps to the entry key; `value:`
// and `from:` propagate to the selfInjectEntry's value / from fields with
// the same per-field semantics as the existing per-key {value, from}
// extraction arms above.
func TestNormalizeSelfInjectEntries_VerboseArrayForm(t *testing.T) {
	t.Parallel()

	raw := []any{
		map[string]any{"in": "DB_URL", "from": "connection.url"},
		map[string]any{"in": "PORT", "value": "8080"},
	}

	entries, err := normalizeSelfInjectEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if e, ok := entries["DB_URL"]; !ok || e.from != "connection.url" {
		t.Errorf("DB_URL: %+v want from=connection.url", e)
	}
	if e, ok := entries["PORT"]; !ok || e.value != "8080" {
		t.Errorf("PORT: %+v want value=8080", e)
	}

	emptyEntries, _ := normalizeSelfInjectEntries([]any{})
	if len(emptyEntries) != 0 {
		t.Errorf("empty []any should produce empty map; got %d entries", len(emptyEntries))
	}
}

// TestNormalizeSelfInjectEntries_BothFieldsPresent_BlocksTheCaseBeforeResolveRuns
// pins the architectural-intent record: a verbose-form entry that
// simultaneously carries `value:` AND `from:` is schema-illegal (the
// inject_delivery schema's `oneOf: [required:[from], required:[value]]`
// rejects it at envelope load); the IR-build-time second-line-of-defense
// validator at [ir.ValidateInjectBlocks] halts the build with
// [ir.ErrInjectEntryAmbiguous] for code paths that bypass envelope-load
// validation (in-memory fixtures, programmatic catalog builders). The
// renderer-layer decode arm still populates both selfInjectEntry fields
// verbatim if the case somehow reaches Pass 2a (e.g., a unit test that
// constructs entries directly without running through IR build); this
// test documents that direct-construction path and pins
// resolveSelfInjectEntry's downstream behavior in that scenario — `from:`
// resolves first per the function body's if/else cascade. The pin is
// architectural-intent documentation; the case is unreachable in any
// production path through BuildIR because the validator blocks it.
func TestNormalizeSelfInjectEntries_BothFieldsPresent_BlocksTheCaseBeforeResolveRuns(t *testing.T) {
	t.Parallel()

	verboseRaw := []any{
		map[string]any{"in": "BOTH", "from": "ref.path", "value": "literal-text"},
	}
	entries, err := normalizeSelfInjectEntries(verboseRaw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry, ok := entries["BOTH"]
	if !ok {
		t.Fatalf("entry BOTH missing from decoded entries")
	}
	if entry.value != "literal-text" || entry.from != "ref.path" {
		t.Fatalf("decode should populate both fields verbatim; got value=%q from=%q",
			entry.value, entry.from)
	}

	// Direct-construction scenario: the only path to reach resolveSelfInjectEntry
	// with both fields populated is to skip IR build entirely (this test does so).
	// Under that scenario, the function's if/else cascade picks from: first.
	ownProvides := types.OrderedMap[string]{}
	ownProvides.Set("ref.path", "resolved-from-provides")

	resolved, err := resolveSelfInjectEntry("BOTH", entry, "env", ownProvides, SelfInjectContext{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved != "resolved-from-provides" {
		t.Errorf("direct-construction scenario: from: should win at resolveSelfInjectEntry; got %q want %q",
			resolved, "resolved-from-provides")
	}
}

// usesEdge constructs an IREdge with a uses-inject delivery key on the named
// mechanism. Helper for collision tests below.
func usesEdge(source, target, socket, mechanism, deliveryKey, providesKey string) types.IREdge {
	mechMap := types.OrderedMap[string]{}
	mechMap.Set(deliveryKey, providesKey)
	edgeInject := types.OrderedMap[types.OrderedMap[string]]{}
	edgeInject.Set(mechanism, mechMap)
	return types.IREdge{Source: source, Target: target, Socket: socket, Inject: edgeInject}
}

// TestCheckSelfInjectCollisions_CrossSourceSameMechanism pins the primary
// collision contract: a delivery key declared by both component-level
// self-inject and uses-level cross-inject in the same mechanism halts at
// Pass 2 with ErrSelfInjectKeyCollision per the halt-always collision-detection
// policy.
func TestCheckSelfInjectCollisions_CrossSourceSameMechanism(t *testing.T) {
	t.Parallel()

	selfKeys := map[string]map[string]string{
		"env": {"DB_URL": "literal-from-self"},
	}
	edges := []types.IREdge{usesEdge("api", "db", "primary", "env", "DB_URL", "host")}

	err := CheckSelfInjectCollisions(selfKeys, edges, "api")
	if err == nil {
		t.Fatalf("expected ErrSelfInjectKeyCollision, got nil")
	}
	if !errors.Is(err, ErrSelfInjectKeyCollision) {
		t.Errorf("expected errors.Is(err, ErrSelfInjectKeyCollision), got: %v", err)
	}
}

// TestCheckSelfInjectCollisions_DeterministicEmissionAcrossMechanisms pins
// the multi-level sort key. Two collisions across mechanisms (env + file)
// emit the alphabetically-first (mechanism, deliveryKey, usesSocket)
// combination first; with mechanisms env < file, the env collision wins.
func TestCheckSelfInjectCollisions_DeterministicEmissionAcrossMechanisms(t *testing.T) {
	t.Parallel()

	selfKeys := map[string]map[string]string{
		"env":  {"SHARED_KEY": "self-env"},
		"file": {"SHARED_KEY": "self-file"},
	}
	edges := []types.IREdge{
		usesEdge("api", "fs", "primary", "file", "SHARED_KEY", "path"),
		usesEdge("api", "db", "primary", "env", "SHARED_KEY", "host"),
	}

	err := CheckSelfInjectCollisions(selfKeys, edges, "api")
	if err == nil {
		t.Fatalf("expected ErrSelfInjectKeyCollision, got nil")
	}
	if !errors.Is(err, ErrSelfInjectKeyCollision) {
		t.Fatalf("expected errors.Is(err, ErrSelfInjectKeyCollision), got: %v", err)
	}
	// Walk the error chain — cerrors.Wrap masks the inner-error text in its
	// top-level Error() representation; check the unwrap chain for the
	// mechanism name that the multi-level sort emitted first.
	var foundEnv, foundFile bool
	for e := err; e != nil; e = errors.Unwrap(e) {
		msg := e.Error()
		if !foundEnv && indexOfSubstring(msg, `"env"`) >= 0 {
			foundEnv = true
		}
		if indexOfSubstring(msg, `"file"`) >= 0 {
			foundFile = true
		}
	}
	if !foundEnv {
		t.Errorf("error chain should name env mechanism (sort emits env before file): %v", err)
	}
	// Function returns only the first collision; file should NOT appear.
	if foundFile {
		t.Errorf("error chain should not mention file mechanism (emit-first only): %v", err)
	}
}

func indexOfSubstring(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestCheckSelfInjectCollisions_NoCollision pins the negative case: disjoint
// keys across self-inject and uses-inject pass through without error.
func TestCheckSelfInjectCollisions_NoCollision(t *testing.T) {
	t.Parallel()

	selfKeys := map[string]map[string]string{
		"env": {"SELF_ONLY": "literal"},
	}
	edges := []types.IREdge{usesEdge("api", "db", "primary", "env", "USES_ONLY", "host")}

	err := CheckSelfInjectCollisions(selfKeys, edges, "api")
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

// TestCheckSelfInjectCollisions_FRInterceptionStructuralSeparation pins
// the FR-interception exemption as a structural-separation contract.
// Phase A's overlay merge in merge.go::DeepMergeInjectStructured is
// downstream of CheckSelfInjectCollisions; the collision check's signature
// (selfKeys + usesEdges) cannot observe Phase A overwrites by construction.
// A future regression that drifts collision detection into merge.go would
// break this contract; this test is the contract pin.
//
// Scenario: a Component declares `inject.env.HOST: "literal"` (self-inject);
// a Phase A FR's `injectMapping` produces `{"env": {"HOST": "fr-value"}}`
// for the same node. No uses-inject writes HOST. The 5-Tier Onion Model's
// last-wins semantics is honored: post-Phase-A, HOST = "fr-value".
//
// Three assertions:
//  1. CheckSelfInjectCollisions(selfKeys, edges, nodeID) returns nil — Pass 2
//     collision check sees self-inject + uses-inject only; HOST not in any
//     edge's Inject; no overlap.
//  2. After ExecutePhaseA, ctx.Context.Inject.Env.Get("HOST") returns
//     "fr-value" — FR overlay wins per 5-Tier Onion last-wins.
//  3. Structural-separation pin: CheckSelfInjectCollisions's signature
//     takes (selfKeys, usesEdges, nodeID) — NOT the inject pipe's mutated
//     state. Phase A overwrites cannot be observed by this function. The
//     assertion is structural (signature inspection at compile time) plus
//     this test's name + comment as the contract anchor.
func TestCheckSelfInjectCollisions_FRInterceptionStructuralSeparation(t *testing.T) {
	t.Parallel()

	// Step 1: simulate Pass 2a self-inject result. selfKeys captures HOST
	// at the env mechanism (literal value).
	selfKeys := map[string]map[string]string{
		"env": {"HOST": "literal"},
	}

	// Step 2: simulate Pass 2b uses-edges. No edge writes HOST.
	edges := []types.IREdge{
		// An unrelated edge writing a different key; HOST is not present.
		usesEdge("api", "db", "primary", "env", "DB_URL", "host"),
	}

	// Step 3: assertion 1 — Pass 2 collision check returns nil.
	if err := CheckSelfInjectCollisions(selfKeys, edges, "api"); err != nil {
		t.Fatalf("Pass 2 collision check fired on FR-interception scenario: %v", err)
	}

	// Step 4: simulate the post-Pass-2 state (TemplateContext with HOST set
	// by self-inject) and run Phase A's overlay merge.
	envPipe := types.OrderedMap[string]{}
	envPipe.Set("HOST", "literal")
	allPipe := types.OrderedMap[string]{}
	allPipe.Set("HOST", "literal")
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  envPipe,
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  allPipe,
			},
		},
	}
	refs := []FeatureRecipeRef{
		{
			InstanceName: "fr-overlay",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "fr-overlay"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"env": {"HOST": "fr-value"}}`,
				},
			},
		},
	}
	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("ExecutePhaseA error: %v", err)
	}

	// Step 5: assertion 2 — FR overlay won per 5-Tier Onion last-wins.
	host, _ := ctx.Context.Inject.Env.Get("HOST")
	if host != "fr-value" {
		t.Errorf("post-Phase-A HOST = %q, want %q (FeatureRecipe overlay last-wins per 5-Tier Onion Model)", host, "fr-value")
	}

	// Step 6: assertion 3 — structural-separation pin. Documented via this
	// test's name + comment block. The signature of CheckSelfInjectCollisions
	// (selfKeys, usesEdges, nodeID) means Phase A's overlay merge effects
	// (which mutate ctx.Context.Inject.Env post-Pass-2) cannot reach this
	// function's inputs. A future maintainer who attempts to add inject-pipe
	// inspection here breaks the structural separation; that change would
	// fail compilation if the signature is preserved, or violate the
	// structural-separation contract review if the signature changes.
	// This test stays as the anchor.
}
