// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// anyContains reports whether any request line contains every one of parts.
func anyContains(lines []string, parts ...string) bool {
	for _, line := range lines {
		all := true
		for _, p := range parts {
			if !strings.Contains(line, p) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// TestIntegration_ADefaultRegistryNamingAnEntryPublishesToAndPullsFromItsHost
// drives a default registry written as a registries entry's name through the
// production push and pull paths against an in-process registry addressed by
// loopback IP and port.
//
// The workspace names the default `local` and carries one entry named `local`
// whose host is the in-process registry. Publishing with no --registry must reach
// that registry; pulling must fetch from it; and with a signature required, the
// unsigned artifacts it fetched must halt the pull — the verification filter
// reads the entry's host, not a host named after the alias. A default read as
// the name it is written as would bind every location at a host called `local`:
// the pull would reach no registry the test runs, and the filter would read that
// single name as no registry at all.
func TestIntegration_ADefaultRegistryNamingAnEntryPublishesToAndPullsFromItsHost(t *testing.T) {
	// The request log is what says which registry each operation reached, rather
	// than inferring it from the operation succeeding.
	var log requestLog
	host := startRegistry(t, log.wrap)
	aliasConfig := "workspace:\n  catalog_dirs: []\ndefault_registry: local\noffline: false\n" +
		"registries:\n  - name: local\n    host: \"" + host + "\"\n"

	// Publish the Component and its Recipe unsigned, naming no destination: the
	// default is the only thing that says where they go.
	pub := t.TempDir()
	writeWorkspaceConfig(t, pub, aliasConfig)
	componentPath := filepath.Join(pub, "component/test/svc/1.0.0/component.yaml")
	writeFixture(t, componentPath, `apiVersion: aggrega.ai/v1beta1
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
      replicas: { type: integer, default: 1 }
`)
	recipePath := filepath.Join(pub, "recipe/helm/svc/1.0.0/recipe.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1beta1
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
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra `+"`port`"+` 8080 }}
`)
	pubWS := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	if got := pubWS.Config().DefaultRegistry; got != host {
		t.Fatalf("the publisher's default registry resolved to %q, want the entry's host %q", got, host)
	}
	mark := log.len()
	for _, p := range []string{componentPath, recipePath} {
		if err := pushRun(t.Context(), pushConfig{Workspace: pubWS, EntityPath: p}); err != nil {
			t.Fatalf("publish %s through the default registry's name: %v", p, err)
		}
	}
	published := log.since(mark)
	for _, repo := range []string{"/v2/component/test/svc/manifests/1.0.0", "/v2/recipe/helm/svc/manifests/1.0.0"} {
		if !anyContains(published, "PUT", repo) {
			t.Errorf("publishing through the default registry's name wrote no %s to the entry's host; requests: %v", repo, published)
		}
	}

	consumer := func(t *testing.T, signingBlock string) string {
		t.Helper()
		ws := t.TempDir()
		writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
		writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
`)
		writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
		writeWorkspaceConfig(t, ws, aliasConfig+signingBlock)
		t.Chdir(ws)
		return ws
	}

	// With no signing requirement the pull fetches both entities from the
	// entry's host and pins them.
	t.Run("pulls_from_the_entrys_host", func(t *testing.T) {
		ws := consumer(t, "")
		mark := log.len()
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"}); err != nil {
			t.Fatalf("pull through the default registry's name: %v", err)
		}
		fetched := log.since(mark)
		for _, repo := range []string{"/v2/component/test/svc/", "/v2/recipe/helm/svc/"} {
			if !anyContains(fetched, repo) {
				t.Errorf("the pull reached the entry's host for nothing under %s; requests: %v", repo, fetched)
			}
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if err != nil {
			t.Fatalf("load the lockfile the pull wrote: %v", err)
		}
		pinned := map[string]bool{}
		for _, e := range lf.Entities {
			pinned[e.URI] = true
		}
		for _, uri := range []string{"test/svc:1.0.0", "helm/svc:1.0.0"} {
			if !pinned[uri] {
				t.Errorf("the lockfile pins no %s; entities: %v", uri, lf.Entities)
			}
		}
	})

	// With a signature required, the unsigned artifacts fetched from the entry's
	// host halt the pull: the verification pass read that host as a registry and
	// checked what came from it.
	t.Run("signature_required_halts_on_the_unsigned_artifact", func(t *testing.T) {
		ws := consumer(t, "signing:\n  required: true\n")
		mark := log.len()
		err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"})
		if !errors.Is(err, signing.ErrNoSignatureFound()) {
			t.Fatalf("an unsigned artifact pulled through the default registry's name under a required signature: got %v, want the pull halted with ErrNoSignatureFound", err)
		}
		fetched := log.since(mark)
		if !anyContains(fetched, "/v2/") {
			t.Errorf("the refused pull reached the entry's host for nothing, so the refusal is not the verification of what it fetched; requests: %v", fetched)
		}
	})
}
