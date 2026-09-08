// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// settingsPolicyInputs clones the fullstack inputs and attaches the
// typed-settings policy fixture (which declares a settings_schema requiring an
// integer max_replicas) at the policy socket with the given settings object.
func settingsPolicyInputs(t *testing.T, settings map[string]any) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-settings",
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{Settings: settings},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-settings": "policy-settings-rust.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// TestPolicySocketSettingsConformE2E proves a conforming with.settings passes the
// host's schema validation, the build succeeds, and the guest read the typed
// value: its info verdict echoes max_replicas, proving the setting flowed
// through as the declared integer.
func TestPolicySocketSettingsConformE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := settingsPolicyInputs(t, map[string]any{"max_replicas": 5})
		r, err := runPipelineCompleted(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("conforming settings: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("conforming-settings build produced no artifacts")
	}
	if !strings.Contains(stderr, "typed settings applied: max_replicas=5") {
		t.Errorf("guest did not read the typed setting; stderr:\n%s", stderr)
	}
}

// TestPolicySocketSettingsViolationE2E proves a with.settings that does not
// satisfy the plugin's declared settings schema halts the build loudly BEFORE the
// plugin is dispatched, carrying ErrPolicySettingsInvalid and naming the
// offending key.
func TestPolicySocketSettingsViolationE2E(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		settings map[string]any
		wantKey  string // a token the halt message must carry, naming the offending key
	}{
		{"wrong type", map[string]any{"max_replicas": "five"}, "max_replicas"},
		{"missing required", map[string]any{"replicas": 5}, "max_replicas"},
		{"unknown key", map[string]any{"max_replicas": 5, "bogus": true}, "bogus"},
		{"below minimum", map[string]any{"max_replicas": 0}, "max_replicas"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputs, buildLookup, recipeLookup, subAgg := settingsPolicyInputs(t, tc.settings)
			_, err := runPipelineCompleted(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
			if err == nil {
				t.Fatalf("violating settings %v: expected a halt, got nil", tc.settings)
			}
			if !errors.Is(err, pipeline.ErrPolicySettingsInvalid()) {
				t.Fatalf("halt not errors.Is ErrPolicySettingsInvalid: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("halt message does not name the offending key %q:\n%v", tc.wantKey, err)
			}
			// The violation must be a settings fault, never a policy verdict: a
			// guest that never ran cannot have rejected the build.
			if strings.Contains(err.Error(), "rejected the build") {
				t.Errorf("settings fault surfaced as a policy verdict:\n%v", err)
			}
		})
	}
}

// TestPolicySocketNoSettingsSchemaFreeFormE2E proves a plugin that declares NO
// settings_schema keeps the free-form pass-through: arbitrary settings do not
// halt the build (non-regression). The all-pass policy fixture declares no
// schema, so any settings object reaches it unvalidated and the build succeeds.
func TestPolicySocketNoSettingsSchemaFreeFormE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-allow",
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{Settings: map[string]any{
			"anything": "goes",
			"nested":   map[string]any{"count": 3},
		}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-allow": "policy-allow-go.wasm",
	})

	res, err := runPipelineCompleted(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("no-schema guest with arbitrary settings: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("free-form-settings build produced no artifacts")
	}
}
