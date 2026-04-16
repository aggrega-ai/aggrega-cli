// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"strings"
	"testing"
)

func TestInjectAggreraLabels_AddsLabelsToExistingMetadata(t *testing.T) {
	t.Parallel()
	in := []byte(`apiVersion: v1
kind: Deployment
metadata:
  name: api
  labels:
    app: api
spec:
  replicas: 1
`)
	labels := map[string]string{
		"aggrega.ai/aggregate-name": "test-app",
		"aggrega.ai/component-id":   "api",
	}
	out, err := injectAggreraLabels(in, labels)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "aggrega.ai/aggregate-name: test-app") {
		t.Errorf("aggregate-name missing: %s", s)
	}
	if !strings.Contains(s, "aggrega.ai/component-id: api") {
		t.Errorf("component-id missing: %s", s)
	}
	// Chart-author label preserved
	if !strings.Contains(s, "app: api") {
		t.Errorf("chart-author label lost: %s", s)
	}
}

func TestInjectAggreraLabels_CreatesMetadataLabelsWhenAbsent(t *testing.T) {
	t.Parallel()
	in := []byte(`apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  type: ClusterIP
`)
	labels := map[string]string{"aggrega.ai/component-id": "api"}
	out, err := injectAggreraLabels(in, labels)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "labels:") {
		t.Errorf("expected labels block created: %s", s)
	}
	if !strings.Contains(s, "aggrega.ai/component-id: api") {
		t.Errorf("expected label injected: %s", s)
	}
}

func TestInjectAggreraLabels_MultiDocumentBoundariesPreserved(t *testing.T) {
	t.Parallel()
	in := []byte(`apiVersion: v1
kind: Deployment
metadata:
  name: api
---
apiVersion: v1
kind: Service
metadata:
  name: api
`)
	labels := map[string]string{"aggrega.ai/component-id": "api"}
	out, err := injectAggreraLabels(in, labels)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if strings.Count(string(out), "---") < 1 {
		t.Errorf("multi-doc separator missing: %s", out)
	}
	// Both documents have the label
	if strings.Count(string(out), "aggrega.ai/component-id: api") != 2 {
		t.Errorf("expected label in both docs, got:\n%s", out)
	}
}

func TestInjectAggreraLabels_CollisionAggreraWins(t *testing.T) {
	t.Parallel()
	in := []byte(`apiVersion: v1
kind: Deployment
metadata:
  name: api
  labels:
    aggrega.ai/component-id: stale
`)
	labels := map[string]string{"aggrega.ai/component-id": "fresh"}
	out, err := injectAggreraLabels(in, labels)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "aggrega.ai/component-id: fresh") {
		t.Errorf("aggrega label should overwrite on collision: %s", s)
	}
	if strings.Contains(s, "stale") {
		t.Errorf("stale label should have been replaced: %s", s)
	}
}

func TestInjectAggreraLabels_EmptyLabelsIsNoOp(t *testing.T) {
	t.Parallel()
	in := []byte("apiVersion: v1\nkind: Deployment\n")
	out, err := injectAggreraLabels(in, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("expected passthrough for nil labels, got: %s", out)
	}
}
