// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// `aggrega pull --verify` answers one question: would running `aggrega pull` now
// change the lockfile? A pull honours the pins the lockfile records, so the tests
// here hold the verifying pull to the same resolution, against an in-process
// registry, where honouring a pin is observable:
//
//   - a pipeline plugin's .wasm is cached in a slot only its pin addresses, so an
//     offline verify can find it, and an online one fetches nothing for it;
//   - a bare reference whose registry has published a newer version since the pull
//     is held at its pin, which is not drift, because the pull holds it too;
//   - a pinned version the registry now serves different bytes under halts the
//     verify with the condition that halts the pull.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// verifyRegistryWorkspace runs the verifying pull over the workspace at root,
// whose configuration carries its catalog directories and registry, and returns
// what it printed on standard output together with its error.
func verifyRegistryWorkspace(t *testing.T, root string) (string, error) {
	t.Helper()
	var out, diag bytes.Buffer
	err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: "release.yaml",
		Verify:      true,
		Out:         &out,
		DiagOut:     &diag,
	})
	if err != nil {
		t.Logf("verify diagnostics:\n%s", diag.String())
	}
	return out.String(), err
}

func readForTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestIntegration_RegistryPipelinePlugin_OfflineVerifyAfterPull drives an offline
// verify over the lockfile a pull has just written for a Release whose one
// registry-published dependency is a pipeline plugin. The plugin's .wasm is in the
// cache, in the slot its pin addresses, so the offline verify finds it and reports
// a match. A verify that resolved without the pins could not address that slot: it
// would report the plugin as no longer used by the Release and exit non-zero over a
// lockfile that is correct.
func TestIntegration_RegistryPipelinePlugin_OfflineVerifyAfterPull(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	f := writeRegistryPluginFixture(t, startRegistry(t, nil), pluginPath, std, true)
	t.Chdir(f.root)

	f.assertPluginPinned(t, f.pull(t))
	lockPath := lockfile.PathFor("release.yaml")
	pulled := readForTest(t, lockPath)

	f.configure(t, "auto_pull: false\noffline: true")
	out, err := verifyRegistryWorkspace(t, f.root)
	if err != nil {
		t.Fatalf("an offline verify refused the lockfile the pull just wrote: %v", err)
	}
	if !strings.Contains(out, "Verify: lockfile") {
		t.Errorf("the verify returned no error but printed no verdict: %q", out)
	}
	if !bytes.Equal(pulled, readForTest(t, lockPath)) {
		t.Error("the verify rewrote the lockfile")
	}
}

// TestIntegration_RegistryPipelinePlugin_OnlineVerifyAsksNothingOfTheCachedPlugin
// drives the same pair online, with every request the registry serves recorded, and
// holds the verify to asking the registry nothing about the plugin: its .wasm is
// cached under the pin, so the resolution a pull performs reads it from there, and
// no fetch and no signature lookup reaches the plugin's repository.
//
// The recorder is calibrated on the pull first: the pull must be seen fetching the
// .wasm by its content digest, or an empty count during the verify would say only
// that the recorder saw nothing. The count is taken at the registry rather than
// through a puller of the test's own, because a wrapper would have to carry every
// optional capability the production puller offers, and one that dropped a
// capability would change the resolution under test.
func TestIntegration_RegistryPipelinePlugin_OnlineVerifyAsksNothingOfTheCachedPlugin(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	var log requestLog
	f := writeRegistryPluginFixture(t, startRegistry(t, log.wrap), pluginPath, std, true)
	t.Chdir(f.root)

	pluginName := strings.TrimPrefix(f.artifactURI, pipelineStepPluginType+"/")
	repo := "/v2/" + catalog.OCILocation("Plugin", pipelineStepPluginType, pluginName) + "/"
	wasmFetch := "GET " + repo + "blobs/" + f.wasmDigest

	pullMark := log.len()
	f.assertPluginPinned(t, f.pull(t))
	fetched := false
	for _, r := range log.since(pullMark) {
		if r == wasmFetch {
			fetched = true
		}
	}
	if !fetched {
		t.Fatalf("the pull was not seen fetching the plugin's .wasm (%q), so the count below would prove nothing; the pull asked for %v",
			wasmFetch, log.since(pullMark))
	}

	verifyMark := log.len()
	if _, err := verifyRegistryWorkspace(t, f.root); err != nil {
		t.Fatalf("an online verify refused the lockfile the pull just wrote: %v", err)
	}
	var asked []string
	for _, r := range log.since(verifyMark) {
		if strings.Contains(r, repo) {
			asked = append(asked, r)
		}
	}
	if len(asked) > 0 {
		t.Errorf("the verify asked the registry about the plugin its cache holds under the pin: %v", asked)
	}
}

