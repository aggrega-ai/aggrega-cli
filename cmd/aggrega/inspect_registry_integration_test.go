// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestIntegration_InspectReadsAPublishedEntityFromTheRegistry drives the registry
// arm of `aggrega inspect` end to end: two entities are published through the real
// publish path to an in-process registry — a Component, which ships as its
// document, and a Recipe, which ships as an archive of its version directory with
// a co-located file beside the entity — and each is inspected by its coordinate.
//
// Each positive asserts the entity's own header and the coordinate the view says
// it read, so a read that returned the archive unopened, or rendered nothing, fails
// here rather than passing on the absence of an error. The missing coordinate is
// the negative, asserted against the condition it pins. The last assertion is the
// reader's other half: the consuming workspace holds nothing afterwards but its
// configuration, so reading a coordinate wrote no cache entry and no lockfile.
func TestIntegration_InspectReadsAPublishedEntityFromTheRegistry(t *testing.T) {
	host := startRegistry(t, nil)

	cat := t.TempDir()
	writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`)
	writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      server:
        image: test-server:latest
`)
	writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/notes.yaml", "notes: co-located beside the entity, and not an entity\n")

	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{})
	for _, rel := range []string{"component/test/svc/1.0.0/component.yaml", "recipe/helm/svc/1.0.0/recipe.yaml"} {
		if err := pushRun(t.Context(), pushConfig{Workspace: publisher, EntityPath: filepath.Join(cat, rel), Registry: host}); err != nil {
			t.Fatalf("publish %s: %v", rel, err)
		}
	}

	consumer := t.TempDir()
	writeWorkspaceConfig(t, consumer, "workspace: {}\noffline: false")
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: consumer})

	for _, row := range []struct {
		name, ref, header string
	}{
		{"a document-shaped coordinate", "oci://" + host + "/component/test/svc:1.0.0", "Component: test/svc:1.0.0"},
		{"an archive-shaped coordinate", "oci://" + host + "/recipe/helm/svc:1.0.0", "Recipe: svc:1.0.0"},
	} {
		t.Run(row.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := dispatchInspect(context.Background(), rw, row.ref, nil, testInspectOptions(&out)); err != nil {
				t.Fatalf("inspect %s: %v", row.ref, err)
			}
			if !strings.Contains(out.String(), row.header) {
				t.Errorf("the view does not open with the published entity's header %q:\n%s", row.header, out.String())
			}
			if !strings.Contains(out.String(), "Published at: "+row.ref) {
				t.Errorf("the view does not name the coordinate it read:\n%s", out.String())
			}
		})
	}

	err := dispatchInspect(context.Background(), rw, "oci://"+host+"/component/test/svc:9.9.9", nil, testInspectOptions(&bytes.Buffer{}))
	if !errors.Is(err, oci.ErrFetchNotFound()) {
		t.Errorf("a coordinate nothing is published at: err = %v; want ErrFetchNotFound", err)
	}

	var written []string
	_ = filepath.WalkDir(consumer, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr == nil && !d.IsDir() && filepath.Base(p) != ".aggrega-config.yaml" {
			written = append(written, p)
		}
		return nil
	})
	if len(written) > 0 {
		t.Errorf("reading published entities wrote into the consuming workspace: %v", written)
	}
}
