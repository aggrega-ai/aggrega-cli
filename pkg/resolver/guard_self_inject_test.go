// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func selfInjectCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/web-server": // White-Box: allows env, file
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/web-server"},
					Spec:     types.ComponentSpec{Inject: []string{"env", "file"}},
				}, true
			case "core/api-with-ssm": // White-Box: allows env, ssm_param (custom mechanism)
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/api-with-ssm"},
					Spec:     types.ComponentSpec{Inject: []string{"env", "ssm_param"}},
				}, true
			case "core/datastore/postgres": // Black-Box: no spec.inject
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/datastore/postgres"},
					Spec:     types.ComponentSpec{},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardSelfInject_WhiteBoxAccepted(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Inject: &types.InjectBlock{Env: map[string]any{"PORT": "8080"}}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs[0])
	}
}

func TestGuardSelfInject_BlackBoxRejected(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres", Inject: &types.InjectBlock{Env: map[string]any{"X": "1"}}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())
	if len(errs) == 0 {
		t.Fatal("expected error for Black-Box self-inject")
	}
	if !errors.Is(errs[0], ErrSelfInjectForbidden) {
		t.Errorf("expected ErrSelfInjectForbidden, got: %v", errs[0])
	}
}

func TestGuardSelfInject_ForbiddenMechanism(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Inject: &types.InjectBlock{Arg: map[string]any{"--flag": "value"}}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())
	if len(errs) == 0 {
		t.Fatal("expected error for forbidden mechanism")
	}
	if !errors.Is(errs[0], ErrSelfInjectForbidden) {
		t.Errorf("expected ErrSelfInjectForbidden, got: %v", errs[0])
	}
}

func TestGuardSelfInject_KeyCollision(t *testing.T) {
	t.Parallel()
	inject := &types.InjectBlock{Env: map[string]any{"DB_URL": "host"}}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("db", types.VerboseUsesEntry{Target: "my-db", Inject: inject})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Inject: &types.InjectBlock{Env: map[string]any{"DB_URL": "localhost"}}, Uses: uses})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())
	found := false
	for _, e := range errs {
		if errors.Is(e, ErrInjectKeyCollision) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ErrInjectKeyCollision, got %d errors: %v", len(errs), errs)
	}
}

// TestGuardSelfInject_VerboseFormUsesInjectKeyCollision_OrderedMapShape pins
// the unmarshaled shape the guard must handle: a verbose-form `uses:`
// socket value arrives post-YAML-unmarshal as *types.OrderedMap[any], and
// the InjectBlock's Env / File / Arg payloads nested inside that verbose
// entry are also *types.OrderedMap[any]. A key-collection walk that
// asserts plain map[string]any would silently skip the socket and miss
// inject-key collisions in verbose-form uses. This test constructs the
// unmarshaled shape directly and asserts that collision detection fires
// on it.
func TestGuardSelfInject_VerboseFormUsesInjectKeyCollision_OrderedMapShape(t *testing.T) {
	t.Parallel()
	envMap := types.NewOrderedMap[any]()
	envMap.Set("DB_URL", "host")
	inject := &types.InjectBlock{Env: envMap}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("db", types.VerboseUsesEntry{Target: "my-db", Inject: inject})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})

	selfInject := &types.InjectBlock{Env: map[string]any{"DB_URL": "localhost"}}
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{
		Type:   "core/web-server",
		Inject: selfInject,
		Uses:   uses,
	})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())
	found := false
	for _, e := range errs {
		if errors.Is(e, ErrInjectKeyCollision) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ErrInjectKeyCollision for verbose-form uses with OrderedMap-shaped inject, got %d errors: %v", len(errs), errs)
	}
}

func TestGuardSelfInject_NoInjectNilPasses(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errs))
	}
}

