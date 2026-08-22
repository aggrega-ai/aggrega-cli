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
			path, coordinate, recordedCoordinates(trust))
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
	seeded, held := trustRecordAt(t, releasePath).Find(published.Coordinate)
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
		if !errors.Is(err, errVerdictPluginIdentityChanged) {
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
		if !errors.Is(err, errVerdictPluginIdentityChanged) {
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
		if !errors.Is(err, errVerdictPluginIdentityChanged) {
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
		adopted, held := trustRecordAt(t, releasePath).Find(published.Coordinate)
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
		if errors.Is(err, errVerdictPluginIdentityChanged) {
			t.Errorf("an unmatched target is not a changed identity; the two conditions must stay distinct: %v", err)
		}
	})
}
