// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
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

	entityYAML := verdicttest.PluginEntityYAML(socket, name, version)
	reference := host + "/" + catalog.OCILocation("Plugin", socket, name)
	repo, err := oci.NewRemoteRepository(reference, http.DefaultClient)
	if err != nil {
		t.Fatalf("open registry repository %s: %v", reference, err)
	}
	if _, err := oci.PushPluginManifest(context.Background(), repo, version, []byte(entityYAML), verdicttest.HeaderOnlyWasm, nil); err != nil {
		t.Fatalf("publish verdict plugin %s: %v", reference, err)
	}

	coordinate := types.PluginCoordinate(socket, name)
	return publishedVerdictPlugin{
		URI:        coordinate + ":" + version,
		Coordinate: coordinate,
		WasmDigest: digest.ContentDigest(verdicttest.HeaderOnlyWasm),
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
	published = publishVerdictPlugin(t, host, "linter", "trust-probe", "1.0.0")
	writeVerdictAttachments(t, root, host, published.URI)
	return root, releasePath, published
}

// writeVerdictAttachments rewrites the workspace configuration so it attaches
// exactly the linter references given, and nothing else about the workspace
// changes.
//
// Rewriting the whole file rather than patching it is what lets a test DETACH a
// plugin: passing no reference leaves a configuration that names no verdict
// attachment at all, which is the whole-gate removal the record exists to catch.
func writeVerdictAttachments(t *testing.T, root, host string, uris ...string) {
	t.Helper()
	body := "" +
		"workspace:\n" +
		"  catalog_dirs:\n" +
		"    - " + filepath.Join(root, "catalog") + "\n" +
		"default_registry: \"" + host + "\"\n"
	if len(uris) > 0 {
		body += "plugins:\n  linter:\n"
		for _, uri := range uris {
			body += "    - uri: " + uri + "\n"
		}
	}
	writeWorkspaceConfig(t, root, body)
}

// readRecord returns the record's bytes beside releasePath, or "" when there is no
// file. Tests log it before and after an act, so the proof of what a command did
// to the committed file is the file itself rather than a derived assertion.
func readRecord(t *testing.T, releasePath string) string {
	t.Helper()
	data, err := os.ReadFile(pluginstrust.PathFor(releasePath))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read record beside %s: %v", releasePath, err)
	}
	return string(data)
}

// buildAt runs the real build over the workspace and returns its error, which is
// how these tests observe the dropped-gate refusal: at the command a user runs,
// not at the predicate underneath it.
func buildAt(t *testing.T, root, releasePath string) error {
	t.Helper()
	var diagBuf, outBuf bytes.Buffer
	return dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false,
		buildDiagOptions{}, buildops.RunOptions{OutDir: filepath.Join(root, "dist")}, &diagBuf, &outBuf)
}

// pullAt runs the real pull over the workspace with the flags given.
func pullAt(t *testing.T, root, releasePath string, cfg pullConfig) error {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cfg.ws = resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	cfg.ReleasePath = releasePath
	cfg.DiagOut = &stderr
	cfg.Out = &stdout
	err := pullRun(cfg)
	if err != nil {
		return fmt.Errorf("%w (stderr=%q)", err, stderr.String())
	}
	return nil
}

