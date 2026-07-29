// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"go.yaml.in/yaml/v3"
)

// componentWithProvides builds a catalog Component whose spec.schema declares
// the given provides keys (each a string-typed compact-form property).
func componentWithProvides(t *testing.T, keys ...string) types.Component {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("provides:\n")
	if len(keys) == 0 {
		sb.WriteString("  {}\n")
	}
	for _, k := range keys {
		sb.WriteString("  " + k + ":\n    type: string\n")
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(sb.String()), &node); err != nil {
		t.Fatalf("building provides schema: %v", err)
	}
	comp := types.Component{}
	comp.Spec.Schema = node
	return comp
}

// componentNoSchema builds a catalog Component with no spec.schema at all.
func componentNoSchema() types.Component {
	return types.Component{}
}

// TestGuardInjectKeys_NilInputsReturnNil verifies boundary behavior.
func TestGuardInjectKeys_NilInputsReturnNil(t *testing.T) {
	t.Parallel()
	if errs := guardInjectKeys(normalized(t, nil), CatalogLookup{}); errs != nil {
		t.Errorf("expected nil errors on nil agg; got %v", errs)
	}
	agg := &types.Aggregate{}
	if errs := guardInjectKeys(normalized(t, agg), CatalogLookup{Component: nil}); errs != nil {
		t.Errorf("expected nil errors on nil catalog.Component; got %v", errs)
	}
}

// TestGuardInjectKeys_NoInjectIsClean verifies that components without
// inject blocks produce no errors regardless of target schema.
func TestGuardInjectKeys_NoInjectIsClean(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/api-server:1.0.0"})

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return componentWithProvides(t, "host"), true
		},
	}

	if errs := guardInjectKeys(normalized(t, agg), catalog); errs != nil {
		t.Errorf("expected nil errors; got %v", errs)
	}
}

// TestGuardInjectKeys_SelfInjectFromTypoHalts verifies a self-inject verbose
// from: reference to a key absent from the host's own provides halts with
// ErrInvalidInjectKey and lists the declared keys.
func TestGuardInjectKeys_SelfInjectFromTypoHalts(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}

	inst := types.ComponentInstance{Type: "core/db:1.0.0"}
	// Verbose from: form — DB_HOST resolves from own provides key "hots" (typo).
	inst.Inject = &types.InjectBlock{
		Env: map[string]any{"DB_HOST": map[string]any{"from": "hots"}},
	}
	agg.Spec.Components.Set("db", inst)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return componentWithProvides(t, "host", "port"), true
		},
	}

	errs := guardInjectKeys(normalized(t, agg), catalog)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %d (%v)", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrInvalidInjectKey) {
		t.Errorf("expected ErrInvalidInjectKey; got %v", errs[0].Err)
	}
	if !strings.Contains(errs[0].Message, "host") || !strings.Contains(errs[0].Message, "port") {
		t.Errorf("expected declared-keys list in message; got %q", errs[0].Message)
	}
	if !strings.Contains(errs[0].Message, `"hots"`) {
		t.Errorf("expected offending key in message; got %q", errs[0].Message)
	}
}

// TestGuardInjectKeys_SelfInjectValidFromPasses verifies a self-inject from:
// reference to a declared provides key produces no error, and that a compact
// string literal (not a from: reference) is never validated.
func TestGuardInjectKeys_SelfInjectValidFromPasses(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}

	inst := types.ComponentInstance{Type: "core/db:1.0.0"}
	inst.Inject = &types.InjectBlock{
		// Valid from: reference plus a compact literal — the literal "anything"
		// is not a provides-key reference and must not be validated.
		Env: map[string]any{
			"DB_HOST":   map[string]any{"from": "host"},
			"DB_STATIC": "anything",
		},
	}
	agg.Spec.Components.Set("db", inst)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return componentWithProvides(t, "host", "port"), true
		},
	}

	if errs := guardInjectKeys(normalized(t, agg), catalog); errs != nil {
		t.Errorf("expected nil errors; got %v", errs)
	}
}

