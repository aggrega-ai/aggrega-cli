// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestFeaturesSchema_YAMLRoundTrip(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/datastore/postgres
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  featuresSchema:
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

	root, _ := comp.Spec.FeaturesSchema.Get("root_password")
	if !root.Required {
		t.Errorf("root_password.required: got false, want true")
	}
	if len(root.Inject) != 2 || root.Inject[0] != "env" || root.Inject[1] != "file" {
		t.Errorf("root_password.inject: %v", root.Inject)
	}
	if root.Description != "Admin password" {
		t.Errorf("root_password.description: %q", root.Description)
	}

	tls, _ := comp.Spec.FeaturesSchema.Get("tls_ca")
	if tls.Required {
		t.Errorf("tls_ca.required: got true, want false")
	}
	if len(tls.Inject) != 0 {
		t.Errorf("tls_ca.inject: expected empty, got %v", tls.Inject)
	}
}

// TestFeaturesSchema_UnknownSecretNamesKeyIgnoredAtParse pins the
// types-layer posture for the retired secretNames key: yaml.Unmarshal
// drops unknown keys, so a stray secretNames contributes nothing to the
// parsed spec. The loud rejection of the key is JSON-schema
// validation's job, not the unmarshaler's.
func TestFeaturesSchema_UnknownSecretNamesKeyIgnoredAtParse(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/stray
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  secretNames: [old_key]
  featuresSchema:
    new_key:
      required: true
`)
	var comp Component
	if err := yaml.Unmarshal(src, &comp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if comp.Spec.FeaturesSchema.Len() != 1 {
		t.Errorf("featuresSchema should parse; got %v", comp.Spec.FeaturesSchema.Keys())
	}
	newKey, _ := comp.Spec.FeaturesSchema.Get("new_key")
	if !newKey.Required {
		t.Errorf("FeaturesSchema.new_key.required missing")
	}
}
