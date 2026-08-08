// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's retraction-at-selection integration coverage for the
// discover-and-attach command's unpinned version resolution. Build-tagged
// `integration` because it drives a real in-process OCI registry, whose test-only
// dependency must not enter the production binary's import graph.
package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// buildStampedGuest builds the governance fixture guest with its self-reported
// version stamped to the given value, into a directory private to this test.
//
// Two distinctly-stamped builds are what make a two-version plugin repository
// publishable at all: the publish door cross-checks a Plugin entity's
// metadata.version against the guest's own self-reported version and halts on a
// mismatch, so one guest cannot be published twice under two versions. Building
// into a private directory rather than the shared fixture path is deliberate — the
// shared artifact is read by every other test in the run and must not be replaced
// with a differently-stamped one.
func buildStampedGuest(t *testing.T, sharedFixture, version string) string {
	t.Helper()
	// The umbrella root is derived from the shared fixture's own resolved path
	// rather than re-deriving it, so a layout change surfaces here as a missing
	// script instead of as a silently wrong directory.
	umbrellaRoot := sharedFixture
	for range 5 {
		umbrellaRoot = filepath.Dir(umbrellaRoot)
	}
	script := filepath.Join(umbrellaRoot, "aggrega-pdk", "go", "examples", "mutator-proof", "build.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("guest build script not at %s: %v", script, err)
	}

	out := t.TempDir()
	cmd := exec.Command("bash", script)
	cmd.Dir = filepath.Dir(script)
	cmd.Env = append(os.Environ(), "AGGREGA_PDK_WASM_OUT="+out, "AGGREGA_PLUGIN_VERSION="+version)
	if combined, err := cmd.CombinedOutput(); err != nil {
		// The toolchain is known present: the shared fixture resolved above skips the
		// whole test when it is not. So a failure here is a genuine one.
		t.Fatalf("build guest stamped %s: %v\n%s", version, err, combined)
	}
	built := filepath.Join(out, "mutator-proof-go.wasm")
	if _, err := os.Stat(built); err != nil {
		t.Fatalf("guest build stamped %s produced nothing at %s: %v", version, built, err)
	}
	return built
}

// pluginRetractionFixture is one prepared registry for the attach arms below: a
// real in-process registry holding two signed versions of one governance plugin, a
// workspace whose registry policy pins the publisher's key, and the local index
// built from that registry through the production `registry update`.
type pluginRetractionFixture struct {
	host string
	rw   *workspaceconfig.ResolvedWorkspace
}

// pluginRetractionOption adjusts how a fixture is prepared. The default prepares a
// registry AND the local index built from it; an option may withhold the index so a
// resolution takes the live fallback instead of the snapshot.
type pluginRetractionOption func(*pluginRetractionOptions)

type pluginRetractionOptions struct{ buildIndex bool }

// withoutLocalIndex prepares the registry exactly as the default does — same
// publishes, same signed markers, same trust policy — but leaves the local discovery
// index unbuilt. Version resolution then finds no record for the repository and takes
// the live path, which is the only way to exercise it: with an index present the
// snapshot answers first and the live path is never reached.
//
// Withholding the index rather than deleting it afterwards is deliberate. A fixture
// that built an index and then removed it would prove the same thing only if the
// removal were complete, and a partial removal would silently resolve from whatever
// remained.
func withoutLocalIndex(o *pluginRetractionOptions) { o.buildIndex = false }

