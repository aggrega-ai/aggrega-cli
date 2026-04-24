// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func cueSchemaCatalog(schema string) CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server/api" {
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/server/api",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
					},
					Spec: types.ComponentSpec{CueSchema: schema},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardCueSchema_ValuesPillarValid(t *testing.T) {
	t.Parallel()
	schema := `values: { port?: int & >=1024 | *8080 }`
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("port", 8080); return _m }()}); return _m }(),
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(schema), nil)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d: %v", len(errs), errs[0].Error())
	}
}

func TestGuardCueSchema_ValuesPillarInvalid(t *testing.T) {
	t.Parallel()
	schema := `values: { port: int & >=1024 }`
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("port", 80); return _m }()}); return _m }(),
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(schema), nil)
	if len(errs) == 0 {
		t.Fatal("expected constraint violation")
	}
	e := errs[0]
	if !errors.Is(e, ErrSchemaValidation) {
		t.Errorf("expected ErrSchemaValidation sentinel, got %v", e.Err)
	}
	if !strings.HasPrefix(e.Field, ".components.api.values") {
		t.Errorf("expected .components.api.values prefix, got %q", e.Field)
	}
	if strings.Contains(e.Error(), ".cue:") {
		t.Errorf("error leaks raw CUE source: %s", e.Error())
	}
}

func TestGuardCueSchema_InfraPillarFromBoundary(t *testing.T) {
	t.Parallel()
	schema := `infra: { replicas: int & >0 }`
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api"}); return _m }(),
		},
	}
	bi := map[string]*BoundaryInfra{
		"api": {Infra: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("replicas", 0); return _m }()}, // violates >0
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(schema), bi)
	if len(errs) == 0 {
		t.Fatal("expected infra validation error")
	}
	if !strings.HasPrefix(errs[0].Field, ".components.api.infra") {
		t.Errorf("expected .components.api.infra prefix, got %q", errs[0].Field)
	}
}

func TestGuardCueSchema_MissingInfraPillarIsNoOp(t *testing.T) {
	t.Parallel()
	// Schema only declares values — arbitrary infra should be allowed.
	schema := `values: { port: int }`
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("port", 8080); return _m }()}); return _m }(),
		},
	}
	bi := map[string]*BoundaryInfra{
		"api": {Infra: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("anything", "goes"); return _m }()},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(schema), bi)
	if len(errs) != 0 {
		t.Errorf("expected no-op for missing infra pillar, got %d: %+v", len(errs), errs)
	}
}

func TestGuardCueSchema_SubsystemRecursion(t *testing.T) {
	t.Parallel()
	schema := `values: { port: int & >=1024 }`
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Subsystems: func() types.OrderedMap[types.Subsystem] { _m := types.OrderedMap[types.Subsystem]{}; _m.Set("frontend", types.Subsystem{
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("ui", types.ComponentInstance{Type: "core/server/api", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("port", 80); return _m }()}); return _m }(),
					Subsystems: func() types.OrderedMap[types.Subsystem] { _m := types.OrderedMap[types.Subsystem]{}; _m.Set("nested", types.Subsystem{
							Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("deep", types.ComponentInstance{Type: "core/server/api", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("port", 443); return _m }()}); return _m }(),
						}); return _m }(),
				}); return _m }(),
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(schema), nil)
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 violations across subsystems, got %d", len(errs))
	}
	// Deterministic sort by instance name: "deep" < "ui"
	if !strings.HasPrefix(errs[0].Field, ".components.deep") {
		t.Errorf("expected deterministic ordering, first error: %q", errs[0].Field)
	}
}

func TestGuardCueSchema_EmptyCueSchemaSkipped(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api"}); return _m }(),
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(""), nil)
	if len(errs) != 0 {
		t.Errorf("expected no-op for empty cueSchema, got %d", len(errs))
	}
}

func TestGuardCueSchema_UnknownTypeSkipped(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/nonexistent"}); return _m }(),
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(`values: {}`), nil)
	if len(errs) != 0 {
		t.Errorf("expected no-op for unknown component type (handled by other guards), got %d", len(errs))
	}
}

// cueSchemaCatalogWithUsesSchema returns a lookup where "core/server/api" has a
// usesSchema declaring "databases" (only) and no explicit uses block in its own
// cueSchema. Generated baseUsesCueSchema enforces socket names via CUE.
func cueSchemaCatalogWithUsesSchema() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server/api" {
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/server/api"},
					Spec: types.ComponentSpec{
						UsesSchema: func() types.OrderedMap[types.UsesSchemaEntry] { _m := types.OrderedMap[types.UsesSchemaEntry]{}; _m.Set("databases", types.UsesSchemaEntry{
								Selector: "component.aggrega.ai/type=stateful-server",
								Inject:   []string{"env"},
							}); return _m }(),
					},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardCueSchema_UsesPillarValidSocket(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api",
					Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", map[string]interface{}{
							"main": map[string]interface{}{"target": "db"},
						}); return _m }(),
				}); return _m }(),
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalogWithUsesSchema(), nil)
	if len(errs) != 0 {
		t.Errorf("expected valid .uses to pass, got %d: %+v", len(errs), errs)
	}
}

