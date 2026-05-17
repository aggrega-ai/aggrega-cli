// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the URI shorthand expander. Coverage is split into three tables
// matching the source work item's acceptance criteria:
//
//   - TestParseRecipeURI_specExamples carries the five canonical shorthand
//     examples drawn from the spec (three component shorthands routing to
//     the official "aggrega" namespace, one feature shorthand to the same,
//     one feature shorthand to a third-party "lakera" namespace). These
//     are the rows that pin the Scenario A / Scenario B routing decision.
//
//   - TestParseRecipeURI_edgeCases pins the no-error pass-through contract
//     (empty rawURI returns ""; unknown recipeType returns rawURI unchanged;
//     a non-core first segment routes Scenario B to a third-party namespace).
//
//   - TestCoreSets_coverIntentLists asserts that every identifier the
//     authoring spec named as a core type is actually present in the
//     coreComponents / coreFeatures maps — guards against silent drift if a
//     map entry is removed without updating the spec.
//
// All three tests work the package through ParseRecipeURI (and the
// unexported isCoreComponent / isCoreFeature predicates the maps gate)
// without touching the registry network or the resolver — the package is a
// pure, deterministic string transform.

package registry

import "testing"

func TestParseRecipeURI_specExamples(t *testing.T) {
	tests := []struct {
		kind string
		raw  string
		want string
	}{
		{
			"component",
			"ai-gateway/kong:3.4.0",
			"hub.aggrega.ai/aggrega/component-recipes/ai-gateway/kong:3.4.0",
		},
		{
			"component",
			"ai-gateway/internal/custom-proxy:1.0",
			"hub.aggrega.ai/aggrega/component-recipes/ai-gateway/internal/custom-proxy:1.0",
		},
		{
			"component",
			"server/react-app:1.0",
			"hub.aggrega.ai/aggrega/component-recipes/server/react-app:1.0",
		},
		{
			"feature",
			"human-approval-gate/slack:1.0",
			"hub.aggrega.ai/aggrega/feature-recipes/human-approval-gate/slack:1.0",
		},
		{
			"feature",
			"lakera/data-masking/guard:1.0",
			"hub.aggrega.ai/lakera/feature-recipes/data-masking/guard:1.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"_"+tt.raw, func(t *testing.T) {
			got := ParseRecipeURI(tt.kind, tt.raw)
			if got != tt.want {
				t.Fatalf("ParseRecipeURI(%q, %q) = %q, want %q", tt.kind, tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseRecipeURI_edgeCases(t *testing.T) {
	if got := ParseRecipeURI("component", ""); got != "" {
		t.Fatalf("empty raw: got %q", got)
	}
	if got := ParseRecipeURI("unknown", "foo/bar"); got != "foo/bar" {
		t.Fatalf("unknown kind: got %q", got)
	}
	if got := ParseRecipeURI("component", "acme/widget:1"); got != "hub.aggrega.ai/acme/component-recipes/widget:1" {
		t.Fatalf("third-party component: got %q", got)
	}
}

// TestParseRecipeURI_leadingSlashPreservesInput pins the contract that a
// rawURI whose first path segment is empty (a leading "/") is returned
// unchanged rather than rewritten to a registry-prefixed URI. The caller
// supplied a malformed input; the parser does not silently repair it.
func TestParseRecipeURI_leadingSlashPreservesInput(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"/foo/bar:1", "/acme/widget"} {
		if got := ParseRecipeURI("component", raw); got != raw {
			t.Errorf("ParseRecipeURI(%q): expected pass-through; got %q", raw, got)
		}
	}
}

// TestParseRecipeURI_thirdPartySingleSegment pins the third-party branch
// for a single-segment input (no separator after the namespace). The
// registry rewrite trails with a slash because the tail is empty.
func TestParseRecipeURI_thirdPartySingleSegment(t *testing.T) {
	t.Parallel()
	if got := ParseRecipeURI("component", "acme"); got != "hub.aggrega.ai/acme/component-recipes/" {
		t.Errorf("third-party single segment: got %q", got)
	}
	if got := ParseRecipeURI("feature", "lakera"); got != "hub.aggrega.ai/lakera/feature-recipes/" {
		t.Errorf("third-party single segment (feature): got %q", got)
	}
}

func TestCoreSets_coverIntentLists(t *testing.T) {
	for _, id := range []string{
		"ai-agent", "ai-datastore", "ai-gateway", "ai-model", "ai-pipeline", "ai-tool",
		"server", "worker", "task", "function", "stateful-server", "resource", "messaging",
	} {
		if !isCoreComponent(id) {
			t.Errorf("missing core component %q", id)
		}
	}
	for _, id := range []string{
		"semantic-routing", "human-approval-gate", "data-masking", "audit-logging", "public-ingress",
	} {
		if !isCoreFeature(id) {
			t.Errorf("missing core feature %q", id)
		}
	}
}