// newPluginRetractionFixture publishes the governance plugin at 1.0.0 AND 2.0.0
// through the production signed push, optionally attaches a signed deprecation
// marker of the given severity to each named version through the production
// `aggrega deprecate`, then builds the local index through the production
// `registry update` unless an option withholds it.
//
// Two published versions is the whole point: a single-version repository cannot
// show a selection consequence at all, because there is nothing else to select.
// Every link is the production path — the push, the marker attach, the marker's own
// signature, the index build and its verification — so a passing arm cannot be an
// artefact of a hand-stubbed index.
func newPluginRetractionFixture(t *testing.T, guests map[string]string, markers map[string]string, opts ...pluginRetractionOption) pluginRetractionFixture {
	t.Helper()

	o := pluginRetractionOptions{buildIndex: true}
	for _, opt := range opts {
		opt(&o)
	}

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	host := startRegistry(t, nil)
	for _, version := range []string{"1.0.0", "2.0.0"} {
		publishSocketPluginEntitySigned(t, host, pluginRetractionSocketName, version, guests[version], "governance", "mutate", signer)
	}

	// The marker is attached by the same command an operator would run, and its own
	// signature is produced by the publisher's key — so the index build has a real
	// signature to verify rather than a fabricated verdict to trust.
	for version, severity := range markers {
		if derr := deprecateRun(deprecateConfig{
			Reference: host + "/" + pluginRetractionRepoPath + ":" + version,
			Severity:  severity,
			Reason:    "withdrawn in favour of a successor",
			Signer:    signer,
			EnvLookup: func(string) string { return "" },
		}); derr != nil {
			t.Fatalf("deprecateRun(%s, %s): %v", version, severity, derr)
		}
	}

	var b strings.Builder
	b.WriteString("workspace: {}\n")
	b.WriteString("default_registry: \"" + host + "\"\n")
	b.WriteString("offline: false\n")
	b.WriteString("registries:\n")
	b.WriteString("  - host: \"" + host + "\"\n")
	b.WriteString("    name: fixture\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), b.String())

	if o.buildIndex {
		var updateOut bytes.Buffer
		if uerr := registryUpdateRun(registryUpdateConfig{ws: rw, Out: &updateOut}); uerr != nil {
			t.Fatalf("registryUpdateRun: %v\n%s", uerr, updateOut.String())
		}
	} else if _, lerr := loadHostIndex(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host); !errors.Is(lerr, errIndexNotBuilt) {
		// The precondition IS the fixture: an arm that means to exercise the live path
		// proves nothing if a snapshot answered first. Asserting the absence here means a
		// later change that starts building an index eagerly fails loudly rather than
		// quietly re-routing these arms through the path they exist to bypass.
		t.Fatalf("fixture precondition: want no local index for %s; loadHostIndex reported %v", host, lerr)
	}

	return pluginRetractionFixture{host: host, rw: rw}
}

const (
	pluginRetractionSocketName = "governance/tag-injector"
	pluginRetractionRepoPath   = "plugin/governance/tag-injector"
)

// addGovernancePlugin runs the production discover-and-attach command against the
// fixture and returns the version it pinned into the workspace config plus the
// bytes it wrote to its output, so the announcement is read as rendered output
// rather than as a format string.
func (f pluginRetractionFixture) addGovernancePlugin(t *testing.T) (string, string, error) {
	t.Helper()
	var out bytes.Buffer
	err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "governance",
		Role:   "label-transform",
		Query:  "tag-injector",
		ws:     f.rw,
		Out:    &out,
		lookup: func(string) string { return "" },
	})
	if err != nil {
		return "", out.String(), err
	}
	reread, rerr := workspaceconfig.Resolve(f.rw.Root(), "")
	if rerr != nil {
		t.Fatalf("resolve written config: %v", rerr)
	}
	attached := reread.Config().Plugins.Governance
	if len(attached) != 1 {
		t.Fatalf("plugins.governance = %+v; want exactly one entry", attached)
	}
	_, version, found := strings.Cut(attached[0].URI, ":")
	if !found {
		t.Fatalf("attached identity %q carries no pinned version", attached[0].URI)
	}
	return version, out.String(), nil
}

