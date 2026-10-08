// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// TestOptional_InTemplateContext verifies that optional is registered in the
// shared function set and that EvalTemplate (the dominant value-template
// surface) renders correctly when authoring templates use optional.
func TestOptional_InTemplateContext(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		data any
		want string
	}{
		{
			name: "absent infra.replicas falls back to 1",
			tmpl: `{{ optional .infra "replicas" 1 }}`,
			data: map[string]any{"infra": map[string]any{}},
			want: "1",
		},
		{
			name: "authored infra.replicas:0 returns 0 (scale-to-zero)",
			tmpl: `{{ optional .infra "replicas" 1 }}`,
			data: map[string]any{"infra": map[string]any{"replicas": 0}},
			want: "0",
		},
		{
			name: "authored infra.replicas:3 returns 3",
			tmpl: `{{ optional .infra "replicas" 1 }}`,
			data: map[string]any{"infra": map[string]any{"replicas": 3}},
			want: "3",
		},
		{
			name: "boolean fallback path on absent feature flag",
			tmpl: `{{ optional .values "enabled" true }}`,
			data: map[string]any{"values": map[string]any{}},
			want: "true",
		},
		{
			name: "authored false on feature flag returns false (not fallback)",
			tmpl: `{{ optional .values "enabled" true }}`,
			data: map[string]any{"values": map[string]any{"enabled": false}},
			want: "false",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalTemplate("optional-test", tc.tmpl, tc.data, redact.Keys{})
			if err != nil {
				t.Fatalf("EvalTemplate returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("EvalTemplate(%q) = %q; want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

// TestOptional_PathTemplatesShareTheOneFuncSet pins that a path template reaches
// the same function set every other renderer surface does, optional included:
// every template-bearing surface runs through one evaluator, so a per-surface
// function set would be a second place for the surfaces to disagree.
//
// optional is the one spelling that supplies a fallback for an absent key on this
// surface. Under missingkey=error a bare reference to an absent key halts, and so
// does reading it through index: index of a map key the map does not hold halts
// naming the key whatever the surface's missing-key policy, so index piped through
// default never reaches the default. The second and third arms below hold both
// halts, so optional stays the explicit, presence-testing read.
func TestOptional_PathTemplatesShareTheOneFuncSet(t *testing.T) {
	data := PathTemplateContext{Parameters: map[string]any{}}
	got, err := EvalPathTemplate("path-test", "directory", "some-boundary",
		`{{ optional .parameters "x" "y" }}`, data)
	if err != nil {
		t.Fatalf("optional must be reachable from a path template: %v", err)
	}
	if got != "y" {
		t.Errorf("optional fallback = %q; want y", got)
	}
	_, err = EvalPathTemplate("path-test", "directory", "some-boundary",
		`{{ index .parameters "x" | default "y" }}`, data)
	if err == nil || !errors.Is(err, gotmpl.ErrIndexOfAbsentMapKey()) {
		t.Errorf("index of an absent parameter key piped through default must halt naming the key; got %v", err)
	}
	// A bare reference to the same absent key still halts, so the surface's
	// missing-key strictness is intact and only the explicit fallback is exempt.
	if _, err := EvalPathTemplate("path-test", "directory", "some-boundary",
		`{{ .parameters.x }}`, data); err == nil {
		t.Error("a bare reference to an absent parameter key must still halt under missingkey=error")
	}
}
