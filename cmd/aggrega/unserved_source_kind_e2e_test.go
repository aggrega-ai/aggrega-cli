// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// writeSourceKindWorkspace writes an offline workspace whose one component is
// rendered by a Recipe sourcing a chart under the given kind, and returns the root
// and the Release's path.
//
// The workspace is offline so a fetch, were one attempted, would be refused with
// the offline condition rather than reaching anything: a row reporting that
// condition reached the fetch, and a row reporting the source-kind condition was
// refused before it.
func writeSourceKindWorkspace(t *testing.T, kind string) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\nauto_pull: true\n")
	writeFixture(t, filepath.Join(root, "catalog/component/test/svc/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(root, "catalog/recipe/helm/svc/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  source:
    kind: `+kind+`
    url: oci://127.0.0.1:9/charts/svc
    version: 1.0.0
  valuesMapping: |
    {}
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc: helm/svc:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	return root, filepath.Join(root, "release.yaml")
}

// TestAnUnservedSourceKindIsRefusedOnEverySurface drives one Recipe whose source
// names a kind nothing serves through the three surfaces that reach a Release's
// Recipes — validating the Release, pulling it and building it — and asserts each
// refuses it with the source-kind condition, naming the kind, before any fetch.
//
// The refusal arrives as the Recipe is read, so each surface reports the Recipe
// refused at load and leads with that refusal; the condition is read off the
// refusal the report promotes, and the head the terminal prints is read through
// the CLI's own formatter. The control is the same workspace naming a served kind:
// validation passes, and the pull and the build reach the fetch, which the offline
// workspace refuses — so the control fails for a different reason, and a row that
// reported the offline condition would show the source walk had been reached.
//
// INVARIANT: an unserved source kind is refused on every surface under the
// source-kind condition, naming the kind, before anything is fetched.
// CONSEQUENCE: were a surface to pass it, the author would meet the fault only at a
// pull, under a condition that names a template evaluation rather than the kind.
func TestAnUnservedSourceKindIsRefusedOnEverySurface(t *testing.T) {
	type surface struct {
		name string
		run  func(t *testing.T, root, release string) (error, string)
	}
	surfaces := []surface{
		{"validate --release", func(t *testing.T, root, release string) (error, string) {
			rw, err := workspaceconfig.Resolve(root, root)
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			var diag, out bytes.Buffer
			verr := validateRelease(context.Background(), rw, release, nil, compilerops.VerbosityDetailed, false, false, &diag, &out)
			return verr, diag.String()
		}},
		{"pull", func(t *testing.T, root, release string) (error, string) {
			var diag, out bytes.Buffer
			perr := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				ReleasePath: release,
				DiagOut:     &diag,
				Out:         &out,
			})
			return perr, diag.String()
		}},
		{"build", func(t *testing.T, root, release string) (error, string) {
			var diag, out bytes.Buffer
			berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				release, nil, false, &diag, &out)
			return berr, diag.String()
		}},
	}

	for _, s := range surfaces {
		t.Run(s.name+": a served kind (control)", func(t *testing.T) {
			root, release := writeSourceKindWorkspace(t, sourcekind.HelmOCIChart)
			t.Chdir(root)
			err, diag := s.run(t, root, release)
			if refusedForTheKind(err) {
				t.Fatalf("a served kind was refused for its kind: %v\n%s", err, diag)
			}
			if s.name == "validate --release" {
				if err != nil {
					t.Fatalf("validation refused a Recipe naming a served kind: %v\n%s", err, diag)
				}
				return
			}
			if !errors.Is(err, egress.ErrOfflineEgress()) {
				t.Errorf("want the offline refusal at the fetch, which shows the source walk was reached; got %v\n%s",
					err, diag)
			}
		})
		t.Run(s.name+": an unserved kind", func(t *testing.T) {
			root, release := writeSourceKindWorkspace(t, "nosuch")
			t.Chdir(root)
			err, diag := s.run(t, root, release)
			if err == nil {
				t.Fatalf("%s accepted a Recipe whose source names a kind nothing serves", s.name)
			}
			if !refusedForTheKind(err) {
				t.Fatalf("%s refused with %v; want the source-kind condition, promoted as the reason the "+
					"Recipe was refused at load\n%s", s.name, err, diag)
			}
			if errors.Is(err, egress.ErrOfflineEgress()) {
				t.Errorf("%s reached the fetch: %v", s.name, err)
			}
			head := formatCompilerError(err)
			for _, want := range []string{"E0186", `"nosuch"`} {
				if !strings.Contains(head, want) {
					t.Errorf("%s: the printed refusal does not carry %q:\n%s", s.name, want, head)
				}
			}
		})
	}
}

// refusedForTheKind reports whether err is a refusal led by the source-kind
// condition: carried on the chain itself, or as the refusal a skipped-at-load
// report promotes ahead of the resolution failure it explains.
func refusedForTheKind(err error) bool {
	if errors.Is(err, sourcekind.ErrUnknownKind()) {
		return true
	}
	var skipped *compilerops.SkipContextError
	if !errors.As(err, &skipped) {
		return false
	}
	refusal, promoted := skipped.PromotedRefusal()
	return promoted && errors.Is(refusal.Err, sourcekind.ErrUnknownKind())
}