// TestIntegration_PluginAddExcludesWithdrawnAtSelection is the discriminating pair
// the attach command's unpinned resolution needs, plus the arms that fence it.
//
// The pair is load-bearing and neither half proves anything alone: "the lower
// version is pinned" passes identically whether the exclusion fired or whether the
// higher version was never published, so the same two-version plugin is attached
// twice — once with the higher version withdrawn and once without — and only the
// DIFFERENCE between the two outcomes is evidence.
func TestIntegration_PluginAddExcludesWithdrawnAtSelection(t *testing.T) {
	// Resolve the shared fixture first: it is the clean skip when the guest toolchain
	// is absent, and its resolved path anchors the stamped builds below. The two
	// stamped guests are built once for the whole test rather than per arm.
	shared := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	guests := map[string]string{
		"1.0.0": buildStampedGuest(t, shared, "1.0.0"),
		"2.0.0": buildStampedGuest(t, shared, "2.0.0"),
	}

	t.Run("withdrawn_higher_version_is_not_pinned_and_the_substitution_is_announced", func(t *testing.T) {
		f := newPluginRetractionFixture(t, guests, map[string]string{"2.0.0": "retracted"})
		version, out, err := f.addGovernancePlugin(t)
		if err != nil {
			t.Fatalf("attaching a plugin with one unwithdrawn version must succeed; got: %v\n%s", err, out)
		}
		if version != "1.0.0" {
			t.Fatalf("pinned %q; want 1.0.0 — the withdrawn 2.0.0 must not be pinned", version)
		}
		t.Logf("rendered command output:\n%s", out)
		for _, want := range []string{
			f.host + "/" + pluginRetractionRepoPath + ":2.0.0 is withdrawn and was not selected",
			"withdrawn in favour of a successor",
			"resolved to 1.0.0 instead",
			"aggrega registry update",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the announcement must contain %q; rendered output was:\n%s", want, out)
			}
		}
		// The command still reports what it did, on the same writer.
		if !strings.Contains(out, pluginRetractionSocketName+":1.0.0") {
			t.Errorf("the command must still name what it attached; rendered output was:\n%s", out)
		}
	})

	t.Run("the_same_fixture_without_the_marker_pins_the_higher", func(t *testing.T) {
		f := newPluginRetractionFixture(t, guests, nil)
		version, out, err := f.addGovernancePlugin(t)
		if err != nil {
			t.Fatalf("attaching an unmarked plugin must succeed; got: %v\n%s", err, out)
		}
		if version != "2.0.0" {
			t.Fatalf("pinned %q; want 2.0.0 for an unmarked plugin", version)
		}
		if strings.Contains(out, "retraction:") {
			t.Errorf("an unmarked plugin must announce no exclusion; rendered output was:\n%s", out)
		}
	})

	t.Run("a_softer_severity_on_the_same_fixture_still_pins_the_higher", func(t *testing.T) {
		// The sharpest form of the pair: the marker is present, signed and verified
		// exactly as in the excluding arm, so the screen is live and holds a record for
		// this very repository. Only the SEVERITY differs.
		f := newPluginRetractionFixture(t, guests, map[string]string{"2.0.0": "warning"})
		version, out, err := f.addGovernancePlugin(t)
		if err != nil {
			t.Fatalf("a soft marker must not affect the attach; got: %v\n%s", err, out)
		}
		if version != "2.0.0" {
			t.Fatalf("pinned %q; want 2.0.0 — a soft severity warns and does not change selection", version)
		}
		if strings.Contains(out, "retraction:") {
			t.Errorf("a soft marker must announce no exclusion; rendered output was:\n%s", out)
		}
	})

	t.Run("before_picture_an_unscreened_resolution_pins_the_withdrawn_version", func(t *testing.T) {
		// The posture this change replaces, preserved by construction rather than
		// reconstructed: with nowhere to announce a substitution, resolution is left
		// unscreened over the identical registry state and pins the withdrawn version.
		// It is what makes the arm above evidence — the fixture did not change, the
		// screen did.
		f := newPluginRetractionFixture(t, guests, map[string]string{"2.0.0": "retracted"})
		version, err := resolveLatestPluginVersion(context.Background(), f.rw, f.host, pluginRetractionRepoPath, nil)
		if err != nil {
			t.Fatalf("unscreened resolution must succeed: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("unscreened resolution took %q; want 2.0.0 — resolution is marker-blind with no screen", version)
		}
	})

	t.Run("every_version_withdrawn_halts_loudly_and_not_as_not_found", func(t *testing.T) {
		// With nothing left to select, the attach halts naming every withdrawn version
		// and its reason. It must not read as a missing repository: the repository
		// exists and still holds those versions, and naming the wrong problem would
		// send the reader looking for a typo.
		f := newPluginRetractionFixture(t, guests, map[string]string{"1.0.0": "retracted", "2.0.0": "retracted"})
		_, out, err := f.addGovernancePlugin(t)
		if !errors.Is(err, catalog.ErrAllVersionsRetracted()) {
			t.Fatalf("a fully-withdrawn plugin must halt with ErrAllVersionsRetracted; got: %v\n%s", err, out)
		}
		t.Logf("rendered halt: %v", err)
		for _, want := range []string{"1.0.0", "2.0.0", "withdrawn in favour of a successor"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("halt message must name %q; got: %v", want, err)
			}
		}
	})
}