// TestIntegration_VerdictTrustFullRefreshPrunesTheLastDetachedGate is the worked
// round trip for the case that has its own code path: the workspace detaches its
// ONLY verdict attachment, so the pull that has to clear the record resolves
// nothing at all. That path is not exercised by the some-remain case below — one
// crosses a resolution set that is empty and the other does not — so neither is
// evidence for the other and both are written out.
//
// The sequence is the criterion's own: record, detach, show the build refusing,
// show a plain pull refusing to help, run the explicit upgrade, show the entry
// gone and the build silent. The record's bytes are logged at each step, because
// what a command did to a committed file is a claim about the file.
func TestIntegration_VerdictTrustFullRefreshPrunesTheLastDetachedGate(t *testing.T) {
	host := startRegistry(t, nil)
	root, releasePath, published := verdictTrustWorkspace(t, host)
	t.Chdir(root)

	if err := pullAt(t, root, releasePath, pullConfig{}); err != nil {
		t.Fatalf("seeding pullRun: %v", err)
	}
	recorded := readRecord(t, releasePath)
	t.Logf("record after the seeding pull:\n%s", recorded)
	if _, held := verdicttest.TrustRecordAt(t, releasePath).Find(published.Coordinate); !held {
		t.Fatalf("precondition: the seeding pull must record %s", published.Coordinate)
	}
	if err := buildAt(t, root, releasePath); err != nil {
		t.Fatalf("precondition: the build must succeed while the gate is attached; got %v", err)
	}

	// Detach the only verdict attachment. Nothing about the rendered output changes,
	// which is exactly why the record has to speak up.
	writeVerdictAttachments(t, root, host)

	err := buildAt(t, root, releasePath)
	if !errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Fatalf("a build over a record holding a gate nothing attaches must refuse with the dropped-gate condition; got %v", err)
	}
	if !strings.Contains(err.Error(), published.Coordinate) {
		t.Errorf("the refusal must name the dropped plugin; got %v", err)
	}
	// The remedy it prints has to be the one that works, so the test reads it.
	wantCommand := "aggrega pull --upgrade --release " + releasePath + " " + published.Coordinate
	if !strings.Contains(err.Error(), wantCommand) {
		t.Errorf("the refusal must print the explicit upgrade verbatim;\nwant it to contain %q\ngot %v", wantCommand, err)
	}

	// A plain pull must NOT clear it. This is the criterion that protects the guard:
	// if the trigger is ever widened, the record changes here and the next build goes
	// quiet on a gate nobody re-attached.
	if err := pullAt(t, root, releasePath, pullConfig{}); err != nil {
		t.Fatalf("a plain pull over a detached gate must still complete; got %v", err)
	}
	if got := readRecord(t, releasePath); got != recorded {
		t.Fatalf("a plain pull must leave the record byte-identical;\nbefore:\n%s\nafter:\n%s", recorded, got)
	}
	if err := buildAt(t, root, releasePath); !errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Fatalf("after a plain pull the build must still refuse; got %v", err)
	}

	// The explicit upgrade, which is the sanctioned way out.
	if err := pullAt(t, root, releasePath, pullConfig{Upgrade: true}); err != nil {
		t.Fatalf("the explicit full refresh must complete; got %v", err)
	}
	after := readRecord(t, releasePath)
	t.Logf("record after `aggrega pull --upgrade`:\n%s", after)
	if after == "" {
		t.Fatal("the record must be emptied rather than deleted: an emptied record is read, and a deleted one is a first-run state again")
	}
	// It reads back through the loader every consumer uses, so an emptied record that
	// no longer satisfies its schema fails here.
	emptied := verdicttest.TrustRecordAt(t, releasePath)
	if len(emptied.Plugins) != 0 {
		t.Fatalf("the emptied record still holds %v", verdicttest.RecordedCoordinates(emptied))
	}
	if err := buildAt(t, root, releasePath); err != nil {
		t.Fatalf("with the entry gone the build must go silent; got %v", err)
	}
}

// TestIntegration_VerdictTrustFullRefreshPrunesOnlyTheDetachedOne is the
// some-remain half of the round trip: one of two attachments is detached, so the
// pull that clears the record still resolves something and never crosses the
// empty-resolution path the test above covers.
//
// The remaining entry is the discriminating assertion. A prune that dropped every
// entry it could not match would pass the "the detached one is gone" check and
// silently delete a gate that is still attached.
func TestIntegration_VerdictTrustFullRefreshPrunesOnlyTheDetachedOne(t *testing.T) {
	host := startRegistry(t, nil)
	root := t.TempDir()
	releasePath := authoringFixture(t, root)
	t.Chdir(root)

	kept := publishVerdictPlugin(t, host, "linter", "kept-probe", "1.0.0")
	detached := publishVerdictPlugin(t, host, "linter", "detached-probe", "1.0.0")
	writeVerdictAttachments(t, root, host, kept.URI, detached.URI)

	if err := pullAt(t, root, releasePath, pullConfig{}); err != nil {
		t.Fatalf("seeding pullRun: %v", err)
	}
	t.Logf("record after the seeding pull:\n%s", readRecord(t, releasePath))
	seeded := verdicttest.TrustRecordAt(t, releasePath)
	for _, want := range []string{kept.Coordinate, detached.Coordinate} {
		if _, held := seeded.Find(want); !held {
			t.Fatalf("precondition: the seeding pull must record %s; it holds %v", want, verdicttest.RecordedCoordinates(seeded))
		}
	}

	writeVerdictAttachments(t, root, host, kept.URI)

	err := buildAt(t, root, releasePath)
	if !errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Fatalf("a build over a record holding one dropped gate must refuse; got %v", err)
	}
	if !strings.Contains(err.Error(), detached.Coordinate) {
		t.Errorf("the refusal must name the dropped plugin; got %v", err)
	}
	if strings.Contains(err.Error(), kept.Coordinate) {
		t.Errorf("the refusal must not name the plugin that is still attached; got %v", err)
	}

	if err := pullAt(t, root, releasePath, pullConfig{Upgrade: true}); err != nil {
		t.Fatalf("the explicit full refresh must complete; got %v", err)
	}
	t.Logf("record after `aggrega pull --upgrade`:\n%s", readRecord(t, releasePath))
	pruned := verdicttest.TrustRecordAt(t, releasePath)
	if _, held := pruned.Find(detached.Coordinate); held {
		t.Errorf("the detached gate's entry must be gone; record holds %v", verdicttest.RecordedCoordinates(pruned))
	}
	if _, held := pruned.Find(kept.Coordinate); !held {
		t.Errorf("the attached gate's entry must remain; record holds %v", verdicttest.RecordedCoordinates(pruned))
	}
	if err := buildAt(t, root, releasePath); err != nil {
		t.Fatalf("with the stale entry gone the build must go silent; got %v", err)
	}
}

