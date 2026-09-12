// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// trustDeprecationConfigBody builds a consumer workspace config that resolves
// everything from the registry at host, attaches the linter plugins named, and pins
// the publisher key so a marker's own signature can verify.
//
// Signatures are deliberately not REQUIRED. The fixture plugin is published as a
// bare manifest rather than through the signing publish path, so demanding a
// signature would halt on the fixture rather than on anything under test — while a
// pinned key is still what a marker's signature verifies against, which is the only
// thing the marker path needs.
//
// Naming NO linter URI yields a configuration that attaches no verdict plugin at
// all, which is how a test detaches one. It rewrites the whole body rather than
// patching it, and every other dial — the registry, the pinned key, the halt
// threshold — is reproduced, because a detach that also dropped the registry policy
// would change more than the one thing under test.
func trustDeprecationConfigBody(host string, pinnedPubPEM []byte, haltSeverity string, linterURIs ...string) string {
	var b strings.Builder
	b.WriteString("default_registry: \"" + host + "\"\n")
	b.WriteString("auto_pull: false\n")
	b.WriteString("offline: false\n")
	// The `build:` key is omitted entirely rather than written empty when no
	// threshold is set: an empty mapping is null, which the configuration schema
	// refuses. An unset threshold is the default workspace, where a withdrawal
	// advises and never halts.
	if haltSeverity != "" {
		b.WriteString("build:\n  halt_on_deprecated_severity: \"" + haltSeverity + "\"\n")
	}
	if len(linterURIs) > 0 {
		b.WriteString("plugins:\n  linter:\n")
		for _, uri := range linterURIs {
			b.WriteString("    - uri: " + uri + "\n")
		}
	}
	b.WriteString("registries:\n")
	b.WriteString("  - host: \"" + host + "\"\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pinnedPubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// pluginDeprecateRef is the registry reference `aggrega deprecate` addresses a
// published verdict-socket plugin by. It is derived through the same location rule
// the publish side uses, so the fixture cannot disagree with where the artifact is.
func pluginDeprecateRef(host, socket, name, version string) string {
	return host + "/" + catalog.OCILocation("Plugin", socket, name) + ":" + version
}

// TestIntegration_TrustPinnedPluginWithdrawalIsRecordedAndReported is the worked
// round trip for a withdrawn plugin that is pinned in the trust record rather than
// in the lockfile — a linter attachment, or a governance attachment in a verdict
// role, which no report driven by lockfile rows can ever reach.
//
// It carries its own positive control, and the control is the whole point: a
// Component resolved from the SAME registry, withdrawn by the SAME signer in the
// same run, whose marker lands on a lockfile row. So the plugin's own result is a
// statement about the plugin's store rather than about whether the harness attaches
// markers at all — which is exactly the distinction a bare assertion on the plugin
// could not make.
//
// The sequence is: publish both, withdraw both, pull, then read what each store
// recorded and what the build says.
func TestIntegration_TrustPinnedPluginWithdrawalIsRecordedAndReported(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	host := startRegistry(t, nil)
	published := publishVerdictPlugin(t, host, "linter", "withdrawn-probe", "1.0.0")
	// Threshold left unset: this test is about what is RECORDED and REPORTED, and an
	// unset dial is the default workspace, where a marker advises and never halts.
	cfg := trustDeprecationConfigBody(host, pubPEM, "", published.URI)
	wsRoot, componentURI := writeDeprecationConsumerCatalog(t, host, signer, cfg)

	withdraw := func(t *testing.T, reference string) {
		t.Helper()
		if derr := deprecateRun(t.Context(), deprecateConfig{
			Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{}),
			Reference: reference,
			Severity:  "retracted",
			Reason:    "superseded by 2.0.0",
			Message:   "superseded by 2.0.0",
			Signer:    signer,
			EnvLookup: func(string) string { return "" },
		}); derr != nil {
			t.Fatalf("deprecateRun(%s): %v", reference, derr)
		}
	}

	// The control and the subject, withdrawn by the same signer in the same registry.
	withdraw(t, componentDeprecateRef(host))
	withdraw(t, pluginDeprecateRef(host, "linter", "withdrawn-probe", "1.0.0"))

	if perr := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		ReleasePath: "release.yaml",
		DiagOut:     io.Discard,
		Out:         io.Discard,
	}); perr != nil {
		t.Fatalf("pull must succeed with a withdrawn plugin (the halt, where configured, is at build): %v", perr)
	}

	// The positive control. If this is nil the harness is not attaching or honoring
	// markers at all, and nothing below is evidence about the plugin's store.
	if rec := deprecationRowRecord(t, componentURI); rec == nil {
		t.Fatal("positive control failed: the Component's own lockfile row records no withdrawal, " +
			"so this run says nothing about whether a trust-pinned plugin can be reached")
	}

	// Part of the gap this closes: the plugin has no lockfile row to carry a marker.
	// Asserted with the lockfile's own non-empty row set as the control, so an absent
	// row is distinguishable from an absent lockfile.
	lf, _, lerr := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if lerr != nil {
		t.Fatalf("load lockfile: %v", lerr)
	}
	rows := lf.VerifiedFactRows()
	if len(rows) == 0 {
		t.Fatal("the lockfile records no rows at all, so an absent plugin row proves nothing")
	}
	for _, row := range rows {
		if row.URI == published.Coordinate || strings.HasSuffix(row.URI, published.Coordinate) {
			t.Fatalf("a verdict-role plugin is cache-not-pin and must carry no lockfile row; found %q", row.URI)
		}
	}
	t.Logf("lockfile rows (%d), none of them the trust-pinned plugin %q:", len(rows), published.Coordinate)
	for _, row := range rows {
		t.Logf("  %s", row.URI)
	}

	// What this item adds: the withdrawal is recorded on the plugin's own store.
	record := verdicttest.TrustRecordAt(t, "release.yaml")
	t.Logf("trust record:\n%s", readRecord(t, "release.yaml"))
	held, isHeld := record.Find(published.Coordinate)
	if !isHeld {
		t.Fatalf("precondition: the pull must record %s; it holds %v", published.Coordinate, verdicttest.RecordedCoordinates(record))
	}
	if held.Deprecation == nil {
		t.Fatal("the trust entry carries no withdrawal: marker discovery did not reach a trust-pinned plugin, " +
			"so the artifact is still fetched, still cached and still run with nothing said")
	}
	if held.Deprecation.Severity != string(deprecation.SeverityRetracted) {
		t.Errorf("recorded severity = %q, want %q", held.Deprecation.Severity, deprecation.SeverityRetracted)
	}
	if held.Deprecation.Message != "superseded by 2.0.0" {
		t.Errorf("recorded message = %q, want the marker's own message", held.Deprecation.Message)
	}

	// And it reaches a user. The build reports it on the diagnostic stream, naming the
	// plugin the report is about.
	var diag, out strings.Builder
	if berr := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}), "release.yaml", nil, false,
		buildDiagOptions{}, buildops.RunOptions{OutDir: wsRoot + "/dist"}, &diag, &out); berr != nil {
		t.Fatalf("with no halt threshold configured a withdrawal must advise, not halt; got %v", berr)
	}
	t.Logf("build diagnostics:\n%s", diag.String())
	if !strings.Contains(diag.String(), published.Coordinate) {
		t.Errorf("the build said nothing naming the withdrawn trust-pinned plugin %q;\ngot:\n%s",
			published.Coordinate, diag.String())
	}
}