// secretsCatalog returns a CatalogLookup where "core/db/postgres" exposes a
// configurable secretsSchema. Each table-driven case swaps the schema inline.
func secretsCatalog(schema types.OrderedMap[types.SecretSlot]) CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/db/postgres" {
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/db/postgres"},
					Spec:     types.ComponentSpec{SecretsSchema: schema},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardCueSchema_SecretsPillarTable(t *testing.T) {
	t.Parallel()

	type tc struct {
		name        string
		schema      types.OrderedMap[types.SecretSlot]
		secrets     types.OrderedMap[types.SecretInstance]
		expectError bool
		errContains string
	}
	cases := []tc{
		{
			name:   "required + blackbox declared passes",
			schema: func() types.OrderedMap[types.SecretSlot] { _m := types.OrderedMap[types.SecretSlot]{}; _m.Set("root_password", types.SecretSlot{Required: true}); return _m }(),
			secrets: func() types.OrderedMap[types.SecretInstance] { _m := types.OrderedMap[types.SecretInstance]{}; _m.Set("root_password", types.SecretInstance{Type: "core/db-password"}); return _m }(),
			expectError: false,
		},
		{
			name:        "required + blackbox missing rejected",
			schema:      func() types.OrderedMap[types.SecretSlot] { _m := types.OrderedMap[types.SecretSlot]{}; _m.Set("root_password", types.SecretSlot{Required: true}); return _m }(),
			secrets:     types.OrderedMap[types.SecretInstance]{}, // omitted
			expectError: true,
			errContains: "root_password",
		},
		{
			name:   "optional + transparent with inject.env declared passes",
			schema: func() types.OrderedMap[types.SecretSlot] { _m := types.OrderedMap[types.SecretSlot]{}; _m.Set("root_password", types.SecretSlot{Required: false, Inject: []string{"env"}}); return _m }(),
			secrets: func() types.OrderedMap[types.SecretInstance] { _m := types.OrderedMap[types.SecretInstance]{}; _m.Set("root_password", types.SecretInstance{
					Type: "core/db-password",
					Inject: &types.InjectBlock{
						Env: map[string]interface{}{"DB_PASSWORD": "v"},
					},
				}); return _m }(),
			expectError: false,
		},
		{
			name:   "blackbox with inject declared rejected",
			schema: func() types.OrderedMap[types.SecretSlot] { _m := types.OrderedMap[types.SecretSlot]{}; _m.Set("root_password", types.SecretSlot{Required: true}); return _m }(),
			secrets: func() types.OrderedMap[types.SecretInstance] { _m := types.OrderedMap[types.SecretInstance]{}; _m.Set("root_password", types.SecretInstance{
					Type: "core/db-password",
					Inject: &types.InjectBlock{
						Env: map[string]interface{}{"FORBIDDEN": "v"},
					},
				}); return _m }(),
			expectError: true,
			errContains: "inject",
		},
		{
			name:   "undeclared secret name rejected",
			schema: func() types.OrderedMap[types.SecretSlot] { _m := types.OrderedMap[types.SecretSlot]{}; _m.Set("root_password", types.SecretSlot{Required: true}); return _m }(),
			secrets: func() types.OrderedMap[types.SecretInstance] { _m := types.OrderedMap[types.SecretInstance]{}; _m.Set("root_password", types.SecretInstance{Type: "core/db-password"}); _m.Set("unknown_secret", types.SecretInstance{Type: "core/other"}); return _m }(),
			expectError: true,
			errContains: "unknown_secret",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			agg := &types.Aggregate{
				Spec: types.AggregateSpec{
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("db", types.ComponentInstance{
							Type:    "core/db/postgres",
							Secrets: c.secrets,
						}); return _m }(),
				},
			}
			errs := guardCueSchema(agg, secretsCatalog(c.schema), nil)
			if c.expectError {
				if len(errs) == 0 {
					t.Fatalf("expected error, got none")
				}
				if !errors.Is(errs[0], ErrSchemaValidation) {
					t.Errorf("expected ErrSchemaValidation, got %v", errs[0].Err)
				}
				combined := ""
				for _, e := range errs {
					combined += e.Error() + " | "
				}
				if c.errContains != "" && !strings.Contains(combined, c.errContains) {
					t.Errorf("errors %q should mention %q", combined, c.errContains)
				}
			} else {
				if len(errs) != 0 {
					t.Errorf("unexpected errors: %v", errs)
				}
			}
		})
	}
}

func TestGuardCueSchema_UsesPillarUnknownSocketRejected(t *testing.T) {
	t.Parallel()
	// usesSchema declares only "databases"; aggregate references an undeclared "caches" socket.
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api",
					Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("caches", map[string]interface{}{
							"ephemeral": map[string]interface{}{"target": "redis"},
						}); return _m }(),
				}); return _m }(),
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalogWithUsesSchema(), nil)
	if len(errs) == 0 {
		t.Fatal("expected unknown socket to be rejected by generated base CUE")
	}
	if !errors.Is(errs[0], ErrSchemaValidation) {
		t.Errorf("expected ErrSchemaValidation, got %v", errs[0].Err)
	}
	if !strings.HasPrefix(errs[0].Field, ".components.api.uses") {
		t.Errorf("expected .components.api.uses prefix, got %q", errs[0].Field)
	}
}
