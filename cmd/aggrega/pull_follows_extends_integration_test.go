// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// extendsBasePublisher is a publisher tree holding a two-link chain of published
// Components — a base that itself extends a root — and the Recipe written against
// the root, which renders a ConfigMap from a local chart. A Recipe written against
// a base implements everything extending it, at any depth.
var extendsBasePublisher = map[string]string{
	"component/acme/datastore/root-db/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/datastore/root-db
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: datastore
spec:
  schema:
    values:
      greeting: { type: string, default: hello }
`,
	"component/acme/datastore/base-db/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/datastore/base-db
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: datastore
spec:
  extends: acme/datastore/root-db:1.0.0
  schema: inherited
`,
	"recipe/helm/base-db/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: base-db
  version: 1.0.0
spec:
  capability: helm
  implements: acme/datastore/root-db:1.0.0
  source: ./chart
  valuesMapping: |
    {"greeting": "{{ .values.greeting }}"}
`,
	"recipe/helm/base-db/1.0.0/chart/Chart.yaml": "apiVersion: v2\nname: base-db\nversion: 0.1.0\n",
	"recipe/helm/base-db/1.0.0/chart/templates/cm.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-greeting
data:
  greeting: {{ .Values.greeting | quote }}
`,
}

// extendsWrapperConsumer is a workspace whose own Component extends the
// published base, and whose Manifest maps only that Component — the base is named
// by nothing but the wrapper's `extends`.
var extendsWrapperConsumer = map[string]string{
	"catalog/db/component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: local/datastore/db
  version: 0.1.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: datastore
spec:
  extends: acme/datastore/base-db:1.0.0
  schema: inherited
`,
	"aggregate.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    db:
      type: local/datastore/db
      values:
        greeting: ciao
`,
	"manifest.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        local/datastore/db:0.1.0: helm/base-db:1.0.0
`,
	"release.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: dev
  environment: development
  version: "1.0.0"
spec:
  aggregate: aggregate
  manifest: manifest
  components:
    db: {}
`,
}

