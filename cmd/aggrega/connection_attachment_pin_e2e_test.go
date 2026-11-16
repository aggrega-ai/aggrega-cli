// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// The test in this file drives what a user who puts an attachment on a connection
// meets, through the real pull and build: the Feature the attachment names is
// pinned in the lockfile under the connection entry the author wrote — for an
// attachment the Aggregate writes and for one only the Release makes — beside the
// realization the Manifest binds to it; the build verifies it against that pin; and
// a lockfile that does not pin it, or a Feature changed since the pull, is refused
// as drift naming the entry that asked for it.
//
// Neither Feature is named anywhere but on a connection, so with the lock-time
// walk reading a component's own attachments alone the pull records no row for
// either: the row assertions fail first, the changed Feature builds unnoticed, and
// with no row to remove the refusal assertions have nothing to refuse.

// connectionPinFixture lays down a workspace whose caller reaches two peers through
// one socket declaring a credential slot: the Aggregate attaches a credential on
// the first connection, bound by its connection-qualified name, and the Release
// attaches one of its own on the second, bound by type. Every entity is in the
// workspace's catalog directory, so nothing reaches a registry.
func connectionPinFixture(t *testing.T, root string) (catalogDir, releasePath string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/example/compute/caller/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/caller
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    values:
      message:
        type: string
        default: "hello"
  usesSchema:
    peers:
      selector: "component.aggrega.ai/category=backend"
      featuresSchema:
        credential:
          description: The credential the caller presents to the peer this connection reaches.
`)
	writeFixture(t, filepath.Join(catalogDir, "component/example/compute/peer/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/peer
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: backend
spec:
  schema:
    values:
      message:
        type: string
        default: "peer"
`)
	for _, name := range []string{"caller", "peer"} {
		writeFixture(t, filepath.Join(catalogDir, "recipe/compose/"+name+"/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: `+name+`
  version: 1.0.0
spec:
  capability: compose
  implements: example/compute/`+name+`:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "`+name+`:1"
        variables:
          MESSAGE: "{{ .values.message }}"
`)
	}
	writeFixture(t, filepath.Join(catalogDir, "feature/example/token/1.0.0/feature.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/token
  version: 1.0.0
spec:
  schema:
    values:
      key:
        type: string
        default: "token"
    provides:
      key:
        type: string
`)
	writeFixture(t, filepath.Join(catalogDir, "feature/example/badge/1.0.0/feature.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/badge
  version: 1.0.0
spec:
  schema:
    infra:
      issuer:
        type: string
        default: "internal"
    provides:
      issuer:
        type: string
`)
	writeFixture(t, filepath.Join(catalogDir, "feature-recipe/compose/token-plain/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: token-plain
  version: 1.0.0
spec:
  capability: compose
  implements: example/token:1.0.0
  provides:
    key: "{{ .values.key }}"
`)
	writeFixture(t, filepath.Join(catalogDir, "feature-recipe/compose/badge-plain/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: badge-plain
  version: 1.0.0
spec:
  capability: compose
  implements: example/badge:1.0.0
  provides:
    issuer: "{{ .infra.issuer }}"
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: callers
  version: "1.0.0"
spec:
  components:
    caller:
      type: example/compute/caller
      uses:
        peers:
          first:
            target: peer-a
            features:
              credential:
                type: example/token
                values:
                  key: first-key
          second:
            target: peer-b
    peer-a:
      type: example/compute/peer
    peer-b:
      type: example/compute/peer
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: callers-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/caller:1.0.0: compose/caller:1.0.0
        example/compute/peer:1.0.0: compose/peer:1.0.0
    features:
      type:
        example/badge:1.0.0: compose/badge-plain:1.0.0
      name:
        first.credential: compose/token-plain:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: callers-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: callers
  manifest: callers-manifest
  boundaries:
    app:
      directory: app
      components:
        caller:
          uses:
            peers:
              second:
                features:
                  credential:
                    type: example/badge
                    infra:
                      issuer: partner
        peer-a: {}
        peer-b: {}
`)
	return catalogDir, releasePath
}

// TestAFeatureAConnectionAttachmentNamesIsPinnedAndVerified drives the pull, the
// verifying pull and the build over the fixture above, then removes each
// connection attachment's Feature row in turn, then changes one Feature after the
// pull.
func TestAFeatureAConnectionAttachmentNamesIsPinnedAndVerified(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	root := t.TempDir()
	catalogDir, rel := connectionPinFixture(t, root)
	lockPath := lockfile.PathFor(rel)

	pull := func(verify bool) error {
		t.Helper()
		return pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: rel,
			CatalogDirs: []string{catalogDir},
			Verify:      verify,
			DiagOut:     io.Discard,
			Out:         io.Discard,
			diag:        buildDiagOptions{quiet: true},
		})
	}
	build := func() error {
		t.Helper()
		return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			rel, []string{catalogDir}, false, io.Discard, io.Discard)
	}

	if err := pull(false); err != nil {
		t.Fatalf("pull: %v", err)
	}
	first := mustReadFile(t, lockPath)

	pins := []struct {
		uri  string
		kind string
		site string
	}{
		{"example/token", "feature", "aggregate.spec.components.caller.uses.peers.first.features.credential.type"},
		{"example/badge", "feature", "release.spec.boundaries.app.components.caller.uses.peers.second.features.credential.type"},
		{"compose/token-plain:1.0.0", "feature_recipe", "manifest.spec.mappings.features.name.first.credential"},
	}
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	for _, p := range pins {
		var found bool
		for _, e := range lf.Entities {
			if e.URI != p.uri {
				continue
			}
			found = true
			if strings.Join(e.ReferencedFrom, ",") != p.site {
				t.Errorf("row %q referenced_from = %v; want [%s]", p.uri, e.ReferencedFrom, p.site)
			}
			if e.Digest == "" || e.ResolvedVersion != "1.0.0" {
				t.Errorf("row %q pins (%q, %q); want version 1.0.0 and a digest", p.uri, e.ResolvedVersion, e.Digest)
			}
		}
		if !found {
			t.Fatalf("the pull recorded no row for %q, which %s asks for", p.uri, p.site)
		}
	}

	if err := pull(true); err != nil {
		t.Fatalf("pull --verify over the lockfile the pull wrote must pass: %v", err)
	}
	if err := build(); err != nil {
		t.Fatalf("build over the lockfile the pull wrote must pass: %v", err)
	}
	if err := pull(false); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if second := mustReadFile(t, lockPath); !bytes.Equal(second, first) {
		t.Errorf("a second pull over an unchanged workspace rewrote the lockfile\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	refusal := func(t *testing.T, want string) {
		t.Helper()
		steps := []struct {
			name string
			run  func() error
		}{
			{"build", build},
			{"pull --verify", func() error { return pull(true) }},
		}
		for _, step := range steps {
			err := step.run()
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Fatalf("%s must halt as drift; got %v", step.name, err)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal does not carry %q:\n%v", step.name, want, err)
			}
		}
	}

	for _, p := range pins[:2] {
		t.Run("without the row for "+p.uri, func(t *testing.T) {
			without, _, err := lockfile.LoadLockfile(lockPath)
			if err != nil {
				t.Fatalf("load lockfile: %v", err)
			}
			kept := without.Entities[:0]
			for _, e := range without.Entities {
				if e.URI != p.uri {
					kept = append(kept, e)
				}
			}
			without.Entities = kept
			if err := lockfile.WriteLockfile(lockPath, without); err != nil {
				t.Fatalf("write the lockfile without the row: %v", err)
			}
			refusal(t, `entity "`+p.uri+`" (`+p.kind+`) not in lockfile (referenced from `+p.site+`)`)
			if err := pull(false); err != nil {
				t.Fatalf("pull to restore the row: %v", err)
			}
			if err := build(); err != nil {
				t.Errorf("build after the pull restored the row must pass: %v", err)
			}
		})
	}

	t.Run("a Feature changed since the pull", func(t *testing.T) {
		featurePath := filepath.Join(catalogDir, "feature/example/token/1.0.0/feature.yaml")
		original := mustReadFile(t, featurePath)
		if err := os.WriteFile(featurePath, append(append([]byte(nil), original...), "# changed after the pull\n"...), 0o644); err != nil {
			t.Fatalf("change the Feature: %v", err)
		}
		refusal(t, `entity "example/token" (feature) changed: lockfile=(1.0.0,`)
		if err := os.WriteFile(featurePath, original, 0o644); err != nil {
			t.Fatalf("restore the Feature: %v", err)
		}
		if err := build(); err != nil {
			t.Errorf("build after the Feature was restored must pass: %v", err)
		}
	})
}
