package builder

import (
	"encoding/json"
	"testing"
)

func TestGraphBuilder_Build_SplitBrainValues(t *testing.T) {
	aggregateYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    - id: web
      type: core/http-server
      values:
        port: 8080
`
	manifestYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: lock
spec:
  mappings:
    core/http-server:
      version: "1.0.0"
      componentRecipe: oci://example/recipes/http-server
`
	gb := &GraphBuilder{CompilerVersion: "test", IRSchemaVersion: "ir-v1alpha1"}
	ir, err := gb.Build([]byte(aggregateYAML), []byte(manifestYAML))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	node := ir.Spec.Nodes["web"]
	if node.Component != "core/http-server" {
		t.Fatalf("node component: got %q", node.Component)
	}
	raw, ok := ir.Spec.ValuesMapping["web"]
	if !ok {
		t.Fatal("expected values_mapping entry for web")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("values_mapping json: %v", err)
	}
	if payload["port"].(float64) != 8080 {
		t.Fatalf("values_mapping port: got %#v", payload["port"])
	}
	if ir.Spec.FeatureValuesMapping == nil {
		t.Fatal("expected non-nil FeatureValuesMapping")
	}
	inner, ok := ir.Spec.FeatureValuesMapping["web"]
	if !ok || inner == nil {
		t.Fatal("expected empty feature map for web")
	}
	if len(inner) != 0 {
		t.Fatalf("expected no feature configs, got %d entries", len(inner))
	}
}

func TestGraphBuilder_Build_FeatureValuesMapping(t *testing.T) {
	aggregateYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    - id: gw
      type: core/gateway
      features:
        - type: privacy-filter
          with:
            action: redact
    - id: agent
      type: core/agent
`
	manifestYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: lock
spec:
  mappings:
    core/gateway:
      version: "1.0.0"
      componentRecipe: oci://example/gateway
    core/agent:
      version: "1.0.0"
      componentRecipe: oci://example/agent
    privacy-filter:
      version: "1.0.0"
      featureRecipe: oci://example/privacy
`
	gb := &GraphBuilder{CompilerVersion: "test"}
	ir, err := gb.Build([]byte(aggregateYAML), []byte(manifestYAML))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	gwFeatures := ir.Spec.FeatureValuesMapping["gw"]
	if gwFeatures == nil {
		t.Fatal("expected non-nil feature map for gw")
	}
	raw, ok := gwFeatures["privacy-filter"]
	if !ok {
		t.Fatal("missing privacy-filter feature payload")
	}
	var with map[string]interface{}
	if err := json.Unmarshal(raw, &with); err != nil {
		t.Fatalf("feature with json: %v", err)
	}
	if with["action"] != "redact" {
		t.Fatalf("feature with action: got %#v", with["action"])
	}
	agentFeatures := ir.Spec.FeatureValuesMapping["agent"]
	if agentFeatures == nil {
		t.Fatal("expected non-nil feature map for agent")
	}
	if len(agentFeatures) != 0 {
		t.Fatalf("agent: expected empty inner map, got %v", agentFeatures)
	}
}

func TestResolveManifestAgainstAggregate_Errors(t *testing.T) {
	agg := &Aggregate{
		APIVersion: expectedAPIVersion,
		Kind:       aggregateKind,
		Metadata:   AggregateMetadata{Name: "a"},
		Spec: AggregateSpec{
			Components: []AggregateComponent{{
				ID:   "x",
				Type: "missing/component",
			}},
		},
	}
	man := &Manifest{
		APIVersion: expectedAPIVersion,
		Kind:       manifestKind,
		Metadata:   ManifestMeta{Name: "m"},
		Spec: ManifestSpec{
			Mappings: map[string]ManifestMapping{},
		},
	}
	if err := validateManifest(man); err == nil {
		t.Fatal("expected validateManifest to reject empty mappings")
	}
	man.Spec.Mappings["other"] = ManifestMapping{Version: "1", ComponentRecipe: "oci://x"}
	if err := ResolveManifestAgainstAggregate(agg, man); err == nil {
		t.Fatal("expected resolution error for missing component mapping")
	}
}

func TestInferResolvedType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"core/ai-api-gateway", "ai-gateway"},
		{"core/enterprise-agent", "ai-agent"},
		{"core/vector-datastore", "ai-datastore"},
		{"core/agent-event-logs", "ai-datastore"},
		{"hosted-llm", "ai-model"},
		{"generic/server", "server"},
		{"unknown/widget", "component"},
	}
	for _, tt := range tests {
		if got := InferResolvedType(tt.in); got != tt.want {
			t.Errorf("InferResolvedType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
