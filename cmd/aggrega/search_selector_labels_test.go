// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// labelledComponentRepo is the OCI repository path the labelled fixture publishes
// to, derived through the same location rule the push verb uses so the fixture
// cannot drift from the real publish location.
var labelledComponentRepo = catalog.OCILocation("Component", "", "core/compute/api-server")

// publishAndIndexLabelledComponent publishes a Component carrying three authored
// metadata.labels through the real publish path, indexes the published manifest
// through the real registry-update path, and returns the workspace a search reads.
//
// Driving both real paths is the point of the fixture rather than a convenience:
// the label map a selector matches has to survive the manifest round trip, so a
// fixture that seeded annotations directly would assert nothing about whether the
// author's labels ever reach the manifest.
func publishAndIndexLabelledComponent(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	const host = "hub.aggrega.ai"

	root := t.TempDir()
	rw := resolveWorkspaceWithConfig(t, root, "workspace: {}\ndefault_registry: hub.aggrega.ai\n"+
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n")

	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/api-server
  version: 1.0.0
  description: The API server component.
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
    component.aggrega.ai/upstream-licensing: osi-approved
spec:
  schema:
    values:
      type: object
`)

	store := &memTagTarget{Store: memory.New()}
	if err := pushRun(t.Context(), pushConfig{
		EntityPath: entityPath,
		Registry:   host,
		PushTarget: store,
		Workspace:  rw,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}
	store.tags = []string{"1.0.0"}

	withIndexTarget(t, fakeIndexSource{
		repos:   []string{labelledComponentRepo},
		targets: map[string]oras.ReadOnlyTarget{labelledComponentRepo: store},
	})
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: new(bytes.Buffer), now: searchFrozen}); err != nil {
		t.Fatalf("registryUpdateRun: %v", err)
	}
	withSearchClock(t, searchFrozen().Add(time.Hour))
	return rw
}

// TestSearchSelectorMatchesEveryAuthoredLabel proves a label selector reaches
// every label an author wrote, not only the category. The category case is the
// control: it is the one label that matched before the full map travelled on the
// manifest, so its passing alongside the others shows the result is not an
// artifact of the fixture. The mismatched-value cases prove the selector still
// discriminates rather than matching everything.
func TestSearchSelectorMatchesEveryAuthoredLabel(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget and searchNow seams.
	rw := publishAndIndexLabelledComponent(t)
	wantURI := "oci://hub.aggrega.ai/" + labelledComponentRepo

	cases := []struct {
		name     string
		selector string
		match    bool
	}{
		{"non-category label type", "component.aggrega.ai/type=server", true},
		{"non-category label upstream-licensing", "component.aggrega.ai/upstream-licensing=osi-approved", true},
		{"category label (control)", "component.aggrega.ai/category=compute", true},
		{"two labels intersected", "component.aggrega.ai/type=server,component.aggrega.ai/category=compute", true},
		{"existence of a non-category key", "component.aggrega.ai/upstream-licensing", true},
		{"non-category label wrong value", "component.aggrega.ai/type=worker", false},
		{"category label wrong value (control)", "component.aggrega.ai/category=data", false},
		{"label the author never wrote", "component.aggrega.ai/tier=gold", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := runSearch(t, rw, searchOptions{Selector: tc.selector, Output: "names"})
			if err != nil {
				t.Fatalf("searchRun(--selector %q): %v", tc.selector, err)
			}
			got := strings.TrimSpace(out)
			if tc.match {
				if got != wantURI {
					t.Errorf("--selector %q returned %q; want %q", tc.selector, got, wantURI)
				}
				return
			}
			if got != "" {
				t.Errorf("--selector %q returned %q; want no match", tc.selector, got)
			}
		})
	}
}

// TestPublishedLabelMapIsAuthorOrdered proves the label map stamped onto the
// manifest and read back into the index record preserves the order the author
// wrote, which is what makes the index bytes identical across two builds of the
// same registry state.
func TestPublishedLabelMapIsAuthorOrdered(t *testing.T) {
	// Not parallel: mutates the process-global registryops.IndexTarget and searchNow seams.
	rw := publishAndIndexLabelledComponent(t)

	cfg := rw.Config()
	loaded, err := registryindex.LoadHost(rw.Root(), cfg.ResolvedArtifactCacheDir(), "hub.aggrega.ai")
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if len(loaded.Records) != 1 {
		t.Fatalf("index has %d records; want 1: %+v", len(loaded.Records), loaded.Records)
	}
	rec := loaded.Records[0]
	if rec.Labels == nil {
		t.Fatal("record carries no labels; the authored label map did not survive the manifest round trip")
	}
	want := []string{
		"component.aggrega.ai/type",
		"component.aggrega.ai/category",
		"component.aggrega.ai/upstream-licensing",
	}
	got := rec.Labels.Keys()
	if len(got) != len(want) {
		t.Fatalf("label keys = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label key[%d] = %q; want %q (author order)", i, got[i], want[i])
		}
	}
}
