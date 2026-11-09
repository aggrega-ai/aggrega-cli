// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The test in this file drives what a user who overrides one instance's
// realization in the Manifest's name tier meets, through the real pull and build:
// the realization chosen by instance name is pinned in the lockfile beside the
// type-tier ones, the build renders through it and verifies it against that pin,
// and a lockfile that does not pin it is refused as drift naming the instance
// whose binding asked for it. It holds both sections of the mapping — a Recipe
// chosen for one component instance, and a FeatureRecipe chosen for one
// attachment — since each is read through the same tiers.
//
// With the lock-time walk reading the type tier alone, the pull records no row for
// either override, so the row assertions fail first; and with no row to remove,
// the refusal assertions have nothing to refuse.

// nameTierFixture lays down a workspace whose Manifest binds every greeter through
// the type tier and overrides one instance, and one attachment, by name. Every
// entity is in the workspace's catalog directory, so nothing reaches a registry.
func nameTierFixture(t *testing.T, root string) (catalogDir, releasePath string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/example/compute/greeter/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/greeter
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
    infra:
      port:
        type: integer
        default: 8080
`)
	for _, colour := range []string{"blue", "green"} {
		writeFixture(t, filepath.Join(catalogDir, "recipe/compose/greeter-"+colour+"/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: greeter-`+colour+`
  version: 1.0.0
spec:
  capability: compose
  implements: example/compute/greeter:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "greeter:`+colour+`"
        variables:
          MESSAGE: "{{ .values.message }}"
        ports:
          http: {{ optional .infra `+"`port`"+` 8080 }}
`)
	}
	writeFixture(t, filepath.Join(catalogDir, "feature/example/banner/1.0.0/feature.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/banner
  version: 1.0.0
spec:
  schema:
    values:
      text:
        type: string
        default: "hi"
    provides:
      banner:
        type: string
`)
	for _, tone := range []string{"plain", "loud"} {
		writeFixture(t, filepath.Join(catalogDir, "feature-recipe/compose/banner-"+tone+"/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: banner-`+tone+`
  version: 1.0.0
spec:
  capability: compose
  implements: example/banner:1.0.0
  provides:
    banner: "{{ .values.text }}-`+tone+`"
`)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: greeters
  version: "1.0.0"
spec:
  components:
    greeter-default:
      type: example/compute/greeter
      features:
        banner:
          type: example/banner
    greeter-special:
      type: example/compute/greeter
      features:
        loud-banner:
          type: example/banner
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: greeters-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/greeter:1.0.0: compose/greeter-blue:1.0.0
      name:
        greeter-special: compose/greeter-green:1.0.0
    features:
      type:
        example/banner:1.0.0: compose/banner-plain:1.0.0
      name:
        loud-banner: compose/banner-loud:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: greeters-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: greeters
  manifest: greeters-manifest
  boundaries:
    app:
      directory: app
      components:
        greeter-default:
          infra:
            port: 8080
        greeter-special:
          infra:
            port: 8081
`)
	return catalogDir, releasePath
}

// TestARealizationChosenByInstanceNameIsPinnedAndVerified drives the pull, the
// build and the verifying pull over the fixture above, then removes each
// name-tier row in turn.
func TestARealizationChosenByInstanceNameIsPinnedAndVerified(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	catalogDir, rel := nameTierFixture(t, root)
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

	overrides := []struct {
		uri  string
		kind string
		site string
	}{
		{"compose/greeter-green:1.0.0", "recipe", "manifest.spec.mappings.components.name.greeter-special"},
		{"compose/banner-loud:1.0.0", "feature_recipe", "manifest.spec.mappings.features.name.loud-banner"},
	}
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	for _, o := range overrides {
		var found bool
		for _, e := range lf.Entities {
			if e.URI != o.uri {
				continue
			}
			found = true
			if strings.Join(e.ReferencedFrom, ",") != o.site {
				t.Errorf("row %q referenced_from = %v; want [%s]", o.uri, e.ReferencedFrom, o.site)
			}
			if e.Digest == "" || e.ResolvedVersion != "1.0.0" {
				t.Errorf("row %q pins (%q, %q); want version 1.0.0 and a digest", o.uri, e.ResolvedVersion, e.Digest)
			}
		}
		if !found {
			t.Fatalf("the pull recorded no row for %q, the realization %s binds", o.uri, o.site)
		}
	}

	if err := pull(true); err != nil {
		t.Fatalf("pull --verify over the lockfile the pull wrote must pass: %v", err)
	}
	if err := build(); err != nil {
		t.Fatalf("build over the lockfile the pull wrote must pass: %v", err)
	}
	compose := string(mustReadFile(t, filepath.Join(root, "dist", "app", "compose", "docker-compose.yaml")))
	for _, want := range []string{"image: greeter:green", "image: greeter:blue", "aggrega.ai/recipe-uri: greeter-green:1.0.0"} {
		if !strings.Contains(compose, want) {
			t.Errorf("the render does not carry %q, so the override did not reach it:\n%s", want, compose)
		}
	}

	if err := pull(false); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if second := mustReadFile(t, lockPath); !bytes.Equal(second, first) {
		t.Errorf("a second pull over an unchanged workspace rewrote the lockfile\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	for _, o := range overrides {
		t.Run("without the row for "+o.uri, func(t *testing.T) {
			without, _, err := lockfile.LoadLockfile(lockPath)
			if err != nil {
				t.Fatalf("load lockfile: %v", err)
			}
			kept := without.Entities[:0]
			for _, e := range without.Entities {
				if e.URI != o.uri {
					kept = append(kept, e)
				}
			}
			without.Entities = kept
			if err := lockfile.WriteLockfile(lockPath, without); err != nil {
				t.Fatalf("write the lockfile without the row: %v", err)
			}
			want := `entity "` + o.uri + `" (` + o.kind + `) not in lockfile (referenced from ` + o.site + `)`
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
					t.Fatalf("%s over a lockfile without the row for %q must halt as drift; got %v", step.name, o.uri, err)
				}
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: the refusal does not carry %q:\n%v", step.name, want, err)
				}
			}
			if err := pull(false); err != nil {
				t.Fatalf("pull to restore the row: %v", err)
			}
			if err := build(); err != nil {
				t.Errorf("build after the pull restored the row must pass: %v", err)
			}
		})
	}
}