// TestGuardInjectKeys_InheritedProvidesAccepted is the extends-inheritance proof:
// a from: reference to a provides key the BASE declared, which the extending
// Component never authored, resolves rather than halting — because the merge
// copied the base's pillar block onto the extending Component, so its authored
// provides set already carries the inherited key.
//
// The fixture runs the real merge rather than stubbing a second view of the
// Component's schema. A stub would pass whether or not inheritance works.
func TestGuardInjectKeys_InheritedProvidesAccepted(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	inst := types.ComponentInstance{Type: "core/db:1.0.0"}
	inst.Inject = &types.InjectBlock{
		Env: map[string]any{"DB_HOST": map[string]any{"from": "inherited_host"}},
	}
	agg.Spec.Components.Set("db", inst)

	// The base declares both provides keys; the extending Component declares the
	// marker and nothing else.
	base := componentWithProvides(t, "host", "inherited_host")
	base.Metadata.Name = "core/db-base"
	var marker yaml.Node
	if err := yaml.Unmarshal([]byte(validator.InheritedSchemaMarker), &marker); err != nil {
		t.Fatalf("marker parse: %v", err)
	}
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/db", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/db-base", Schema: marker},
	}
	resolved, rerrs := validator.ResolveComponentExtends(child, func(uri string) (types.Component, bool) {
		if uri == "core/db-base" {
			return base, true
		}
		return types.Component{}, false
	})
	if len(rerrs) > 0 {
		t.Fatalf("ResolveComponentExtends: %v", rerrs)
	}

	catalog := CatalogLookup{
		Component: func(string) (types.Component, bool) { return resolved, true },
	}

	if errs := guardInjectKeys(normalized(t, agg), catalog); errs != nil {
		t.Errorf("inherited provides key must resolve off the extending Component's schema; got %v", errs)
	}
}

// TestGuardInjectKeys_UsesCrossInjectTypoHalts verifies a uses-level inject
// reference to a key absent from the used target's provides halts.
func TestGuardInjectKeys_UsesCrossInjectTypoHalts(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}

	// Target instance "database" of type core/postgres:1.0.0 declares host, port.
	agg.Spec.Components.Set("database", types.ComponentInstance{Type: "core/postgres:1.0.0"})

	// Consumer "web" uses database, injecting from the target's provides.
	web := types.ComponentInstance{Type: "core/web:1.0.0"}
	web.Uses = types.OrderedMap[types.UsesSocketValue]{}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("db", types.VerboseUsesEntry{
		Target: "database",
		Inject: &types.InjectBlock{Env: map[string]any{"DB_HOST": "hots"}}, // typo
	})
	web.Uses.Set("data", types.UsesSocketValue{Verbose: verbose})
	agg.Spec.Components.Set("web", web)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/postgres:1.0.0":
				return componentWithProvides(t, "host", "port"), true
			default:
				return componentWithProvides(t), true // web declares no provides
			}
		},
	}

	errs := guardInjectKeys(normalized(t, agg), catalog)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %d (%v)", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrInvalidInjectKey) {
		t.Errorf("expected ErrInvalidInjectKey; got %v", errs[0].Err)
	}
	if !strings.Contains(errs[0].Field, ".uses[\"data\"][\"db\"].inject") {
		t.Errorf("expected uses-path field locator; got %q", errs[0].Field)
	}
	if !strings.Contains(errs[0].Message, "core/postgres:1.0.0") {
		t.Errorf("expected target type URI in message; got %q", errs[0].Message)
	}
	if !strings.Contains(errs[0].Message, "host") || !strings.Contains(errs[0].Message, "port") {
		t.Errorf("expected declared-keys list; got %q", errs[0].Message)
	}
}

