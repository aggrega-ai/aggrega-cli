// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// sourceCredentialUser and sourceCredentialSecret are the user and password the
// refused rows write into a source URL. No surface may print either.
const (
	sourceCredentialUser   = "deploy-bot"
	sourceCredentialSecret = "s3cr3t-in-a-source-url"
)

// writeSourceCredentialWorkspace writes an offline workspace whose one component
// is rendered by a Recipe whose source block is source, and returns the root, the
// Release's path and the Recipe's path.
//
// The workspace is offline so a fetch, were one attempted, would be refused with
// the offline condition rather than reaching anything: a row reporting that
// condition reached the fetch, and a row reporting the source-url condition was
// refused before it.
func writeSourceCredentialWorkspace(t *testing.T, source string) (root, release, recipe string) {
	t.Helper()
	root = t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\nauto_pull: true\n")
	writeFixture(t, filepath.Join(root, "catalog/component/test/svc/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	recipe = filepath.Join(root, "catalog/recipe/helm/svc/1.0.0/recipe.yaml")
	writeFixture(t, recipe, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  source:
`+source+`
  valuesMapping: |
    {}
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	release = filepath.Join(root, "release.yaml")
	writeFixture(t, release, `apiVersion: aggrega.ai/v1beta1
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
	return root, release, recipe
}

// sourceBlock is a structured source of kind, its url reached at hostPath over
// scheme, with credential written before the host when it is not empty.
func sourceBlock(kind, scheme, credential, hostPath string) string {
	url := scheme + "://" + hostPath
	if credential != "" {
		url = scheme + "://" + credential + "@" + hostPath
	}
	switch kind {
	case sourcekind.GitRepository:
		return "    kind: " + kind + "\n    url: " + url + "\n    ref: v1.0.0\n    module_subdir: charts/probe"
	case sourcekind.HelmHTTPSChart:
		return "    kind: " + kind + "\n    url: " + url + "\n    chart: probe\n    version: \"1.0.0\""
	default:
		return "    kind: " + kind + "\n    url: " + url + "\n    version: \"1.0.0\""
	}
}

// TestASourceURLCarryingACredentialIsRefusedOnEverySurface drives a Recipe whose
// source URL carries a credential, for each of the three source kinds fetched
// over HTTP, through the four command surfaces that read a Release's Recipes or
// the Recipe itself — validating the Release, pulling it, building it and pinning
// the Recipe — and asserts each refuses it with the source-url condition before
// any fetch, and that neither the user nor the password reaches anything the
// surface writes: its diagnostic stream, its output stream, the error it returns
// or the head the terminal prints for that error.
//
// The control is the same workspace with the credential removed: no surface
// refuses it for a credential, and the three that fetch reach the fetch, which the
// offline workspace refuses — so a row that reported the offline condition would
// show the fetch had been reached.
//
// INVARIANT: a source URL carrying a credential is refused on every surface under
// the source-url condition, before anything is fetched, and the credential is
// printed nowhere.
// CONSEQUENCE: were a surface to pass it, the credential would be sent and written
// into the lockfile, the lock beside the Recipe and the source cache.
//
// A planted defect makes this fail: have the source-url rule's predicate answer no
// credential for every URL.
func TestASourceURLCarryingACredentialIsRefusedOnEverySurface(t *testing.T) {
	type surface struct {
		name string
		run  func(t *testing.T, root, release, recipe string) (err error, streams string)
	}
	surfaces := []surface{
		{"validate --release", func(t *testing.T, root, release, _ string) (error, string) {
			rw, err := workspaceconfig.Resolve(root, root)
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			var diag, out bytes.Buffer
			verr := validateRelease(context.Background(), rw, release, nil, compilerops.VerbosityDetailed, false, false, &diag, &out)
			return verr, diag.String() + out.String()
		}},
		{"pull", func(t *testing.T, root, release, _ string) (error, string) {
			var diag, out bytes.Buffer
			perr := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				ReleasePath: release,
				DiagOut:     &diag,
				Out:         &out,
			})
			return perr, diag.String() + out.String()
		}},
		{"build", func(t *testing.T, root, release, _ string) (error, string) {
			var diag, out bytes.Buffer
			berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				release, nil, false, &diag, &out)
			return berr, diag.String() + out.String()
		}},
		{"pin", func(t *testing.T, root, _, recipe string) (error, string) {
			var out bytes.Buffer
			perr := pinRun(context.Background(), &out, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), recipe, false)
			return perr, out.String()
		}},
	}
	kinds := []struct {
		kind, scheme, hostPath string
	}{
		{sourcekind.HelmHTTPSChart, "http", "127.0.0.1:9/charts"},
		{sourcekind.ComposeHTTPSFragment, "http", "127.0.0.1:9/fragments/web.yaml"},
		{sourcekind.GitRepository, "https", "127.0.0.1:9/org/module.git"},
	}

	for _, k := range kinds {
		for _, s := range surfaces {
			t.Run(k.kind+", "+s.name+": no credential in the URL (control)", func(t *testing.T) {
				root, release, recipe := writeSourceCredentialWorkspace(t, sourceBlock(k.kind, k.scheme, "", k.hostPath))
				t.Chdir(root)
				err, streams := s.run(t, root, release, recipe)
				if refusedForTheCredential(err) {
					t.Fatalf("a source URL carrying no credential was refused for one: %v\n%s", err, streams)
				}
				switch s.name {
				case "validate --release":
					if err != nil {
						t.Fatalf("validation refused a Recipe whose source URL carries no credential: %v\n%s", err, streams)
					}
				case "pin":
				default:
					if !errors.Is(err, egress.ErrOfflineEgress()) {
						t.Errorf("want the offline refusal at the fetch, which shows the fetch was reached; got %v\n%s",
							err, streams)
					}
				}
			})
			t.Run(k.kind+", "+s.name+": a credential in the URL", func(t *testing.T) {
				credential := sourceCredentialUser + ":" + sourceCredentialSecret
				root, release, recipe := writeSourceCredentialWorkspace(t, sourceBlock(k.kind, k.scheme, credential, k.hostPath))
				t.Chdir(root)
				err, streams := s.run(t, root, release, recipe)
				if err == nil {
					t.Fatalf("%s accepted a Recipe whose source URL carries a credential\n%s", s.name, streams)
				}
				if !refusedForTheCredential(err) {
					t.Fatalf("%s refused with %v; want the source-url condition, promoted as the reason the "+
						"Recipe was refused at load\n%s", s.name, err, streams)
				}
				if errors.Is(err, egress.ErrOfflineEgress()) {
					t.Errorf("%s reached the fetch: %v", s.name, err)
				}
				head := formatCompilerError(err)
				for _, want := range []string{"E0361", "host 127.0.0.1", "netrc"} {
					if !strings.Contains(head, want) {
						t.Errorf("%s: the printed refusal does not carry %q:\n%s", s.name, want, head)
					}
				}
				for _, printed := range []string{head, err.Error(), streams} {
					for _, secret := range []string{sourceCredentialSecret, sourceCredentialUser} {
						if strings.Contains(printed, secret) {
							t.Errorf("%s printed %q from the source URL:\n%s", s.name, secret, printed)
						}
					}
				}
			})
		}
	}
}

// refusedForTheCredential reports whether err is a refusal led by the source-url
// condition: carried on the chain itself, or as the refusal a skipped-at-load
// report promotes ahead of the resolution failure it explains.
func refusedForTheCredential(err error) bool {
	if errors.Is(err, netrc.ErrSourceURLCarriesCredential()) {
		return true
	}
	var skipped *compilerops.SkipContextError
	if !errors.As(err, &skipped) {
		return false
	}
	refusal, promoted := skipped.PromotedRefusal()
	return promoted && errors.Is(refusal.Err, netrc.ErrSourceURLCarriesCredential())
}