// TestIntegration_VerdictTrustTargetedUpgradePrunesOnlyItsTarget holds the narrower
// of the two sanctioned invocations to what it named. With both recorded plugins
// detached, an upgrade naming one drops that one and leaves the other's entry
// standing, so the build keeps refusing on the entry nobody asked about.
//
// Holding every pin it did not name is what a targeted upgrade means, so dropping
// more than was named would exceed the request — and the residual refusal is what
// proves the restraint rather than a comparison that never ran.
func TestIntegration_VerdictTrustTargetedUpgradePrunesOnlyItsTarget(t *testing.T) {
	host := startRegistry(t, nil)
	root := t.TempDir()
	releasePath := authoringFixture(t, root)
	t.Chdir(root)

	named := publishVerdictPlugin(t, host, "linter", "named-probe", "1.0.0")
	unnamed := publishVerdictPlugin(t, host, "linter", "unnamed-probe", "1.0.0")
	writeVerdictAttachments(t, root, host, named.URI, unnamed.URI)

	if err := pullAt(t, root, releasePath, pullConfig{}); err != nil {
		t.Fatalf("seeding pullRun: %v", err)
	}
	t.Logf("record after the seeding pull:\n%s", readRecord(t, releasePath))

	// Both gates go, so the only thing separating the two entries below is the
	// target the upgrade named.
	writeVerdictAttachments(t, root, host)
	err := buildAt(t, root, releasePath)
	if !errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Fatalf("a build over a record holding two dropped gates must refuse; got %v", err)
	}
	for _, want := range []string{named.Coordinate, unnamed.Coordinate} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name every dropped gate; missing %s in %v", want, err)
		}
	}

	// The target is the recorded COORDINATE, which is the only string left for the
	// reader to name — the plugin is attached nowhere. Accepting it is what makes the
	// command the refusal prints runnable.
	if err := pullAt(t, root, releasePath, pullConfig{Upgrade: true, UpgradeTargets: []string{named.Coordinate}}); err != nil {
		t.Fatalf("a targeted upgrade naming a recorded-but-detached plugin must be accepted and complete; got %v", err)
	}
	t.Logf("record after `aggrega pull --upgrade %s`:\n%s", named.Coordinate, readRecord(t, releasePath))

	pruned := verdicttest.TrustRecordAt(t, releasePath)
	if _, held := pruned.Find(named.Coordinate); held {
		t.Errorf("the named target's entry must be gone; record holds %v", verdicttest.RecordedCoordinates(pruned))
	}
	if _, held := pruned.Find(unnamed.Coordinate); !held {
		t.Fatalf("an entry the upgrade did not name must be held; record holds %v", verdicttest.RecordedCoordinates(pruned))
	}
	err = buildAt(t, root, releasePath)
	if !errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Fatalf("the build must still refuse on the entry nobody named; got %v", err)
	}
	if strings.Contains(err.Error(), named.Coordinate) {
		t.Errorf("the residual refusal must not name the pruned plugin; got %v", err)
	}
	if !strings.Contains(err.Error(), unnamed.Coordinate) {
		t.Errorf("the residual refusal must name the entry that is still held; got %v", err)
	}
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

	trust := verdicttest.TrustRecordAt(t, releasePath)
	got, held := trust.Find(published.Coordinate)
	if !held {
		t.Fatalf("expected a record for the acquired plugin %s; record holds %v", published.Coordinate, verdicttest.RecordedCoordinates(trust))
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
	again, _ := verdicttest.TrustRecordAt(t, releasePath).Find(published.Coordinate)
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
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false,
		buildDiagOptions{}, buildops.RunOptions{AutoPull: true, OutDir: filepath.Join(root, "dist")}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --auto-pull: %v (diag=%q)", err, diagBuf.String())
	}

	trust := verdicttest.TrustRecordAt(t, releasePath)
	if _, held := trust.Find(published.Coordinate); !held {
		t.Fatalf("build --auto-pull runs the pull path, so it must write the record; record holds %v", verdicttest.RecordedCoordinates(trust))
	}

	// The other half: with the lockfile now present the build's own resolution path
	// runs and no pull does, so a record removed here stays removed.
	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("remove the record between builds: %v", err)
	}
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false,
		buildDiagOptions{}, buildops.RunOptions{OutDir: filepath.Join(root, "dist")}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build with a present lockfile: %v (diag=%q)", err, diagBuf.String())
	}
	if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a build that runs no pull step must write no record; stat err = %v", err)
	}
}

