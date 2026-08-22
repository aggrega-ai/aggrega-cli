// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// publishedVerdictPlugin is a verdict-socket plugin served by a registry: the
// versioned entity URI an attachment names, the identity coordinate a record is
// addressed by, and the content digest of its .wasm layer.
type publishedVerdictPlugin struct {
	URI        string
	Coordinate string
	WasmDigest string
}

// publishVerdictPlugin publishes a verdict-socket plugin to the registry at host
// as the single OCI manifest a pull caches such a plugin from — the entity YAML as
// the manifest's config blob and the .wasm as an inline layer.
//
// It packs the manifest directly rather than going through the publish command,
// because the publish command cross-checks a guest's self-report against the
// entity and this fixture carries an import-free stub module instead of a compiled
// guest. What is under test here is the pull's record-writing, not publish.
//
// The repository path is derived through the shared location rule rather than
// spelled out, so this fixture cannot disagree with where the pull looks.
func publishVerdictPlugin(t *testing.T, host, socket, name, version string) publishedVerdictPlugin {
	t.Helper()

	entityYAML := verdictPluginEntityYAML(socket, name, version)
	reference := host + "/" + catalog.OCILocation("Plugin", socket, name)
	repo, err := oci.NewRemoteRepository(reference, http.DefaultClient)
	if err != nil {
		t.Fatalf("open registry repository %s: %v", reference, err)
	}
	if _, err := oci.PushPluginManifest(context.Background(), repo, version, []byte(entityYAML), headerOnlyWasm, nil); err != nil {
		t.Fatalf("publish verdict plugin %s: %v", reference, err)
	}

	coordinate := types.PluginCoordinate(socket, name)
	return publishedVerdictPlugin{
		URI:        coordinate + ":" + version,
		Coordinate: coordinate,
		WasmDigest: digest.ContentDigest(headerOnlyWasm),
	}
}

// verdictTrustWorkspace lays a workspace that resolves its Release from a local
// catalog and its one linter attachment from the registry at host, and returns the
// Release path and the published plugin.
//
// The plugin is deliberately absent from the local catalog: a plugin the local
// catalog serves is never written into the content cache, and the cache is the only
// place a resolution reads a plugin's identity from. So a locally-served plugin has
// no identity to record — which is the acquired-artifact rule showing up in the
// fixture rather than in the assertion.
func verdictTrustWorkspace(t *testing.T, host string) (root, releasePath string, published publishedVerdictPlugin) {
	t.Helper()
	root = t.TempDir()
	releasePath = authoringFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")
	published = publishVerdictPlugin(t, host, "linter", "trust-probe", "1.0.0")

	writeWorkspaceConfig(t, root, ""+
		"workspace:\n"+
		"  catalog_dirs:\n"+
		"    - "+catalogDir+"\n"+
		"default_registry: \""+host+"\"\n"+
		"plugins:\n"+
		"  linter:\n"+
		"    - uri: "+published.URI+"\n")
	return root, releasePath, published
}

