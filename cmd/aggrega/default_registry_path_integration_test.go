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
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestIntegration_ADefaultRegistryWithARepositoryPathPublishesAndResolvesUnderIt
// drives a default registry carrying a repository path through the production
// push and pull paths against an in-process registry addressed by loopback IP and
// port, and holds three things the path is and is not.
//
// It is location: publishing a Component and its Recipe through the default,
// naming no destination, writes every request under the path and nothing at the
// registry's root; pulling them into a clean workspace through the same default
// fetches them from under the path, and the signatures attached at publication
// are found there and verified against the key the consumer pins, so the lockfile
// records each as verified.
//
// It is never trust: the consumer's one registries entry is written for the host
// alone and requires a signature, and it governs the prefixed default — an
// unsigned publication under another path on the same host halts the pull.
//
// And it is not something an entry's name carries: a default written as an
// entry's name followed by the path is refused where the configuration resolves.
//
// A publication that composed its reference from the host alone, or a pull that
// did not bind under the path, would put the two sides in different places: the
// request log is what says where each operation went, rather than inferring it
// from the operation succeeding.
func TestIntegration_ADefaultRegistryWithARepositoryPathPublishesAndResolvesUnderIt(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	var log requestLog
	host := startRegistry(t, log.wrap)
	const path = "acme/catalog"

	publish := func(t *testing.T, prefixPath string, sign bool, name string) {
		t.Helper()
		pub := t.TempDir()
		writeWorkspaceConfig(t, pub, "workspace:\n  catalog_dirs: []\noffline: false\ndefault_registry: \""+host+"/"+prefixPath+"\"\n")
		componentPath := filepath.Join(pub, "component/test/"+name+"/1.0.0/component.yaml")
		writeFixture(t, componentPath, `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/`+name+`
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
`)
		recipePath := filepath.Join(pub, "recipe/helm/"+name+"/1.0.0/recipe.yaml")
		writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: `+name+`
  version: "1.0.0"
spec:
  capability: helm
  implements: test/`+name+`
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
		if got := pubWS.Config().DefaultRegistryLocation().Prefix(); got != host+"/"+prefixPath {
			t.Fatalf("the publisher's default reads as %q, want %q", got, host+"/"+prefixPath)
		}
		for _, p := range []string{componentPath, recipePath} {
			cfg := pushConfig{Workspace: pubWS, EntityPath: p}
			if sign {
				on := true
				cfg.Sign, cfg.Signer = &on, signer
			}
			if err := pushRun(t.Context(), cfg); err != nil {
				t.Fatalf("publish %s through the prefixed default: %v", p, err)
			}
		}
	}

	consumer := func(t *testing.T, prefixPath, name string) string {
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
      type: test/`+name+`
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
        test/`+name+`:1.0.0: helm/`+name+`:1.0.0
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
		// The one entry is written for the host alone; the default carries the path.
		body := strings.Replace(signedPluginWorkspaceConfigBody(host, pubPEM),
			"default_registry: \""+host+"\"", "default_registry: \""+host+"/"+prefixPath+"\"", 1)
		writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs: []\n"+body)
		t.Chdir(ws)
		return ws
	}

	mark := log.len()
	publish(t, path, true, "svc")
	published := log.since(mark)
	for _, repo := range []string{"/v2/" + path + "/component/test/svc/manifests/1.0.0", "/v2/" + path + "/recipe/helm/svc/manifests/1.0.0"} {
		if !anyContains(published, "PUT", repo) {
			t.Errorf("publishing through the prefixed default wrote no %s; requests: %v", repo, published)
		}
	}
	for _, line := range published {
		if !strings.Contains(line, " /v2/"+path+"/") {
			t.Errorf("publishing through the prefixed default reached outside its path: %q", line)
		}
	}

	t.Run("pulls_and_verifies_from_under_the_path", func(t *testing.T) {
		ws := consumer(t, path, "svc")
		mark := log.len()
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"}); err != nil {
			t.Fatalf("a signed publication pulled through the same prefixed default, its key pinned for the host: %v", err)
		}
		fetched := log.since(mark)
		if len(fetched) == 0 {
			t.Fatal("the pull reached the registry for nothing")
		}
		for _, line := range fetched {
			if !strings.Contains(line, " /v2/"+path+"/") {
				t.Errorf("the pull reached outside the default's path: %q", line)
			}
		}
		if !anyContains(fetched, "/v2/"+path+"/component/test/svc/referrers/") {
			t.Errorf("the pull looked for no signature of the Component under the path; requests: %v", fetched)
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if err != nil {
			t.Fatalf("load the lockfile the pull wrote: %v", err)
		}
		verified := map[string]bool{}
		for _, e := range lf.Entities {
			if e.Signature != nil && e.Signature.Mode == "keyed" {
				verified[e.URI] = true
			}
		}
		for _, uri := range []string{"test/svc:1.0.0", "helm/svc:1.0.0"} {
			if !verified[uri] {
				t.Errorf("the lockfile records no keyed signature verified for %s; entities: %+v", uri, lf.Entities)
			}
		}
	})

	t.Run("the_hosts_policy_governs_the_prefixed_default", func(t *testing.T) {
		publish(t, "unsigned", false, "plain")
		ws := consumer(t, "unsigned", "plain")
		mark := log.len()
		err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"})
		if !errors.Is(err, signing.ErrNoSignatureFound()) {
			t.Fatalf("an unsigned publication under a path on a host whose entry requires a signature: got %v, want the pull halted with ErrNoSignatureFound", err)
		}
		if !anyContains(log.since(mark), "/v2/unsigned/recipe/helm/plain/referrers/") {
			t.Errorf("the refused pull looked for no signature under the path, so the refusal is not the verification of what it fetched; requests: %v", log.since(mark))
		}
	})

	t.Run("an_entrys_name_followed_by_a_path_is_refused", func(t *testing.T) {
		ws := t.TempDir()
		writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs: []\ndefault_registry: local/"+path+"\nregistries:\n  - name: local\n    host: \""+host+"\"\n")
		_, err := workspaceconfig.Resolve(ws, "", func(string) string { return "" })
		if !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
			t.Fatalf("an entry's name followed by a path: got %v, want it refused where the configuration resolves", err)
		}
		if !strings.Contains(err.Error(), `write "local"`) || !strings.Contains(err.Error(), `"`+host+"/"+path+`"`) {
			t.Errorf("the refusal does not name the two spellings to write: %v", err)
		}
	})
}
