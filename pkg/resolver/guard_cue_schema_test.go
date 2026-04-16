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
						Labels: map[string]string{types.ComponentTypeLabel: "server"},
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
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api", Values: map[string]interface{}{"port": 8080}},
			},
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
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api", Values: map[string]interface{}{"port": 80}},
			},
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
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api"},
			},
		},
	}
	bi := map[string]*BoundaryInfra{
		"api": {Infra: map[string]interface{}{"replicas": 0}}, // violates >0
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
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api", Values: map[string]interface{}{"port": 8080}},
			},
		},
	}
	bi := map[string]*BoundaryInfra{
		"api": {Infra: map[string]interface{}{"anything": "goes"}},
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
			Subsystems: map[string]types.Subsystem{
				"frontend": {
					Components: map[string]types.ComponentInstance{
						"ui": {Type: "core/server/api", Values: map[string]interface{}{"port": 80}}, // violates
					},
					Subsystems: map[string]types.Subsystem{
						"nested": {
							Components: map[string]types.ComponentInstance{
								"deep": {Type: "core/server/api", Values: map[string]interface{}{"port": 443}}, // also violates
							},
						},
					},
				},
			},
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
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api"},
			},
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
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/nonexistent"},
			},
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
						UsesSchema: map[string]types.UsesSchemaEntry{
							"databases": {
								Selector: "component.aggrega.ai/type=stateful-server",
								Inject:   []string{"env"},
							},
						},
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
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api",
					Uses: map[string]interface{}{
						"databases": map[string]interface{}{
							"main": map[string]interface{}{"target": "db"},
						},
					},
				},
			},
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalogWithUsesSchema(), nil)
	if len(errs) != 0 {
		t.Errorf("expected valid .uses to pass, got %d: %+v", len(errs), errs)
	}
}

// secretsCatalog returns a CatalogLookup where "core/db/postgres" exposes a
// configurable secretsSchema. Each table-driven case swaps the schema inline.
func secretsCatalog(schema map[string]types.SecretSlot) CatalogLookup {
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
		schema      map[string]types.SecretSlot
		secrets     map[string]types.SecretInstance
		expectError bool
		errContains string
	}
	cases := []tc{
		{
			name:   "required + blackbox declared passes",
			schema: map[string]types.SecretSlot{"root_password": {Required: true}},
			secrets: map[string]types.SecretInstance{
				"root_password": {Type: "core/db-password"},
			},
			expectError: false,
		},
		{
			name:        "required + blackbox missing rejected",
			schema:      map[string]types.SecretSlot{"root_password": {Required: true}},
			secrets:     map[string]types.SecretInstance{}, // omitted
			expectError: true,
			errContains: "root_password",
		},
		{
			name:   "optional + transparent with inject.env declared passes",
			schema: map[string]types.SecretSlot{"root_password": {Required: false, Inject: []string{"env"}}},
			secrets: map[string]types.SecretInstance{
				"root_password": {
					Type: "core/db-password",
					Inject: &types.InjectBlock{
						Env: map[string]interface{}{"DB_PASSWORD": "v"},
					},
				},
			},
			expectError: false,
		},
		{
			name:   "blackbox with inject declared rejected",
			schema: map[string]types.SecretSlot{"root_password": {Required: true}},
			secrets: map[string]types.SecretInstance{
				"root_password": {
					Type: "core/db-password",
					Inject: &types.InjectBlock{
						Env: map[string]interface{}{"FORBIDDEN": "v"},
					},
				},
			},
			expectError: true,
			errContains: "inject",
		},
		{
			name:   "undeclared secret name rejected",
			schema: map[string]types.SecretSlot{"root_password": {Required: true}},
			secrets: map[string]types.SecretInstance{
				"root_password":   {Type: "core/db-password"},
				"unknown_secret":  {Type: "core/other"},
			},
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
					Components: map[string]types.ComponentInstance{
						"db": {
							Type:    "core/db/postgres",
							Secrets: c.secrets,
						},
					},
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
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api",
					Uses: map[string]interface{}{
						"caches": map[string]interface{}{
							"ephemeral": map[string]interface{}{"target": "redis"},
						},
					},
				},
			},
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