// bareReferenceComponent is a registry-published Component the fixture's Aggregate
// names by bare identity; description tells two publishes of it apart.
func bareReferenceComponent(version, description string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: remote/db
  version: "` + version + `"
  description: ` + description + `
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 5432 }
`
}

// bareReferenceFixture is a workspace whose Aggregate names a registry-published
// Component by bare identity — no version — and whose Recipe for it is authored in
// the catalog directory the workspace reads.
type bareReferenceFixture struct {
	root string
	host string
	pubs int
}

func writeBareReferenceFixture(t *testing.T, host string) *bareReferenceFixture {
	t.Helper()
	root := t.TempDir()
	cat := filepath.Join(root, "catalog")
	writeFixtureEntity(t, cat, "recipe/helm/remote-db/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: remote-db
  version: "1.0.0"
spec:
  capability: helm
  implements: remote/db
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      db:
        image: db:latest
        ports:
          pg: 5432
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    db:
      type: remote/db
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        remote/db: helm/remote-db:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-dev
  environment: development
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  components:
    db: {}
`)
	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs:\n    - \""+cat+"\"\ndefault_registry: \""+host+"\"\noffline: false")
	return &bareReferenceFixture{root: root, host: host}
}

// publish pushes a Component through the real publish path and returns the
// document it published.
func (f *bareReferenceFixture) publish(t *testing.T, version, description string) []byte {
	t.Helper()
	f.pubs++
	doc := bareReferenceComponent(version, description)
	path := filepath.Join(t.TempDir(), "publish-"+strconv.Itoa(f.pubs), "component.yaml")
	writeFixture(t, path, doc)
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{}), EntityPath: path, Registry: f.host}); err != nil {
		t.Fatalf("publish remote/db %s: %v", version, err)
	}
	return []byte(doc)
}

// pull runs a default pull, or a targeted upgrade of the named references.
func (f *bareReferenceFixture) pull(t *testing.T, upgradeTargets ...string) error {
	t.Helper()
	var out, diag bytes.Buffer
	err := pullRun(context.Background(), pullConfig{
		ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}),
		ReleasePath:    "release.yaml",
		Upgrade:        len(upgradeTargets) > 0,
		UpgradeTargets: upgradeTargets,
		Out:            &out,
		DiagOut:        &diag,
	})
	if err != nil {
		t.Logf("pull diagnostics:\n%s", diag.String())
	}
	return err
}

// pinned returns the lockfile row the pull wrote for the bare reference.
func (f *bareReferenceFixture) pinned(t *testing.T) lockfile.Entity {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	for _, e := range lf.Entities {
		if e.URI == "remote/db" {
			return e
		}
	}
	t.Fatalf("the lockfile pins no row for remote/db: %+v", lf.Entities)
	return lockfile.Entity{}
}

// withdrawTag removes a published version's tag from the registry, so the same
// version can be published again over different bytes.
func (f *bareReferenceFixture) withdrawTag(t *testing.T, version string) {
	t.Helper()
	url := "http://" + f.host + "/v2/" + catalog.OCILocation("Component", "", "remote/db") + "/manifests/" + version
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("build the tag withdrawal: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("withdraw %s: %v", url, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("withdraw %s: status %d", url, resp.StatusCode)
	}
}