// TestIntegration_TrustPinnedWithdrawalHaltsAtTheConfiguredThreshold proves the
// report is wired to the SAME halt dial every artifact-producing surface reads, so a
// workspace that asked to be stopped on a withdrawal is stopped by this one too.
//
// It is separate from the advisory round trip above because the two cross different
// arms of one gate, and a single test at one threshold cannot show that the dial is
// read at all.
func TestIntegration_TrustPinnedWithdrawalHaltsAtTheConfiguredThreshold(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	host := startRegistry(t, nil)
	published := publishVerdictPlugin(t, host, "linter", "halting-probe", "1.0.0")
	cfg := trustDeprecationConfigBody(host, pubPEM, "retracted", published.URI)
	wsRoot, _ := writeDeprecationConsumerCatalog(t, host, signer, cfg)

	if derr := deprecateRun(t.Context(), deprecateConfig{
		Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{}),
		Reference: pluginDeprecateRef(host, "linter", "halting-probe", "1.0.0"),
		Severity:  "retracted",
		Reason:    "unsound rule set",
		Message:   "unsound rule set",
		Signer:    signer,
		EnvLookup: func(string) string { return "" },
	}); derr != nil {
		t.Fatalf("deprecateRun: %v", derr)
	}

	if perr := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		ReleasePath: "release.yaml",
		DiagOut:     io.Discard,
		Out:         io.Discard,
	}); perr != nil {
		t.Fatalf("pull must succeed; the halt is at build: %v", perr)
	}

	var diag, out strings.Builder
	berr := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}), "release.yaml", nil, false,
		buildDiagOptions{}, buildops.RunOptions{OutDir: wsRoot + "/dist"}, &diag, &out)
	if !errors.Is(berr, deprecation.ErrDeprecatedEntity()) {
		t.Fatalf("a retracted trust-pinned plugin at a retracted threshold must halt the build with the shared condition; got %v\ndiagnostics:\n%s",
			berr, diag.String())
	}
}

