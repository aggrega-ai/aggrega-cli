package schema

import (
	"strings"
	"testing"
)

func TestValidate_Aggregate_happy(t *testing.T) {
	yaml := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: ai-gateway
      values:
        max_connections: 100
    db:
      type: ai-datastore
`
	if err := Validate([]byte(yaml), "Aggregate"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_Aggregate_defaultTypeServer(t *testing.T) {
	yaml := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    svc: {}
`
	if err := Validate([]byte(yaml), "Aggregate"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_Release_happy(t *testing.T) {
	yaml := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel1
spec:
  aggregate: my-app
  manifest: my-lock
  policies:
    strict_networking: true
  scopes:
    - name: local
      provider: compose
    - name: prod
      provider: helm
`
	if err := Validate([]byte(yaml), "Release"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_Release_optionalPoliciesOmitted(t *testing.T) {
	yaml := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel1
spec:
  aggregate: a
  manifest: m
  scopes:
    - name: s1
      provider: compose
`
	if err := Validate([]byte(yaml), "Release"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_errors(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		yaml    string
		contains []string
	}{
		{
			name: "aggregate bad apiVersion",
			kind: "Aggregate",
			yaml: `
apiVersion: aggrega.ai/v99
kind: Aggregate
metadata:
  name: x
spec:
  components:
    a:
      type: server
`,
			contains: []string{"cue validation failed"},
		},
		{
			name: "aggregate component type must be string not number",
			kind: "Aggregate",
			yaml: `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: x
spec:
  components:
    web:
      type: 8080
`,
			contains: []string{"cue validation failed"},
		},
		{
			name: "aggregate invalid component type enum",
			kind: "Aggregate",
			yaml: `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: x
spec:
  components:
    web:
      type: not-in-enum
`,
			contains: []string{"cue validation failed"},
		},
		{
			name: "release bad apiVersion",
			kind: "Release",
			yaml: `
apiVersion: wrong
kind: Release
metadata:
  name: r
spec:
  aggregate: a
  manifest: m
  scopes:
    - name: s
      provider: p
`,
			contains: []string{"cue validation failed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate([]byte(tt.yaml), tt.kind)
			if err == nil {
				t.Fatal("expected error")
			}
			msg := err.Error()
			for _, frag := range tt.contains {
				if !strings.Contains(msg, frag) {
					t.Fatalf("error %q should contain %q", msg, frag)
				}
			}
		})
	}
}

func TestValidate_unsupportedKind(t *testing.T) {
	err := Validate([]byte(`x: 1`), "Manifest")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("got %v", err)
	}
}
