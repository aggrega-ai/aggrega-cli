package builder

import (
	"encoding/json"
	"testing"
)

func TestMergeDeveloperOverManifest_developerWinsTopLevel(t *testing.T) {
	man := map[string]interface{}{
		"image": "kong",
		"foo":   "from-ops",
	}
	dev := map[string]interface{}{
		"image": "myapp",
	}
	got := mergeDeveloperOverManifest(man, dev)
	if got["image"] != "myapp" {
		t.Fatalf("image: want developer override, got %v", got["image"])
	}
	if got["foo"] != "from-ops" {
		t.Fatalf("foo: want manifest fallback, got %v", got["foo"])
	}
}

func TestMergeDeveloperOverManifest_environmentDeepMerge(t *testing.T) {
	man := map[string]interface{}{
		"environment": map[string]interface{}{
			"KONG_DATABASE": "off",
			"OPS_ONLY":      "yes",
		},
	}
	dev := map[string]interface{}{
		"environment": map[string]interface{}{
			"KONG_DATABASE": "postgres",
			"DEV_ONLY":      "1",
		},
	}
	got := mergeDeveloperOverManifest(man, dev)
	env, ok := got["environment"].(map[string]interface{})
	if !ok {
		t.Fatalf("environment: %T", got["environment"])
	}
	if env["KONG_DATABASE"] != "postgres" {
		t.Fatalf("dev should override KONG_DATABASE, got %v", env["KONG_DATABASE"])
	}
	if env["OPS_ONLY"] != "yes" {
		t.Fatalf("ops key missing: %v", env["OPS_ONLY"])
	}
	if env["DEV_ONLY"] != "1" {
		t.Fatalf("dev key missing: %v", env["DEV_ONLY"])
	}
}

func TestResolveManifestAgainstAggregate_mergesValuesIntoComponent(t *testing.T) {
	aggregateYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    web:
      type: server
`
	manifestYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: lock
spec:
  mappings:
    server:
      version: "1.0.0"
      componentRecipe: oci://example/server
      values:
        image: kong
`
	gb := &GraphBuilder{CompilerVersion: "test"}
	ir, err := gb.Build([]byte(aggregateYAML), []byte(manifestYAML))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, ok := ir.Spec.ValuesMapping["web"]
	if !ok {
		t.Fatal("missing values_mapping")
	}
	var vm map[string]interface{}
	if err := json.Unmarshal(raw, &vm); err != nil {
		t.Fatal(err)
	}
	if vm["image"] != "kong" {
		t.Fatalf("expected merged manifest image kong, got %#v", vm["image"])
	}
}

func TestResolveManifestAgainstAggregate_developerImageOverridesManifest(t *testing.T) {
	aggregateYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    web:
      type: server
      values:
        image: dev-image
`
	manifestYAML := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: lock
spec:
  mappings:
    server:
      version: "1.0.0"
      componentRecipe: oci://example/server
      values:
        image: kong
        foo: bar
`
	gb := &GraphBuilder{CompilerVersion: "test"}
	ir, err := gb.Build([]byte(aggregateYAML), []byte(manifestYAML))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var vm map[string]interface{}
	if err := json.Unmarshal(ir.Spec.ValuesMapping["web"], &vm); err != nil {
		t.Fatal(err)
	}
	if vm["image"] != "dev-image" {
		t.Fatalf("image: %v", vm["image"])
	}
	if vm["foo"] != "bar" {
		t.Fatalf("manifest-only key missing: %v", vm["foo"])
	}
}