// TestIntegration_TrustPinnedWithdrawalFollowsTheTrustFileLifecycle is the
// lifecycle criterion, and it is the one most likely to be got wrong by copying the
// lockfile's rule: the lockfile is regenerated wholesale on every pull, while the
// trust record prunes only on an explicit upgrade and a plain pull neither rewrites
// nor removes an entry.
//
// So the marker is shown holding on both invocations that matter. A plain pull over
// an already-recorded entry records a withdrawal that appeared after the entry was
// written — the identity is untouched, and recording a fact about the artifact
// already recorded adopts no new identity. An explicit upgrade that detaches the
// plugin takes the entry and its marker together.
func TestIntegration_TrustPinnedWithdrawalFollowsTheTrustFileLifecycle(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	host := startRegistry(t, nil)
	published := publishVerdictPlugin(t, host, "linter", "lifecycle-probe", "1.0.0")
	cfg := trustDeprecationConfigBody(host, pubPEM, "", published.URI)
	wsRoot, _ := writeDeprecationConsumerCatalog(t, host, signer, cfg)

	pull := func(t *testing.T, upgrade bool) {
		t.Helper()
		if perr := pullRun(pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
			ReleasePath: "release.yaml",
			Upgrade:     upgrade,
			DiagOut:     io.Discard,
			Out:         io.Discard,
		}); perr != nil {
			t.Fatalf("pullRun(upgrade=%v): %v", upgrade, perr)
		}
	}

	// The entry is recorded first, with no marker attached anywhere yet.
	pull(t, false)
	seeded, isHeld := verdicttest.TrustRecordAt(t, "release.yaml").Find(published.Coordinate)
	if !isHeld {
		t.Fatalf("precondition: the seeding pull must record %s", published.Coordinate)
	}
	if seeded.Deprecation != nil {
		t.Fatalf("precondition: nothing is withdrawn yet, so the entry must carry no marker; got %+v", seeded.Deprecation)
	}

	// The withdrawal happens AFTER the entry was recorded, which is the ordinary case:
	// a publisher withdraws an artifact a workspace already trusts.
	if derr := deprecateRun(t.Context(), deprecateConfig{
		Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{}),
		Reference: pluginDeprecateRef(host, "linter", "lifecycle-probe", "1.0.0"),
		Severity:  "warning",
		Reason:    "deprecated after the entry was recorded",
		Message:   "deprecated after the entry was recorded",
		Signer:    signer,
		EnvLookup: func(string) string { return "" },
	}); derr != nil {
		t.Fatalf("deprecateRun: %v", derr)
	}

	// A PLAIN pull. It must not rewrite the identity, and it must record the marker:
	// were the marker gated on the pin decision instead of on artifact identity, this
	// is where the feature would go inert — the entry is already held, so no later
	// plain pull would ever record a withdrawal.
	pull(t, false)
	afterPlain, isHeld := verdicttest.TrustRecordAt(t, "release.yaml").Find(published.Coordinate)
	if !isHeld {
		t.Fatalf("a plain pull must not remove the entry; it holds %v",
			verdicttest.RecordedCoordinates(verdicttest.TrustRecordAt(t, "release.yaml")))
	}
	if afterPlain.ResolvedVersion != seeded.ResolvedVersion || afterPlain.Digest != seeded.Digest {
		t.Fatalf("a plain pull must leave the recorded IDENTITY exactly as it was;\nbefore: %+v\nafter:  %+v", seeded, afterPlain)
	}
	if afterPlain.Deprecation == nil {
		t.Fatal("a plain pull did not record a withdrawal discovered for the artifact the entry already holds; " +
			"the marker must follow artifact identity, not the pin decision")
	}
	if afterPlain.Deprecation.Severity != "warning" {
		t.Errorf("recorded severity = %q, want warning", afterPlain.Deprecation.Severity)
	}

	// An EXPLICIT upgrade that detaches the plugin takes the entry and its marker
	// together. Nothing clears a marker on its own.
	writeWorkspaceConfig(t, wsRoot, trustDeprecationConfigBody(host, pubPEM, ""))
	pull(t, true)
	pruned := verdicttest.TrustRecordAt(t, "release.yaml")
	if _, stillHeld := pruned.Find(published.Coordinate); stillHeld {
		t.Fatalf("an explicit upgrade over a detached plugin must drop the entry, marker and all; it holds %v",
			verdicttest.RecordedCoordinates(pruned))
	}
}
