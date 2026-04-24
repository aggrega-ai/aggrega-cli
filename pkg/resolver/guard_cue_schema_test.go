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
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/server/api",
						Labels: labels,
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
	values := types.OrderedMap[interface{}]{}
	values.Set("port", 8080)
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Values: values})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
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
	values := types.OrderedMap[interface{}]{}
	values.Set("port", 80)
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Values: values})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
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
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api"})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	infra := types.OrderedMap[interface{}]{}
	infra.Set("replicas", 0)
	bi := map[string]*BoundaryInfra{
		"api": {Infra: infra}, // violates >0
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
	values := types.OrderedMap[interface{}]{}
	values.Set("port", 8080)
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Values: values})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	infra := types.OrderedMap[interface{}]{}
	infra.Set("anything", "goes")
	bi := map[string]*BoundaryInfra{
		"api": {Infra: infra},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(schema), bi)
	if len(errs) != 0 {
		t.Errorf("expected no-op for missing infra pillar, got %d: %+v", len(errs), errs)
	}
}

func TestGuardCueSchema_SubsystemRecursion(t *testing.T) {
	t.Parallel()
	schema := `values: { port: int & >=1024 }`
	values2 := types.OrderedMap[interface{}]{}
	values2.Set("port", 443)
	values := types.OrderedMap[interface{}]{}
	values.Set("port", 80)
	components2 := types.OrderedMap[types.ComponentInstance]{}
	components2.Set("deep", types.ComponentInstance{Type: "core/server/api", Values: values2})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("ui", types.ComponentInstance{Type: "core/server/api", Values: values})
	subsystems2 := types.OrderedMap[types.Subsystem]{}
	subsystems2.Set("nested", types.Subsystem{Components: components2})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("frontend", types.Subsystem{Components: components, Subsystems: subsystems2})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
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
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api"})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardCueSchema(agg, cueSchemaCatalog(""), nil)
	if len(errs) != 0 {
		t.Errorf("expected no-op for empty cueSchema, got %d", len(errs))
	}
}

func TestGuardCueSchema_UnknownTypeSkipped(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/nonexistent"})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
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
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server", Inject: []string{"env"}})
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/server/api"},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema,
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
	uses := types.OrderedMap[interface{}]{}
	uses.Set("databases", map[string]interface{}{"main": map[string]interface{}{"target": "db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
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
	schema2 := types.OrderedMap[types.SecretSlot]{}
	schema2.Set("root_password", types.SecretSlot{Required: true})
	secrets2 := types.OrderedMap[types.SecretInstance]{}
	secrets2.Set("root_password", types.SecretInstance{Type: "core/db-password"})
	schema3 := types.OrderedMap[types.SecretSlot]{}
	schema3.Set("root_password", types.SecretSlot{Required: true})
	schema4 := types.OrderedMap[types.SecretSlot]{}
	schema4.Set("root_password", types.SecretSlot{Required: false, Inject:

	// omitted

	[]string{"env"}})
	secrets3 := types.OrderedMap[types.SecretInstance]{}
	secrets3.Set("root_password", types.SecretInstance{Type: "core/db-password", Inject: &types.InjectBlock{Env: map[string]interface{}{"DB_PASSWORD": "v"}}})
	schema5 := types.OrderedMap[types.SecretSlot]{}
	schema5.Set("root_password", types.SecretSlot{Required: true})
	secrets4 := types.OrderedMap[types.SecretInstance]{}
	secrets4.Set("root_password", types.SecretInstance{Type: "core/db-password", Inject: &types.InjectBlock{Env: map[string]interface{}{"FORBIDDEN": "v"}}})
	schema6 := types.OrderedMap[types.SecretSlot]{}
	schema6.Set("root_password", types.SecretSlot{Required: true})
	secrets5 := types.OrderedMap[types.SecretInstance]{}
	secrets5.Set("root_password", types.SecretInstance{Type: "core/db-password"})
	secrets5.Set("unknown_secret", types.SecretInstance{Type: "core/other"})
	cases := []tc{
		{
			name:        "required + blackbox declared passes",
			schema:      schema2,
			secrets:     secrets2,
			expectError: false,
		},
		{
			name:        "required + blackbox missing rejected",
			schema:      schema3,
			secrets:     types.OrderedMap[types.SecretInstance]{},
			expectError: true,
			errContains: "root_password",
		},
		{
			name:    "optional + transparent with inject.env declared passes",
			schema:  schema4,
			secrets: secrets3,

			expectError: false,
		},
		{
			name:    "blackbox with inject declared rejected",
			schema:  schema5,
			secrets: secrets4,

			expectError: true,
			errContains: "inject",
		},
		{
			name:        "undeclared secret name rejected",
			schema:      schema6,
			secrets:     secrets5,
			expectError: true,
			errContains: "unknown_secret",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			components := types.OrderedMap[types.ComponentInstance]{}
			components.Set("db", types.ComponentInstance{Type: "core/db/postgres", Secrets: c.secrets})
			agg := &types.Aggregate{
				Spec: types.AggregateSpec{
					Components: components,
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
	uses :=
		// usesSchema declares only "databases"; aggregate references an undeclared "caches" socket.
		types.OrderedMap[interface{}]{}
	uses.Set("caches", map[string]interface{}{"ephemeral": map[string]interface{}{"target": "redis"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
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