// TestIntegration_PullVerifyHoldsABareReferenceAtItsPin publishes a newer version of
// a Component the Aggregate names by bare identity, after the pull pinned it. A pull
// holds the pin, so it would change nothing, and the verify reports no drift.
//
// The two halves of that claim are each driven rather than inferred: the default
// pull after the verify leaves the pin where it was, and a targeted upgrade of the
// reference moves it to the newer version — which is the calibration that the
// newer version really is served, so the verify's match is not the product of a
// publish that never landed.
func TestIntegration_PullVerifyHoldsABareReferenceAtItsPin(t *testing.T) {
	f := writeBareReferenceFixture(t, startRegistry(t, nil))
	t.Chdir(f.root)
	f.publish(t, "1.0.0", "a registry database")
	for i := 1; i <= 2; i++ {
		if err := f.pull(t); err != nil {
			t.Fatalf("pull %d: %v", i, err)
		}
	}
	if got := f.pinned(t).ResolvedVersion; got != "1.0.0" {
		t.Fatalf("the pull pinned remote/db at %q; want 1.0.0", got)
	}
	lockPath := lockfile.PathFor("release.yaml")
	pulled := readForTest(t, lockPath)

	f.publish(t, "1.0.1", "a registry database, next release")

	if _, err := verifyRegistryWorkspace(t, f.root); err != nil {
		t.Fatalf("the verify reported drift for a pin a pull holds: %v", err)
	}
	if !bytes.Equal(pulled, readForTest(t, lockPath)) {
		t.Fatal("the verify rewrote the lockfile")
	}

	if err := f.pull(t); err != nil {
		t.Fatalf("the pull the verify answers for: %v", err)
	}
	if !bytes.Equal(stripGeneratedAt(pulled), stripGeneratedAt(readForTest(t, lockPath))) {
		t.Errorf("the default pull changed the lockfile the verify said it would leave alone:\nbefore:\n%s\nafter:\n%s",
			pulled, readForTest(t, lockPath))
	}

	if err := f.pull(t, "remote/db"); err != nil {
		t.Fatalf("targeted upgrade of remote/db: %v", err)
	}
	if got := f.pinned(t).ResolvedVersion; got != "1.0.1" {
		t.Fatalf("a targeted upgrade pinned remote/db at %q; want 1.0.1 — the registry is not serving the newer version, so the match above proves nothing", got)
	}
}

// TestIntegration_PullVerifyHaltsWhereAPullHaltsOnRepublishedBytes re-publishes the
// pinned version of a bare-referenced Component over different bytes and clears the
// cache, so the resolution has to ask the registry. A pull halts there rather than
// re-pinning the changed bytes, and the verify meets the same halt, under the same
// condition and in the same words, instead of reporting it as drift.
func TestIntegration_PullVerifyHaltsWhereAPullHaltsOnRepublishedBytes(t *testing.T) {
	f := writeBareReferenceFixture(t, startRegistry(t, nil))
	t.Chdir(f.root)
	original := f.publish(t, "1.0.0", "a registry database")
	for i := 1; i <= 2; i++ {
		if err := f.pull(t); err != nil {
			t.Fatalf("pull %d: %v", i, err)
		}
	}
	if got := f.pinned(t).Digest; got != digest.ContentDigest(original) {
		t.Fatalf("the pull pinned remote/db at %s; want the published document's digest %s", got, digest.ContentDigest(original))
	}
	lockPath := lockfile.PathFor("release.yaml")
	pulled := readForTest(t, lockPath)

	f.withdrawTag(t, "1.0.0")
	republished := f.publish(t, "1.0.0", "a registry database, re-published under the same version")
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root})
	if err := os.RemoveAll(filepath.Join(f.root, rw.Config().ResolvedArtifactCacheDir())); err != nil {
		t.Fatalf("clear the cache: %v", err)
	}

	want := `pinned entity "remote/db": registry now serves digest ` + digest.ContentDigest(republished) +
		` for the pinned version, not the pinned digest ` + digest.ContentDigest(original)
	for _, run := range []struct {
		name string
		run  func() error
	}{
		{"the verify", func() error { _, err := verifyRegistryWorkspace(t, f.root); return err }},
		{"the pull it answers for", func() error { return f.pull(t) }},
	} {
		err := run.run()
		if !errors.Is(err, oci.ErrFetchIntegrity()) {
			t.Errorf("%s: want the fetch-integrity condition; got %v", run.name, err)
			continue
		}
		if errors.Is(err, lockfile.ErrLockfileDrift()) {
			t.Errorf("%s: the halt also reads as drift: %v", run.name, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the halt does not name what the registry serves against the pin; want %q in:\n%v", run.name, want, err)
		}
		if !bytes.Equal(pulled, readForTest(t, lockPath)) {
			t.Errorf("%s rewrote the lockfile", run.name)
		}
	}
}
