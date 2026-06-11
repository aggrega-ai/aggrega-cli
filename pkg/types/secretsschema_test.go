// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSecretsSchema_YAMLRoundTrip(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/postgres
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  secretsSchema:
    root_password:
      required: true
      inject: [env, file]
      description: Admin password
    tls_ca:
      required: false
  schema:
    values: {}
`)

	var comp Component
	if err := yaml.Unmarshal(src, &comp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	root, _ := comp.Spec.SecretsSchema.Get("root_password")
	if !root.Required {
		t.Errorf("root_password.required: got false, want true")
	}
	if len(root.Inject) != 2 || root.Inject[0] != "env" || root.Inject[1] != "file" {
		t.Errorf("root_password.inject: %v", root.Inject)
	}
	if root.Description != "Admin password" {
		t.Errorf("root_password.description: %q", root.Description)
	}

	tls, _ := comp.Spec.SecretsSchema.Get("tls_ca")
	if tls.Required {
		t.Errorf("tls_ca.required: got true, want false")
	}
	if len(tls.Inject) != 0 {
		t.Errorf("tls_ca.inject: expected empty, got %v", tls.Inject)
	}
}

func TestSecretsSchema_LegacySecretNamesStillParses(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/legacy
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  secretNames: [root_password, api_token]
`)
	var comp Component
	if err := yaml.Unmarshal(src, &comp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(comp.Spec.SecretNames) != 2 {
		t.Errorf("secretNames: %v", comp.Spec.SecretNames)
	}
	if comp.Spec.SecretsSchema.Len() != 0 {
		t.Errorf("expected no SecretsSchema on legacy component, got %v", comp.Spec.SecretsSchema.Keys())
	}
}

func TestSecretsSchema_BothFieldsCoexist(t *testing.T) {
	t.Parallel()
	// Both legacy secretNames and new secretsSchema present — both should parse
	// (consumption-site precedence is WI-2's concern).
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/mixed
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  secretNames: [old_key]
  secretsSchema:
    new_key:
      required: true
`)
	var comp Component
	if err := yaml.Unmarshal(src, &comp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(comp.Spec.SecretNames) != 1 || comp.Spec.SecretNames[0] != "old_key" {
		t.Errorf("SecretNames: %v", comp.Spec.SecretNames)
	}
	newKey, _ := comp.Spec.SecretsSchema.Get("new_key")
	if !newKey.Required {
		t.Errorf("SecretsSchema.new_key.required missing")
	}
}
