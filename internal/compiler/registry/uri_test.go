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
