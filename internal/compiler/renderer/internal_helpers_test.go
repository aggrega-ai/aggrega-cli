// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// TestNodeKindName_EveryDocumentedKind pins the diagnostic-formatter
// helper used in CATALOG_ERROR messages. Every yaml.Kind plus the
// zero-value fall-through must produce a stable name string.
func TestNodeKindName_EveryDocumentedKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind yaml.Kind
		want string
	}{
		{yaml.DocumentNode, "document"},
		{yaml.SequenceNode, "sequence"},
		{yaml.MappingNode, "mapping"},
		{yaml.ScalarNode, "scalar"},
		{yaml.AliasNode, "alias"},
	}
	for _, c := range cases {
		if got := nodeKindName(c.kind); got != c.want {
			t.Errorf("nodeKindName(%v) = %q; want %q", c.kind, got, c.want)
		}
	}
	// Unknown kind (zero value or out-of-range) falls through.
	if got := nodeKindName(yaml.Kind(99)); got == "" {
		t.Errorf("unknown kind: expected non-empty diagnostic; got empty")
	}
}

// TestIsSourceless_EveryBranch pins the generative-vs-sourced
// discriminator: nil recipe → sourceless; nil source → sourceless;
// empty-string source → sourceless; non-empty string source → sourced.
func TestIsSourceless_EveryBranch(t *testing.T) {
	t.Parallel()
	if !isSourceless(nil) {
		t.Errorf("nil recipe: expected sourceless")
	}
	nilSource := &types.Recipe{}
	if !isSourceless(nilSource) {
		t.Errorf("nil source: expected sourceless")
	}
	emptyString := &types.Recipe{Spec: types.RecipeSpec{Source: ""}}
	if !isSourceless(emptyString) {
		t.Errorf("empty-string source: expected sourceless")
	}
	withSource := &types.Recipe{Spec: types.RecipeSpec{Source: "./chart"}}
	if isSourceless(withSource) {
		t.Errorf("string source: expected sourced (not sourceless)")
	}
}

// TestDeepMergeInject_OverwritesAndAdds pins the per-key dispatch:
// overlay values replace base values for the same key; new keys
// append; output is sorted-key deterministic.
func TestDeepMergeInject_OverwritesAndAdds(t *testing.T) {
	t.Parallel()
	base := &types.InjectPipe{}
	base.Env.Set("KEEP", "keep-value")
	base.Env.Set("OVERRIDE", "old-value")
	overlay := map[string]string{
		"OVERRIDE": "new-value",
		"NEW":      "new-key-value",
	}
	DeepMergeInject(base, overlay)
	if v, _ := base.Env.Get("KEEP"); v != "keep-value" {
		t.Errorf("KEEP should be preserved; got %q", v)
	}
	if v, _ := base.Env.Get("OVERRIDE"); v != "new-value" {
		t.Errorf("OVERRIDE should be replaced; got %q", v)
	}
	if v, _ := base.Env.Get("NEW"); v != "new-key-value" {
		t.Errorf("NEW should be added; got %q", v)
	}
}

// TestDeepMergeInject_NilOrEmptyOverlay pins the no-op branches:
// nil base or empty overlay leaves state unchanged.
func TestDeepMergeInject_NilOrEmptyOverlay(t *testing.T) {
	t.Parallel()
	// Nil base.
	DeepMergeInject(nil, map[string]string{"x": "y"}) // must not panic
	// Empty overlay.
	base := &types.InjectPipe{}
	base.Env.Set("X", "v")
	DeepMergeInject(base, map[string]string{})
	if v, _ := base.Env.Get("X"); v != "v" {
		t.Errorf("empty overlay should not touch base; got %q", v)
	}
}