// TestIntegration_PluginAddExcludesWithdrawnWithNoLocalIndex is the same
// discriminating pair for the path taken when the local index does not carry the
// repository at all — an online workspace whose snapshot was never built.
//
// It is a separate concern from the pair above rather than more of it: there, the
// withdrawal is read from a snapshot a `registry update` recorded, and the screen
// costs no round trip. Here there is no snapshot, so the withdrawal has to be read
// from the registry during the attach itself. Same registry state, same signed
// markers, same production commands — only the presence of the index differs, which
// is what makes the two arms below evidence about the live path specifically.
func TestIntegration_PluginAddExcludesWithdrawnWithNoLocalIndex(t *testing.T) {
	shared := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	guests := map[string]string{
		"1.0.0": buildStampedGuest(t, shared, "1.0.0"),
		"2.0.0": buildStampedGuest(t, shared, "2.0.0"),
	}

	t.Run("withdrawn_higher_version_is_not_pinned_and_the_substitution_is_announced", func(t *testing.T) {
		f := newPluginRetractionFixture(t, guests, map[string]string{"2.0.0": "retracted"}, withoutLocalIndex)
		version, out, err := f.addGovernancePlugin(t)
		if err != nil {
			t.Fatalf("attaching a plugin with one unwithdrawn version must succeed; got: %v\n%s", err, out)
		}
		if version != "1.0.0" {
			t.Fatalf("pinned %q; want 1.0.0 — the withdrawn 2.0.0 must not be pinned with no snapshot to screen from", version)
		}
		t.Logf("rendered command output:\n%s", out)
		for _, want := range []string{
			f.host + "/" + pluginRetractionRepoPath + ":2.0.0 is withdrawn and was not selected",
			"withdrawn in favour of a successor",
			"resolved to 1.0.0 instead",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the announcement must contain %q; rendered output was:\n%s", want, out)
			}
		}
		// The provenance clause differs from the snapshot path's on purpose: these
		// markers were read during this command, so there is no staleness to warn about
		// and no snapshot to refresh.
		if !strings.Contains(out, "read from the registry just now") {
			t.Errorf("the announcement must name its live source; rendered output was:\n%s", out)
		}
		if strings.Contains(out, "registry update") {
			t.Errorf("a live reading must not point at the snapshot-refresh command; rendered output was:\n%s", out)
		}
		// The command still reports what it did, on the same writer.
		if !strings.Contains(out, pluginRetractionSocketName+":1.0.0") {
			t.Errorf("the command must still name what it attached; rendered output was:\n%s", out)
		}
	})

	t.Run("the_same_fixture_without_the_marker_pins_the_higher", func(t *testing.T) {
		// The other half of the pair. Without it, "1.0.0 was pinned" above would pass
		// identically had 2.0.0 simply never been published.
		f := newPluginRetractionFixture(t, guests, nil, withoutLocalIndex)
		version, out, err := f.addGovernancePlugin(t)
		if err != nil {
			t.Fatalf("attaching an unmarked plugin must succeed; got: %v\n%s", err, out)
		}
		if version != "2.0.0" {
			t.Fatalf("pinned %q; want 2.0.0 for an unmarked plugin", version)
		}
		if strings.Contains(out, "retraction:") {
			t.Errorf("an unmarked plugin must announce no exclusion; rendered output was:\n%s", out)
		}
	})

	t.Run("a_softer_severity_on_the_same_fixture_still_pins_the_higher", func(t *testing.T) {
		// The sharpest arm: the marker is present, signed and verified through the live
		// read exactly as in the excluding arm, so the read is working and holds a record
		// for this very repository. Only the SEVERITY differs.
		f := newPluginRetractionFixture(t, guests, map[string]string{"2.0.0": "warning"}, withoutLocalIndex)
		version, out, err := f.addGovernancePlugin(t)
		if err != nil {
			t.Fatalf("a soft marker must not affect the attach; got: %v\n%s", err, out)
		}
		if version != "2.0.0" {
			t.Fatalf("pinned %q; want 2.0.0 — a soft severity warns and does not change selection", version)
		}
		if strings.Contains(out, "retraction:") {
			t.Errorf("a soft marker must announce no exclusion; rendered output was:\n%s", out)
		}
	})

	t.Run("every_version_withdrawn_halts_loudly_and_not_as_not_found", func(t *testing.T) {
		f := newPluginRetractionFixture(t, guests, map[string]string{"1.0.0": "retracted", "2.0.0": "retracted"}, withoutLocalIndex)
		_, out, err := f.addGovernancePlugin(t)
		if !errors.Is(err, catalog.ErrAllVersionsRetracted()) {
			t.Fatalf("a fully-withdrawn plugin must halt with ErrAllVersionsRetracted; got: %v\n%s", err, out)
		}
		t.Logf("rendered halt: %v", err)
		for _, want := range []string{"1.0.0", "2.0.0", "withdrawn in favour of a successor"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("halt message must name %q; got: %v", want, err)
			}
		}
	})
}