// TestIntegration_VerdictTrustPullRecordsAnAcquiredPlugin is the end-to-end
// positive at the pull entrypoint: a pull that acquires a verdict-socket plugin
// from a registry creates the record, fills it with the identity a later
// resolution compares against, announces the creation once, and counts it on the
// completion line.
//
// The digest assertions are the load-bearing ones. A record carrying the plugin's
// OCI manifest digest instead of its .wasm layer digest is equally well-formed and
// equally schema-valid, and would halt every later resolution of a plugin nobody
// changed — so the test asserts which of the two was recorded rather than that
// something digest-shaped was.
func TestIntegration_VerdictTrustPullRecordsAnAcquiredPlugin(t *testing.T) {
	host := startRegistry(t, nil)
	root, releasePath, published := verdictTrustWorkspace(t, host)

	var stdout, stderr bytes.Buffer
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("pullRun: %v (stderr=%q)", err, stderr.String())
	}

	trust := trustRecordAt(t, releasePath)
	got, held := trust.Find(published.Coordinate)
	if !held {
		t.Fatalf("expected a record for the acquired plugin %s; record holds %v", published.Coordinate, recordedCoordinates(trust))
	}
	if got.Digest != published.WasmDigest {
		t.Errorf("recorded digest = %s; want the .wasm layer content digest %s", got.Digest, published.WasmDigest)
	}
	if got.ResolvedVersion != "1.0.0" {
		t.Errorf("recorded version = %q; want the version the entity declares, 1.0.0", got.ResolvedVersion)
	}
	if !strings.Contains(stderr.String(), pluginstrust.FileName) {
		t.Errorf("creating the record must be announced on the diagnostic sink; stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "trusted_plugins=1") {
		t.Errorf("the completion line must count the recorded identity; got %q", stdout.String())
	}

	// A second pull over the same workspace holds the record and announces nothing:
	// the creation notice is for creation.
	stderr.Reset()
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("second pullRun: %v (stderr=%q)", err, stderr.String())
	}
	if strings.Contains(stderr.String(), pluginstrust.FileName) {
		t.Errorf("the record already existed, so the second pull must not announce a creation; stderr = %q", stderr.String())
	}
	again, _ := trustRecordAt(t, releasePath).Find(published.Coordinate)
	if again.Digest != published.WasmDigest {
		t.Errorf("second pull changed the recorded digest to %s; want %s held", again.Digest, published.WasmDigest)
	}
}

// TestIntegration_VerdictTrustVerifyOnlyPullWritesNothing proves the split between
// reading each verdict plugin's identity and recording it. The caching walk that
// reads those identities runs on every pull, a verify-only one included, so the
// absence of a record here is attributable to the mode — the workspace HAS a
// recordable attachment, which is what makes the negative meaningful.
func TestIntegration_VerdictTrustVerifyOnlyPullWritesNothing(t *testing.T) {
	host := startRegistry(t, nil)
	root, releasePath, _ := verdictTrustWorkspace(t, host)

	// A verify-only run needs a lockfile to verify against, so seed one with an
	// ordinary pull and remove the record it wrote.
	var stdout, stderr bytes.Buffer
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("seeding pullRun: %v (stderr=%q)", err, stderr.String())
	}
	recordPath := pluginstrust.PathFor(releasePath)
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("precondition: the seeding pull must have written a record; stat err = %v", err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("remove the seeding pull's record: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		Verify:      true,
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("pullRun --verify: %v (stderr=%q)", err, stderr.String())
	}
	if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a verify-only pull must write no record at %s; stat err = %v", recordPath, err)
	}
}

// TestIntegration_VerdictTrustAutoPullBuildWritesTheRecord discharges the
// auto-pull criterion by invocation rather than by inspection: a build that runs
// the pull step writes the record, because it runs the pull path itself. A build
// whose lockfile is already present runs no pull step and writes nothing, which is
// the other half of the same property and is why the record only ever appears when
// an acquisition actually happened.
func TestIntegration_VerdictTrustAutoPullBuildWritesTheRecord(t *testing.T) {
	host := startRegistry(t, nil)
	root, releasePath, published := verdictTrustWorkspace(t, host)
	t.Chdir(root)

	recordPath := pluginstrust.PathFor(releasePath)
	if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("precondition: no record before the build; stat err = %v", err)
	}

	var diagBuf, outBuf bytes.Buffer
	if err := dispatchBuildWithDiag(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false,
		buildDiagOptions{}, buildRunOptions{autoPull: true, outDir: filepath.Join(root, "dist")}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --auto-pull: %v (diag=%q)", err, diagBuf.String())
	}

	trust := trustRecordAt(t, releasePath)
	if _, held := trust.Find(published.Coordinate); !held {
		t.Fatalf("build --auto-pull runs the pull path, so it must write the record; record holds %v", recordedCoordinates(trust))
	}

	// The other half: with the lockfile now present the build's own resolution path
	// runs and no pull does, so a record removed here stays removed.
	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("remove the record between builds: %v", err)
	}
	if err := dispatchBuildWithDiag(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false,
		buildDiagOptions{}, buildRunOptions{outDir: filepath.Join(root, "dist")}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build with a present lockfile: %v (diag=%q)", err, diagBuf.String())
	}
	if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a build that runs no pull step must write no record; stat err = %v", err)
	}
}
