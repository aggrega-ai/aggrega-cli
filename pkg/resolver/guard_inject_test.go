// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// injectTargetCatalog returns a lookup where "core/db" has a cueSchema declaring
// `provides: { host, port }` — the target most tests validate inject from-keys against.
func injectTargetCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/db" {
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/db", Version: "1.0.0"},
					Spec: types.ComponentSpec{
						CueSchema: `provides: { host: string, port: string }`,
					},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardInjectKeys_TypoFromKey(t *testing.T) {
	t.Parallel()
	features :=
		// Feature attached to the db component has inject.env.DB_HOST from "hots" — typo for "host".
		types.OrderedMap[types.FeatureInstance]{}
	features.Set("telemetry", types.FeatureInstance{Inject: &types.InjectBlock{Env: map[string]any{"DB_HOST": "hots"}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/db", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "typo-test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardInjectKeys(agg, injectTargetCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	e := errs[0]
	if !errors.Is(e, ErrInvalidInjectKey) {
		t.Errorf("expected ErrInvalidInjectKey, got %v", e.Err)
	}
	if !strings.Contains(e.Message, "hots") {
		t.Errorf("error should name the bad from-key, got: %s", e.Message)
	}
	if !strings.Contains(e.Message, "host") || !strings.Contains(e.Message, "port") {
		t.Errorf("error should suggest valid keys (host, port), got: %s", e.Message)
	}
	if !strings.Contains(e.Field, "DB_HOST") {
		t.Errorf("field should locate the offending local key, got: %s", e.Field)
	}
}

func TestGuardInjectKeys_ValidFromKey(t *testing.T) {
	t.Parallel()
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("telemetry", types.FeatureInstance{Inject: &types.InjectBlock{Env: map[string]any{"DB_HOST": "host", "DB_PORT": "port"}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/db", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "ok-test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	if errs := guardInjectKeys(agg, injectTargetCatalog()); len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
}

func TestGuardInjectKeys_VerboseForm(t *testing.T) {
	t.Parallel()
	features :=
		// Verbose form: DB_URL has {from: "hots"} — typo through the verbose path.
		types.OrderedMap[types.FeatureInstance]{}
	features.Set("telemetry", types.FeatureInstance{Inject: &types.InjectBlock{Env: map[string]any{"DB_URL": map[string]any{"from": "hots"}}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/db", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "verbose-test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardInjectKeys(agg, injectTargetCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrInvalidInjectKey) {
		t.Errorf("expected ErrInvalidInjectKey")
	}
}

func TestGuardInjectKeys_VerboseLiteralSkipped(t *testing.T) {
	t.Parallel()
	features :=
		// Verbose literal: only "value" set (no "from") — this is a literal inject,
		// not a provides reference. Validator should skip it.
		types.OrderedMap[types.FeatureInstance]{}
	features.Set("telemetry", types.FeatureInstance{Inject: &types.InjectBlock{Env: map[string]any{"DB_TAG": map[string]any{"value": "production"}}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/db", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "literal-test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	if errs := guardInjectKeys(agg, injectTargetCatalog()); len(errs) != 0 {
		t.Errorf("expected no errors for literal inject, got %d: %v", len(errs), errs)
	}
}

func TestGuardInjectKeys_NoCueSchema(t *testing.T) {
	t.Parallel()
	// Target has no cueSchema → validator can't check; no errors emitted.
	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return types.Component{Metadata: types.ComponentMetadata{Name: "core/db"}}, true
		},
		Feature: func(typeURI string) (types.Feature, bool) { return types.Feature{}, false },
	}
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("f", types.FeatureInstance{Inject: &types.InjectBlock{Env: map[string]any{"K": "anything"}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/db", Features: features})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	if errs := guardInjectKeys(agg, catalog); len(errs) != 0 {
		t.Errorf("no-cueSchema target should produce no errors, got %d", len(errs))
	}
}

func TestGuardInjectKeys_NoProvidesPillar(t *testing.T) {
	t.Parallel()
	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return types.Component{
				Metadata: types.ComponentMetadata{Name: "core/db"},
				Spec:     types.ComponentSpec{CueSchema: `values: { port: int }`}, // no provides
			}, true
		},
		Feature: func(typeURI string) (types.Feature, bool) { return types.Feature{}, false },
	}
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("f", types.FeatureInstance{Inject: &types.InjectBlock{Env: map[string]any{"K": "anything"}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/db", Features: features})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	if errs := guardInjectKeys(agg, catalog); len(errs) != 0 {
		t.Errorf("no-provides target should produce no errors, got %d", len(errs))
	}
}

func TestGuardInjectKeys_SecretInjectAlsoValidated(t *testing.T) {
	t.Parallel()
	secrets :=
		// Same typo via a secret inject block instead of a feature.
		types.OrderedMap[types.SecretInstance]{}
	secrets.Set("root_password", types.SecretInstance{Inject: &types.InjectBlock{Env: map[string]any{"PW": "hots"}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/db", Secrets: secrets})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "secret-test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardInjectKeys(agg, injectTargetCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error from secret inject, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Field, "secrets") {
		t.Errorf("expected field path to mention secrets, got %s", errs[0].Field)
	}
}