// rerecordVerdictIdentity rewrites the record beside releasePath so the entry for
// coordinate carries version and digest, and returns nothing but a fatal on
// failure. It edits the committed file the way a reviewer would see it edited, so
// the halt under test is provoked by the record's own bytes rather than by a
// fabricated in-memory value.
func rerecordVerdictIdentity(t *testing.T, releasePath, coordinate, version, digest string) {
	t.Helper()
	path := pluginstrust.PathFor(releasePath)
	trust, err := pluginstrust.Load(path)
	if err != nil {
		t.Fatalf("load record %s: %v", path, err)
	}
	if _, held := trust.Find(coordinate); !held {
		t.Fatalf("record %s does not hold %s, so editing it would prove nothing; it holds %v",
			path, coordinate, verdicttest.RecordedCoordinates(trust))
	}
	trust.Upsert(pluginstrust.TrustedPlugin{Plugin: coordinate, ResolvedVersion: version, Digest: digest})
	if err := pluginstrust.Write(path, trust); err != nil {
		t.Fatalf("write edited record %s: %v", path, err)
	}
}

// TestIntegration_VerdictTrustRefusesAChangedRecordedIdentity is the end-to-end
// needle at the real command entrypoint, run in the order the criterion states:
// edit a recorded identity, show the halt, restore it, show green.
//
// It is written to discriminate a halt from a warning and from a pass that never
// compared anything:
//
//   - Each halt is matched against the changed-identity condition, not against
//     "an error came back". A warning would let the pull complete.
//   - Each halt is followed by a restore-and-pull that must go green, so no halt
//     can be attributed to a broken fixture.
//   - Both axes are needled separately — the version with the digest held, and the
//     digest with the version held — because a comparison that read only one axis
//     would pass one of the two while looking correct.
//   - The record on disk is re-read after a halt: a refused run must leave the
//     committed bytes exactly as they were, since replacing them is the very thing
//     it refused to do.
func TestIntegration_VerdictTrustRefusesAChangedRecordedIdentity(t *testing.T) {
	host := startRegistry(t, nil)
	root, releasePath, published := verdictTrustWorkspace(t, host)
	recordPath := pluginstrust.PathFor(releasePath)
	bogusDigest := "sha256:" + strings.Repeat("e", 64)

	pull := func(t *testing.T, cfg pullConfig) error {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cfg.ws = resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
		cfg.ReleasePath = releasePath
		cfg.DiagOut = &stderr
		cfg.Out = &stdout
		return pullRun(cfg)
	}

	// The seeding pull records the identity every phase below is measured against.
	if err := pull(t, pullConfig{}); err != nil {
		t.Fatalf("seeding pullRun: %v", err)
	}
	seeded, held := verdicttest.TrustRecordAt(t, releasePath).Find(published.Coordinate)
	if !held {
		t.Fatalf("precondition: the seeding pull must record %s", published.Coordinate)
	}
	if seeded.ResolvedVersion != "1.0.0" || seeded.Digest != published.WasmDigest {
		t.Fatalf("precondition: recorded identity = %s/%s; want 1.0.0/%s",
			seeded.ResolvedVersion, seeded.Digest, published.WasmDigest)
	}
	restore := func(t *testing.T) {
		t.Helper()
		rerecordVerdictIdentity(t, releasePath, published.Coordinate, seeded.ResolvedVersion, seeded.Digest)
	}

	t.Run("a_moved_recorded_version_halts_the_pull", func(t *testing.T) {
		rerecordVerdictIdentity(t, releasePath, published.Coordinate, "0.9.0", published.WasmDigest)
		before, rerr := os.ReadFile(recordPath)
		if rerr != nil {
			t.Fatalf("read the edited record: %v", rerr)
		}

		err := pull(t, pullConfig{})
		if !errors.Is(err, pullops.ErrVerdictPluginIdentityChanged()) {
			t.Fatalf("a recorded version the resolution does not report must halt with the changed-identity condition; got %v", err)
		}
		for _, want := range []string{published.Coordinate, pluginstrust.FileName, "0.9.0", "1.0.0"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the halt must name %q; got %v", want, err)
			}
		}
		after, rerr := os.ReadFile(recordPath)
		if rerr != nil {
			t.Fatalf("read the record after the halt: %v", rerr)
		}
		if string(after) != string(before) {
			t.Errorf("a refused pull must leave the record exactly as it was;\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("restoring_the_recorded_version_goes_green", func(t *testing.T) {
		restore(t)
		if err := pull(t, pullConfig{}); err != nil {
			t.Fatalf("the restored record must pull cleanly, or the halt above is unattributable; got %v", err)
		}
	})

	t.Run("a_moved_recorded_digest_halts_the_pull", func(t *testing.T) {
		rerecordVerdictIdentity(t, releasePath, published.Coordinate, "1.0.0", bogusDigest)

		err := pull(t, pullConfig{})
		if !errors.Is(err, pullops.ErrVerdictPluginIdentityChanged()) {
			t.Fatalf("a recorded digest the resolution does not report must halt with the changed-identity condition; got %v", err)
		}
		for _, want := range []string{published.Coordinate, bogusDigest, published.WasmDigest} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the halt must name %q; got %v", want, err)
			}
		}
	})

	t.Run("verify_covers_the_record_and_halts_on_the_same_disagreement", func(t *testing.T) {
		// The record still carries the bogus digest from the phase above, and a
		// verify-only run writes nothing — so this proves the comparison sits above
		// the verify-only return rather than inside the write path.
		err := pull(t, pullConfig{Verify: true})
		if !errors.Is(err, pullops.ErrVerdictPluginIdentityChanged()) {
			t.Fatalf("--verify must cover the record beside the lockfile it verifies; got %v", err)
		}
	})

	t.Run("naming_the_plugin_in_an_upgrade_adopts_the_resolved_identity", func(t *testing.T) {
		// The escape the halt above prints. It has to clear two gates that both used
		// to refuse it: the targeted-upgrade target check, which sees no pin for a
		// plugin recorded beside the lockfile rather than in it, and the comparison
		// itself.
		if err := pull(t, pullConfig{Upgrade: true, UpgradeTargets: []string{published.Coordinate}}); err != nil {
			t.Fatalf("naming the plugin in a targeted upgrade must adopt the resolved identity; got %v", err)
		}
		adopted, held := verdicttest.TrustRecordAt(t, releasePath).Find(published.Coordinate)
		if !held {
			t.Fatalf("the upgrade dropped the record for %s", published.Coordinate)
		}
		if adopted.Digest != published.WasmDigest || adopted.ResolvedVersion != "1.0.0" {
			t.Errorf("adopted identity = %s/%s; want the resolved 1.0.0/%s — an upgrade that halts nothing and adopts nothing is inert",
				adopted.ResolvedVersion, adopted.Digest, published.WasmDigest)
		}
		if err := pull(t, pullConfig{}); err != nil {
			t.Fatalf("an ordinary pull after the adoption must go green; got %v", err)
		}
	})

	t.Run("an_upgrade_naming_nothing_the_run_resolves_still_halts", func(t *testing.T) {
		// The existing refusal is preserved: widening the accepted targets to the
		// configured verdict attachments must not turn a mistyped name into a silent
		// no-op.
		err := pull(t, pullConfig{Upgrade: true, UpgradeTargets: []string{"linter/absent-probe"}})
		if err == nil {
			t.Fatal("a target naming neither a pin nor a configured attachment must halt")
		}
		if !strings.Contains(err.Error(), "is not pinned in the lockfile") {
			t.Errorf("the halt must name the unmatched target's absence; got %v", err)
		}
		if errors.Is(err, pullops.ErrVerdictPluginIdentityChanged()) {
			t.Errorf("an unmatched target is not a changed identity; the two conditions must stay distinct: %v", err)
		}
	})
}