// TestGuardInjectKeys_UsesCrossInjectValidPasses verifies a valid uses-level
// inject reference to a declared target provides key produces no error.
func TestGuardInjectKeys_UsesCrossInjectValidPasses(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	agg.Spec.Components.Set("database", types.ComponentInstance{Type: "core/postgres:1.0.0"})

	web := types.ComponentInstance{Type: "core/web:1.0.0"}
	web.Uses = types.OrderedMap[types.UsesSocketValue]{}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("db", types.VerboseUsesEntry{
		Target: "database",
		Inject: &types.InjectBlock{Env: map[string]any{"DB_HOST": "host", "DB_PORT": "port"}},
	})
	web.Uses.Set("data", types.UsesSocketValue{Verbose: verbose})
	agg.Spec.Components.Set("web", web)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/postgres:1.0.0" {
				return componentWithProvides(t, "host", "port"), true
			}
			return componentWithProvides(t), true
		},
	}

	if errs := guardInjectKeys(normalized(t, agg), catalog); errs != nil {
		t.Errorf("expected nil errors; got %v", errs)
	}
}

// TestGuardInjectKeys_CustomMechanismCovered verifies a typo on a custom
// inject mechanism (InjectBlock.Additional) is caught on both surfaces.
func TestGuardInjectKeys_CustomMechanismCovered(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	agg.Spec.Components.Set("database", types.ComponentInstance{Type: "core/postgres:1.0.0"})

	web := types.ComponentInstance{Type: "core/web:1.0.0"}
	web.Uses = types.OrderedMap[types.UsesSocketValue]{}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	usesInject := &types.InjectBlock{}
	usesInject.Additional.Set("ssm_param", map[string]any{"DB_URL": "hots"}) // typo on custom mechanism
	verbose.Set("db", types.VerboseUsesEntry{Target: "database", Inject: usesInject})
	web.Uses.Set("data", types.UsesSocketValue{Verbose: verbose})
	agg.Spec.Components.Set("web", web)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/postgres:1.0.0" {
				return componentWithProvides(t, "host"), true
			}
			return componentWithProvides(t), true
		},
	}

	errs := guardInjectKeys(normalized(t, agg), catalog)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %d (%v)", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrInvalidInjectKey) {
		t.Errorf("expected ErrInvalidInjectKey; got %v", errs[0].Err)
	}
	if !strings.Contains(errs[0].Field, "ssm_param") {
		t.Errorf("expected custom mechanism in field locator; got %q", errs[0].Field)
	}
}

// TestGuardInjectKeys_NoProvidesRejectsEveryFrom verifies a target Component
// declaring no provides rejects every from: reference targeting it.
func TestGuardInjectKeys_NoProvidesRejectsEveryFrom(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}

	inst := types.ComponentInstance{Type: "core/opaque:1.0.0"}
	inst.Inject = &types.InjectBlock{
		Env: map[string]any{
			"A": map[string]any{"from": "host"},
			"B": map[string]any{"from": "port"},
		},
	}
	agg.Spec.Components.Set("svc", inst)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return componentNoSchema(), true // no schema → no provides
		},
	}

	errs := guardInjectKeys(normalized(t, agg), catalog)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors (every from: rejected); got %d (%v)", len(errs), errs)
	}
	for _, e := range errs {
		if !errors.Is(e, ErrInvalidInjectKey) {
			t.Errorf("expected ErrInvalidInjectKey; got %v", e.Err)
		}
		if !strings.Contains(e.Message, "no provides keys") {
			t.Errorf("expected empty-provides phrasing; got %q", e.Message)
		}
	}
}

// TestGuardInjectKeys_UnknownTargetTypeSkipped verifies that a uses target
// whose type is a genuinely-non-catalog Tier-1 relative-path reference is
// skipped (no catalog entity to validate against), and so self-inject is not
// flagged. The halt arm (a bare catalog ref absent from the view) is in
// guard_view_consistency_test.go.
func TestGuardInjectKeys_RelativePathTargetTypeSkipped(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "app"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	inst := types.ComponentInstance{Type: "./local-variant"}
	inst.Inject = &types.InjectBlock{Env: map[string]any{"X": map[string]any{"from": "nope"}}}
	agg.Spec.Components.Set("svc", inst)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return types.Component{}, false // not found
		},
	}

	if errs := guardInjectKeys(normalized(t, agg), catalog); errs != nil {
		t.Errorf("expected nil errors for relative-path (non-catalog) type; got %v", errs)
	}
}
