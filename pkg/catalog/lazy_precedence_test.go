// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "testing"

func componentBody(desc string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/thing
  version: "1.0.0"
  description: ` + desc + `
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`
}

func featureBody(desc string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/thing-feature
  version: "1.0.0"
  description: ` + desc + `
spec: {}
`
}

// TestLazyTier1Precedence_FirstWin proves the lazy Component/Feature filesystem
// index resolves the FIRST catalog directory declaring a name at a version,
// matching the eager whole-catalog merge and the lazy recipe-shell enumeration.
// Before the shared foldFirstWin fix the index was unconditional last-write, so
// dir B silently overrode dir A; this test fails against that behavior.
func TestLazyTier1Precedence_FirstWin(t *testing.T) {
	t.Parallel()

	dirA, dirB := t.TempDir(), t.TempDir()
	writeFile(t, dirA+"/component.yaml", componentBody("dir-a"))
	writeFile(t, dirB+"/component.yaml", componentBody("dir-b"))
	writeFile(t, dirA+"/feature.yaml", featureBody("dir-a"))
	writeFile(t, dirB+"/feature.yaml", featureBody("dir-b"))

	ll := NewLazyLookup("", "", []string{dirA, dirB}, nil)

	comp, ok := ll.tier1ReadComponent("core/thing")
	if !ok {
		t.Fatalf("tier1ReadComponent(core/thing): not found")
	}
	if got := comp.Metadata.Description; got != "dir-a" {
		t.Errorf("Component first-win across [A,B]: want dir-a, got %q", got)
	}

	feat, ok := ll.tier1ReadFeature("core/thing-feature")
	if !ok {
		t.Fatalf("tier1ReadFeature(core/thing-feature): not found")
	}
	if got := feat.Metadata.Description; got != "dir-a" {
		t.Errorf("Feature first-win across [A,B]: want dir-a, got %q", got)
	}
}