// TestIntegration_PullFollowsAComponentsExtendsAndPlainValidateReadsWhatItPinned
// publishes a two-link chain — a base Component extending a root Component — and
// the Recipe written against the root, and drives a workspace whose own Component
// extends that base, with only the extending Component mapped, through a
// whole-catalog validation and an inspection, a pull, a build, and a second
// whole-catalog validation and inspection.
//
// Before the pull nothing on this machine holds the chain, so the whole-catalog
// validation and the inspection both refuse the extends link and name the pull that
// fetches it. The pull then fetches the whole chain and pins each link — under its
// own kind, referenced from the `extends` of the entity that names it — and the
// build reads the chain from there and writes the rendered file. The second
// validation and the inspection read the chain the pull pinned, from the cache, as
// the build did, and pass.
func TestIntegration_PullFollowsAComponentsExtendsAndPlainValidateReadsWhatItPinned(t *testing.T) {
	host := startRegistry(t, nil)

	pub := t.TempDir()
	for rel, body := range extendsBasePublisher {
		writeFixture(t, filepath.Join(pub, filepath.FromSlash(rel)), body)
	}
	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	for _, entity := range []string{
		"component/acme/datastore/root-db/1.0.0/component.yaml",
		"component/acme/datastore/base-db/1.0.0/component.yaml",
		"recipe/helm/base-db/1.0.0/recipe.yaml",
	} {
		if err := pushRun(t.Context(), pushConfig{Workspace: publisher, EntityPath: filepath.Join(pub, entity), Registry: host, PinSources: boolPtr(true)}); err != nil {
			t.Fatalf("push %s: %v", entity, err)
		}
	}

	ws := t.TempDir()
	for rel, body := range extendsWrapperConsumer {
		writeFixture(t, filepath.Join(ws, filepath.FromSlash(rel)), body)
	}
	writeWorkspaceConfig(t, ws, "workspace: {}\ndefault_registry: \""+host+"\"\noffline: false")
	t.Chdir(ws)
	consumer := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})

	// The whole-catalog validation's single pass, as `aggrega validate` with no
	// argument runs it, with its diagnostics captured.
	plainValidate := func(t *testing.T) (string, error) {
		t.Helper()
		passes, err := serve.WholeCatalogPasses(consumer, nil)
		if err != nil {
			t.Fatalf("whole-catalog passes: %v", err)
		}
		if len(passes) != 1 || passes[0].NamesReleases() {
			t.Fatalf("this workspace gives no Release directories of its own, so it runs one pass; got %+v", passes)
		}
		pins, err := serve.CatalogPassPins(consumer, passes[0])
		if err != nil {
			t.Fatalf("pins of the workspace's lockfiles: %v", err)
		}
		var diag, out bytes.Buffer
		err = validateCatalogWide(t.Context(), consumer.Root(), consumer.Config().ResolvedArtifactCacheDir(), passes[0].Dirs, passes[0].Named, pins, &diag, &out)
		if fdErr := validateCatalogWideFromWorkspace(t.Context(), consumer, nil); (fdErr == nil) != (err == nil) {
			t.Fatalf("`aggrega validate` reached verdict %v where its single pass reached %v", fdErr, err)
		}
		return diag.String(), err
	}

	// The inspection of the extending Component, as `aggrega inspect` runs it.
	inspectWrapper := func(t *testing.T) (string, error) {
		t.Helper()
		var buf bytes.Buffer
		err := inspectCatalogEntity(context.Background(), consumer, "local/datastore/db", nil, testInspectOptions(&buf))
		return buf.String(), err
	}

	t.Run("before the pull, validation refuses the base and names the pull", func(t *testing.T) {
		diag, err := plainValidate(t)
		if err == nil {
			t.Fatal("a base nothing on this machine holds must refuse the whole-catalog validation")
		}
		for _, want := range []string{"local/datastore/db extends acme/datastore/base-db:1.0.0", "aggrega pull --release"} {
			if !strings.Contains(diag, want) {
				t.Errorf("the refusal does not carry %q:\n%s", want, diag)
			}
		}
	})

	t.Run("before the pull, inspection refuses the base and names the pull", func(t *testing.T) {
		_, err := inspectWrapper(t)
		if err == nil {
			t.Fatal("a base nothing on this machine holds must refuse the inspection of the Component extending it")
		}
		for _, want := range []string{"local/datastore/db extends acme/datastore/base-db:1.0.0", "aggrega pull --release"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not carry %q", err.Error(), want)
			}
		}
	})

	if err := pullRun(context.Background(), pullConfig{ws: consumer, ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	t.Run("the pull pins every link of the chain under its own kind", func(t *testing.T) {
		lf, _, err := lockfile.Load(ws, filepath.Join(ws, "release.lock"))
		if err != nil {
			t.Fatalf("load the lockfile the pull wrote: %v", err)
		}
		for _, link := range []struct{ uri, from string }{
			{"acme/datastore/base-db:1.0.0", "component(local/datastore/db:0.1.0).spec.extends"},
			{"acme/datastore/root-db:1.0.0", "component(acme/datastore/base-db:1.0.0).spec.extends"},
		} {
			key := closure.EntityKey{Kind: closure.KindComponent, URI: link.uri}
			i := slices.IndexFunc(lf.Entities, func(e lockfile.Entity) bool { return e.Key() == key })
			if i < 0 {
				t.Errorf("the lockfile pins no %s; it pins %+v", key.Quoted(), lf.Entities)
				continue
			}
			row := lf.Entities[i]
			if row.ResolvedVersion != "1.0.0" || row.Digest == "" || row.ManifestDigest == "" {
				t.Errorf("the row for %s = %+v; want version 1.0.0 with the content and manifest digests a registry pin records", link.uri, row)
			}
			if !slices.Contains(row.ReferencedFrom, link.from) {
				t.Errorf("the row for %s is referenced from %q; want %s", link.uri, row.ReferencedFrom, link.from)
			}
		}
	})

	t.Run("the build reads the pinned base and writes its file", func(t *testing.T) {
		if err := dispatchBuild(context.Background(), consumer, "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("build: %v", err)
		}
		var rendered strings.Builder
		if err := filepath.WalkDir(filepath.Join(ws, "dist"), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, rerr := os.ReadFile(path)
			rendered.Write(data)
			return rerr
		}); err != nil {
			t.Fatalf("read rendered output: %v", err)
		}
		if !strings.Contains(rendered.String(), `greeting: "ciao"`) {
			t.Errorf("the rendered output does not carry the developer's value through the base's Recipe:\n%s", rendered.String())
		}
	})

	t.Run("after the pull, validation reads the base the pull pinned", func(t *testing.T) {
		if diag, err := plainValidate(t); err != nil {
			t.Fatalf("a base the pull pinned must resolve as the build resolves it; got %v\n%s", err, diag)
		}
	})

	t.Run("after the pull, inspection reads the base the pull pinned", func(t *testing.T) {
		if out, err := inspectWrapper(t); err != nil {
			t.Fatalf("a base the pull pinned must resolve as the build resolves it; got %v\n%s", err, out)
		}
	})
}