// TestGuardSelfInject_CrossMechanismCollision pins the within-self-inject
// cross-mechanism collision contract. A component declaring the same
// delivery key (DB_URL) on two mechanisms (env and ssm_param) within its
// self-inject block is a contradiction — the runtime cannot deliver the
// same key from two different sources simultaneously. The check is
// symmetric to the rule-3 self-vs-uses collision check; same emission
// shape via ErrInjectKeyCollision.
func TestGuardSelfInject_CrossMechanismCollision(t *testing.T) {
	t.Parallel()

	selfInject := &types.InjectBlock{
		Env: map[string]any{"DB_URL": "literal-string"},
	}
	selfInject.Additional.Set("ssm_param", map[string]any{"DB_URL": "connection.url"})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{
		Type:   "core/api-with-ssm",
		Inject: selfInject,
	})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(normalized(t, agg), selfInjectCatalog())

	// Expect exactly two ErrInjectKeyCollision errors — one per offending
	// mechanism path, mirroring the rule-3 emission granularity.
	collisions := []*ResolverError{}
	for _, e := range errs {
		if errors.Is(e, ErrInjectKeyCollision) {
			collisions = append(collisions, e)
		}
	}
	if len(collisions) != 2 {
		t.Fatalf("expected 2 ErrInjectKeyCollision errors (one per mechanism), got %d: %v", len(collisions), errs)
	}

	// Both colliding mechanism paths must surface — env and ssm_param.
	wantFields := map[string]bool{
		`.spec.subsystems.default.components["api"].inject.env["DB_URL"]`:       false,
		`.spec.subsystems.default.components["api"].inject.ssm_param["DB_URL"]`: false,
	}
	for _, e := range collisions {
		if _, ok := wantFields[e.Field]; ok {
			wantFields[e.Field] = true
		}
	}
	for field, seen := range wantFields {
		if !seen {
			t.Errorf("expected collision diagnostic on field %q, not found in %v", field, collisions)
		}
	}

	// The diagnostic message must name OTHER mechanisms (not the current one).
	// For the env diagnostic the others-slot must contain ssm_param and not
	// env itself; symmetric for the ssm_param diagnostic.
	for _, e := range collisions {
		msg := e.Error()
		switch e.Field {
		case `.spec.subsystems.default.components["api"].inject.env["DB_URL"]`:
			if !strings.Contains(msg, "ssm_param") {
				t.Errorf("env diagnostic missing ssm_param in others slot: %s", msg)
			}
			if strings.Contains(msg, "mechanism(s) [env") {
				t.Errorf("env diagnostic includes self in others slot: %s", msg)
			}
		case `.spec.subsystems.default.components["api"].inject.ssm_param["DB_URL"]`:
			if !strings.Contains(msg, "env") {
				t.Errorf("ssm_param diagnostic missing env in others slot: %s", msg)
			}
			if strings.Contains(msg, "mechanism(s) [ssm_param") {
				t.Errorf("ssm_param diagnostic includes self in others slot: %s", msg)
			}
		}
	}
}

// TestCollectInjectDeliveryKeys_VerboseArrayForm pins the schema-verbose-form
// decode at the resolver-guard's key-collection helper. The verbose form's
// [{in: K, from?: P, value?: V}] elements get keyed by `in:` only — the
// guard cares about delivery-key identity (the inject target name), not
// the value-or-reference distinction. Verbose entries lacking `in:` or
// carrying a non-string `in:` skip.
func TestCollectInjectDeliveryKeys_VerboseArrayForm(t *testing.T) {
	t.Parallel()

	inject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "DB_URL", "from": "connection.url"},
			map[string]any{"in": "PORT", "value": "8080"},
		},
		Arg: []any{
			map[string]any{"in": "FLAG", "value": "--enable"},
		},
	}

	keys := collectInjectDeliveryKeys(inject)
	if len(keys) != 2 {
		t.Fatalf("expected 2 mechanisms, got %d: %v", len(keys), keys)
	}
	if got := keys["env"]; len(got) != 2 || got[0] != "DB_URL" || got[1] != "PORT" {
		t.Errorf("env keys = %v, want [DB_URL PORT] (sorted)", got)
	}
	if got := keys["arg"]; len(got) != 1 || got[0] != "FLAG" {
		t.Errorf("arg keys = %v, want [FLAG]", got)
	}

	emptyInject := &types.InjectBlock{Env: []any{}}
	emptyKeys := collectInjectDeliveryKeys(emptyInject)
	if len(emptyKeys) != 0 {
		t.Errorf("empty []any{} should produce no mechanism keys; got %v", emptyKeys)
	}

	skipInject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "GOOD", "value": "ok"},
			map[string]any{"value": "missing-in"},
			map[string]any{"in": "", "value": "empty-in"},
			"not-a-map",
		},
	}
	skipKeys := collectInjectDeliveryKeys(skipInject)
	if got := skipKeys["env"]; len(got) != 1 || got[0] != "GOOD" {
		t.Errorf("malformed entries should skip; got env=%v want [GOOD]", got)
	}
}
