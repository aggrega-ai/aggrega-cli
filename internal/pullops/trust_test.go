// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// attachingVerdictPlugins builds the verdict attachment set for the authored
// references given, at the linter socket, whose every attachment is a verdict
// attachment.
//
// A test states this alongside what the pull resolved because the two are
// different facts and the write path needs both: the resolutions say what was
// acquired, the attachments say what the configuration still names. A detached
// plugin is exactly the case with a record entry and no resolution, so a test
// that supplied only the resolutions could not express it.
func attachingVerdictPlugins(uris ...string) []workspaceconfig.VerdictAttachment {
	out := make([]workspaceconfig.VerdictAttachment, 0, len(uris))
	for _, uri := range uris {
		out = append(out, workspaceconfig.VerdictAttachment{
			Socket: workspaceconfig.SocketLinter,
			Ref:    workspaceconfig.PluginRef{URI: uri},
		})
	}
	return out
}

// TestVerdictPluginIdentityIsTheWasmLayerDigest is the load-bearing identity
// proof, and it is written to DISCRIMINATE: the identity the verdict-plugin verify
// reports carries the .wasm LAYER content digest read from the cached manifest,
// not the OCI manifest digest it is handed. Both are well-formed `sha256:` strings
// and a record carrying either one validates against the schema, so only comparing
// them can tell the right one from the wrong one.
//
// A later resolution of the same plugin compares the recorded digest against the
// .wasm layer digest its cached manifest reports. Reporting the manifest digest
// here would therefore halt every configured attachment on the next resolution,
// while still producing a perfectly valid-looking record.
func TestVerdictPluginIdentityIsTheWasmLayerDigest(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	seeded := verdicttest.SeedCached(t, root, catalogDir, "governance", "trust-probe", "2.4.1")

	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)

	identity, err := verifyCachedVerdictSocketPlugin(
		context.Background(), workspaceconfig.Config{}, lazy, seeded.URI, seeded.ManifestDigest,
		root, cacheDir, nil, io.Discard,
	)
	if err != nil {
		t.Fatalf("verify cached verdict plugin %s: %v", seeded.URI, err)
	}

	if identity.Coordinate != seeded.Coordinate {
		t.Errorf("identity coordinate = %q; want %q", identity.Coordinate, seeded.Coordinate)
	}
	if identity.Version != "2.4.1" {
		t.Errorf("identity version = %q; want the entity's declared version 2.4.1", identity.Version)
	}
	if identity.Digest != seeded.WasmDigest {
		t.Errorf("identity digest = %s; want the .wasm layer content digest %s", identity.Digest, seeded.WasmDigest)
	}
	// The discriminator: the two digests must be different values, and the one
	// reported must not be the manifest digest that was handed in.
	if seeded.WasmDigest == seeded.ManifestDigest {
		t.Fatalf("fixture broken: the .wasm layer digest and the manifest digest are the same value %s, so this test cannot discriminate", seeded.WasmDigest)
	}
	if identity.Digest == seeded.ManifestDigest {
		t.Errorf("identity digest is the OCI manifest digest %s; a resolution comparing the .wasm layer digest would halt on every configured attachment", identity.Digest)
	}
}

// TestVerdictTrustRecordedDigestSatisfiesResolution closes the loop the identity
// test opens: a record written from the reported identity satisfies the resolution
// that reads it, and a record carrying the manifest digest instead HALTS. The
// second half is the A/B that makes the first half attributable — without it, a
// green resolution could mean the comparison never happened.
func TestVerdictTrustRecordedDigestSatisfiesResolution(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	seeded := verdicttest.SeedCached(t, root, catalogDir, "governance", "trust-probe", "2.4.1")

	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	sources := resolver.VerdictPluginTierSources{WorkspaceRoot: root, CacheDir: cacheDir}
	lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)

	resolveWith := func(t *testing.T, recordedDigest string) error {
		t.Helper()
		trust := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
		trust.Upsert(pluginstrust.TrustedPlugin{
			Plugin:          seeded.Coordinate,
			ResolvedVersion: "2.4.1",
			Digest:          recordedDigest,
		})
		_, err := resolver.ResolveVerdictPluginFromCache(sources, trust.PinMaps(), lazy.PluginFromCacheByURI, seeded.URI)
		return err
	}

	t.Run("the_recorded_wasm_layer_digest_resolves", func(t *testing.T) {
		if err := resolveWith(t, seeded.WasmDigest); err != nil {
			t.Fatalf("a record carrying the .wasm layer digest must resolve; got %v", err)
		}
	})

	t.Run("the_manifest_digest_halts", func(t *testing.T) {
		err := resolveWith(t, seeded.ManifestDigest)
		if err == nil {
			t.Fatal("a record carrying the OCI manifest digest must halt; got a clean resolution, so the comparison is not discriminating")
		}
		if !errors.Is(err, resolver.ErrVerdictPluginTrustMismatch()) {
			t.Errorf("the halt must name the trust-mismatch condition; got %v", err)
		}
	})
}

// TestVerdictTrustDeclinesWhatTheBinaryCarries proves the acquired-artifact rule:
// a coordinate the binary carries is not recorded, however it was configured,
// while a coordinate the binary does not carry is.
func TestVerdictTrustDeclinesWhatTheBinaryCarries(t *testing.T) {
	const carried = "linter/carried-probe"
	restore := embedded.SeedForTest(carried, verdicttest.HeaderOnlyWasm)
	t.Cleanup(restore)

	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")

	var diag bytes.Buffer
	recorded, err := recordVerdictPluginTrust("", releasePath,
		attachingVerdictPlugins(carried, "linter/acquired-probe"),
		[]verdictPluginResolution{
			{BareURI: carried, Coordinate: carried, Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64)},
			{BareURI: "linter/acquired-probe", Coordinate: "linter/acquired-probe", Version: "2.0.0", Digest: "sha256:" + strings.Repeat("b", 64)},
		}, catalog.PinReuseMode{}, &diag)
	if err != nil {
		t.Fatalf("recordVerdictPluginTrust: %v", err)
	}
	if recorded != 1 {
		t.Errorf("recorded count = %d; want 1 (the carried coordinate is declined)", recorded)
	}
	trust := verdicttest.TrustRecordAt(t, releasePath)
	if want := []string{"linter/acquired-probe"}; strings.Join(verdicttest.RecordedCoordinates(trust), ",") != strings.Join(want, ",") {
		t.Errorf("record holds %v; want exactly %v", verdicttest.RecordedCoordinates(trust), want)
	}
}

// TestVerdictTrustDeclinesEverythingCarriedWritesNoFile is the boundary case the
// decline shares with an unconfigured workspace: when every attachment resolves to
// a coordinate the binary carries, there is nothing to record and therefore no
// file, rather than a file holding an empty list.
func TestVerdictTrustDeclinesEverythingCarriedWritesNoFile(t *testing.T) {
	const carried = "linter/carried-probe"
	restore := embedded.SeedForTest(carried, verdicttest.HeaderOnlyWasm)
	t.Cleanup(restore)

	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")

	var diag bytes.Buffer
	recorded, err := recordVerdictPluginTrust("", releasePath,
		attachingVerdictPlugins(carried),
		[]verdictPluginResolution{
			{BareURI: carried, Coordinate: carried, Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64)},
		}, catalog.PinReuseMode{}, &diag)
	if err != nil {
		t.Fatalf("recordVerdictPluginTrust: %v", err)
	}
	if recorded != 0 {
		t.Errorf("recorded count = %d; want 0", recorded)
	}
	if _, err := os.Stat(pluginstrust.PathFor(releasePath)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("nothing to record must leave no file; stat err = %v", err)
	}
	if diag.Len() != 0 {
		t.Errorf("nothing to record must say nothing; diagnostic = %q", diag.String())
	}
}

// TestVerdictTrustDeclineAgreesWithTheBinary is the single-source guard: the
// decline consults the same set the resolution cascade's first tier consults, so
// whether the standard built-in linter is recorded is decided by whether THIS
// binary carries it — not by a second opinion this package holds. The assertion
// holds on a full binary (carried, declined) and on a light one (not carried,
// recorded) without a build-tag branch, which is the property being locked.
func TestVerdictTrustDeclineAgreesWithTheBinary(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")

	_, carriedByBinary := embedded.Lookup(embedded.PublishingLinterURI)

	var diag bytes.Buffer
	recorded, err := recordVerdictPluginTrust("", releasePath,
		attachingVerdictPlugins(embedded.PublishingLinterURI),
		[]verdictPluginResolution{{
			BareURI:    embedded.PublishingLinterURI,
			Coordinate: embedded.PublishingLinterURI,
			Version:    "0.5.0",
			Digest:     "sha256:" + strings.Repeat("c", 64),
		}}, catalog.PinReuseMode{}, &diag)
	if err != nil {
		t.Fatalf("recordVerdictPluginTrust: %v", err)
	}

	if carriedByBinary && recorded != 0 {
		t.Errorf("this binary carries %s, so it must not be recorded; recorded %d", embedded.PublishingLinterURI, recorded)
	}
	if !carriedByBinary && recorded != 1 {
		t.Errorf("this binary does not carry %s, so a configured attachment must be recorded; recorded %d", embedded.PublishingLinterURI, recorded)
	}
}

// TestVerdictTrustFillsAMissingEntryAndHoldsAPresentOne covers the fill-not-
// overwrite rule in both directions at once: a coordinate the record does not
// hold is added, and a coordinate it does hold keeps its recorded digest even
// though this resolution reported a different one. Holding the present entry is
// the whole point — a record that adopted whatever was just resolved would follow
// the resolution instead of governing it.
func TestVerdictTrustFillsAMissingEntryAndHoldsAPresentOne(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")

	held := "sha256:" + strings.Repeat("1", 64)
	fresh := "sha256:" + strings.Repeat("2", 64)
	added := "sha256:" + strings.Repeat("3", 64)

	seed := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
	seed.Upsert(pluginstrust.TrustedPlugin{Plugin: "linter/held-probe", ResolvedVersion: "1.0.0", Digest: held})
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), seed); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	var diag bytes.Buffer
	if _, err := recordVerdictPluginTrust("", releasePath,
		attachingVerdictPlugins("linter/held-probe", "linter/added-probe"),
		[]verdictPluginResolution{
			{BareURI: "linter/held-probe", Coordinate: "linter/held-probe", Version: "9.9.9", Digest: fresh},
			{BareURI: "linter/added-probe", Coordinate: "linter/added-probe", Version: "2.0.0", Digest: added},
		}, catalog.PinReuseMode{}, &diag); err != nil {
		t.Fatalf("recordVerdictPluginTrust: %v", err)
	}

	trust := verdicttest.TrustRecordAt(t, releasePath)
	got, ok := trust.Find("linter/held-probe")
	if !ok {
		t.Fatalf("the present entry disappeared; record holds %v", verdicttest.RecordedCoordinates(trust))
	}
	if got.Digest != held {
		t.Errorf("present entry digest = %s; want the held %s (an ordinary pull must not overwrite it)", got.Digest, held)
	}
	if got.ResolvedVersion != "1.0.0" {
		t.Errorf("present entry version = %q; want the held 1.0.0", got.ResolvedVersion)
	}
	addedRec, ok := trust.Find("linter/added-probe")
	if !ok {
		t.Fatalf("the missing entry was not filled; record holds %v", verdicttest.RecordedCoordinates(trust))
	}
	if addedRec.Digest != added {
		t.Errorf("filled entry digest = %s; want %s", addedRec.Digest, added)
	}
	// What the run says has to match what it did, on both halves at once: the
	// record already existed, so no creation is announced; the missing entry was
	// filled, so it is; and the present entry was HELD rather than rewritten, so
	// naming it would tell a reader its identity had moved when it had not.
	said := diag.String()
	if strings.Contains(said, "created") {
		t.Errorf("the record already existed, so nothing should announce a creation; diagnostic = %q", said)
	}
	if !strings.Contains(said, "linter/added-probe: recorded for the first time") {
		t.Errorf("the entry this pull filled must be announced; diagnostic = %q", said)
	}
	if strings.Contains(said, "linter/held-probe") {
		t.Errorf("the held entry did not move, so announcing it would report a change that did not happen; diagnostic = %q", said)
	}
}

// TestVerdictTrustRewritesOnlyWhenTheRunReresolves pins the one condition under
// which a present entry is replaced: the run asked for fresh resolution. A bare
// upgrade re-resolves everything; an upgrade naming this attachment re-resolves
// it; an upgrade naming something else does not; and an ordinary pull never does.
//
// The table is what makes this discriminating: a rewrite rule keyed on the wrong
// question would pass one row and fail another.
func TestVerdictTrustRewritesOnlyWhenTheRunReresolves(t *testing.T) {
	held := "sha256:" + strings.Repeat("1", 64)
	fresh := "sha256:" + strings.Repeat("2", 64)

	cases := []struct {
		name string
		mode catalog.PinReuseMode
		want string
	}{
		{name: "ordinary_pull_holds", mode: catalog.PinReuseMode{}, want: held},
		{name: "bare_upgrade_rewrites", mode: catalog.PinReuseMode{Upgrade: true}, want: fresh},
		{
			name: "targeted_upgrade_naming_it_rewrites",
			mode: catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"linter/held-probe"}},
			want: fresh,
		},
		{
			name: "targeted_upgrade_naming_another_holds",
			mode: catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database"}},
			want: held,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			releasePath := filepath.Join(root, "release.yaml")
			seed := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
			seed.Upsert(pluginstrust.TrustedPlugin{Plugin: "linter/held-probe", ResolvedVersion: "1.0.0", Digest: held})
			if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), seed); err != nil {
				t.Fatalf("seed record: %v", err)
			}

			if _, err := recordVerdictPluginTrust("", releasePath,
				attachingVerdictPlugins("linter/held-probe"),
				[]verdictPluginResolution{{
					BareURI: "linter/held-probe", Coordinate: "linter/held-probe", Version: "9.9.9", Digest: fresh,
				}}, tc.mode, io.Discard); err != nil {
				t.Fatalf("recordVerdictPluginTrust: %v", err)
			}

			got, ok := verdicttest.TrustRecordAt(t, releasePath).Find("linter/held-probe")
			if !ok {
				t.Fatal("the entry disappeared")
			}
			if got.Digest != tc.want {
				t.Errorf("digest = %s; want %s", got.Digest, tc.want)
			}
		})
	}
}

// TestVerdictTrustCreationNoticeFiresOnceAndSaysWhatToDo pins the notice: it
// fires when the record is CREATED and not on a later pull over the same record,
// it names the file, and it tells the reader to commit it. It must not claim
// anything about version-control state, which the compiler cannot observe.
func TestVerdictTrustCreationNoticeFiresOnceAndSaysWhatToDo(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	resolutions := []verdictPluginResolution{{
		BareURI: "linter/notice-probe", Coordinate: "linter/notice-probe", Version: "1.0.0",
		Digest: "sha256:" + strings.Repeat("4", 64),
	}}

	var first bytes.Buffer
	attached := attachingVerdictPlugins("linter/notice-probe")
	if _, err := recordVerdictPluginTrust("", releasePath, attached, resolutions, catalog.PinReuseMode{}, &first); err != nil {
		t.Fatalf("first record: %v", err)
	}
	notice := first.String()
	for _, want := range []string{pluginstrust.FileName, "commit"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the creation notice should mention %q; got %q", want, notice)
		}
	}
	if strings.Contains(notice, "uncommitted") {
		t.Errorf("the notice must not claim anything about version-control state; got %q", notice)
	}
	if strings.Contains(strings.ToLower(notice), "trust anchor") {
		t.Errorf("the notice must not use that phrase, which names a signing trust root; got %q", notice)
	}

	var second bytes.Buffer
	if _, err := recordVerdictPluginTrust("", releasePath, attached, resolutions, catalog.PinReuseMode{}, &second); err != nil {
		t.Fatalf("second record: %v", err)
	}
	if second.Len() != 0 {
		t.Errorf("the notice is for creation only; a later pull said %q", second.String())
	}
}

// TestVerdictTrustRefusesAnUnreadableRecord holds the fail-closed half: a record
// on disk that does not satisfy its schema is a halt, not a file to replace.
// Replacing it would discard exactly the identities a later resolution is meant
// to be held to.
func TestVerdictTrustRefusesAnUnreadableRecord(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	recordPath := pluginstrust.PathFor(releasePath)
	const garbage = "version: 1\nplugins:\n  - plugin: linter/x\n"
	if err := os.WriteFile(recordPath, []byte(garbage), 0o644); err != nil {
		t.Fatalf("write invalid record: %v", err)
	}

	_, err := recordVerdictPluginTrust("", releasePath,
		attachingVerdictPlugins("linter/probe"),
		[]verdictPluginResolution{{
			BareURI: "linter/probe", Coordinate: "linter/probe", Version: "1.0.0",
			Digest: "sha256:" + strings.Repeat("5", 64),
		}}, catalog.PinReuseMode{}, io.Discard)
	if err == nil {
		t.Fatal("a record that fails its schema must halt rather than be replaced")
	}
	onDisk, rerr := os.ReadFile(recordPath)
	if rerr != nil {
		t.Fatalf("read record back: %v", rerr)
	}
	if string(onDisk) != garbage {
		t.Errorf("the unreadable record must be left exactly as it was; got %q", string(onDisk))
	}
}

// TestVerdictTrustRecordHasOneWriter is the structural half of the auto-pull
// criterion: the record is written from exactly one place, and that place is the
// pull. A build reaches it only by running the pull step; the build's own
// lockfile-driven resolution has no route to it, so no second writer can appear
// without this guard failing.
func TestVerdictTrustRecordHasOneWriter(t *testing.T) {
	callers := enclosingCallers(t)

	got := make([]string, 0, len(callers["pullops.recordVerdictPluginTrust"]))
	for name := range callers["pullops.recordVerdictPluginTrust"] {
		got = append(got, name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "pullops.Run" {
		t.Errorf("callers of recordVerdictPluginTrust = %v; want exactly [pullops.Run]\n"+
			"the record is written by the pull and by nothing else; a second writer would let a "+
			"resolution that acquired nothing overwrite identities it never resolved", got)
	}

	writers := make([]string, 0, len(callers["pluginstrust.Write"]))
	for name := range callers["pluginstrust.Write"] {
		writers = append(writers, name)
	}
	sort.Strings(writers)
	if strings.Join(writers, ",") != "pullops.recordVerdictPluginTrust" {
		t.Errorf("callers of pluginstrust.Write = %v; want exactly [pullops.recordVerdictPluginTrust]", writers)
	}

	// The build's auto-pull step reaches the record by running the pull path
	// verbatim rather than by growing a fill of its own. Assembling the shared
	// build layer's inputs is where that path is handed over, so it is the site
	// this checks.
	//
	// The entry point named here is the RESULT-BEARING one, and naming that one
	// rather than any pull entry is the discriminating part: it hands the finished
	// discovery render back, which is what lets a build that filled its own
	// lockfile present a render it has already performed. The command-line wrapper
	// that discards the resolution satisfies the same prose and not this check, so
	// a hand-over that dropped the conserved render reddens here instead of
	// passing as "still the pull path".
	//
	// 🔴 THIS IS THE STRUCTURAL HALF AND IT IS THE WEAKER ONE. The shared layer
	// invokes what this site hands it through a FUNCTION VALUE, which carries no
	// call edge any source derivation can follow, so nothing structural can see
	// the last hop. TestBuildAutoPullFlagFillsLockfileWhenConfigOff is the half
	// that can: it drives a real build with the fill requested and asserts the
	// lockfile came back with pins. Neither substitutes for the other — this one
	// catches a second fill implementation appearing, and that one catches the
	// hand-off being dropped.
	if !callers["main.pullRunWithResult"]["main.buildDispatchConfig"] {
		t.Errorf("the build dispatch does not hand the result-bearing pull path to the build layer; "+
			"the auto-pull route to the record runs the pull path verbatim and carries its render back "+
			"(callers seen: %v). An empty set names a moved anchor rather than a dropped hand-over: "+
			"the census keys on the entry point's name, so confirm that name before reading this as a regression",
			callers["main.pullRunWithResult"])
	}
}

// TestVerdictTrustRefusesAChangedIdentityUnlessTheRunAsksForIt is the refusal's
// discriminating table. Each row moves the recorded identity on a named axis and
// fixes what the run asked for, so a refusal keyed on the wrong question passes one
// row and fails another.
//
// Three things make it discriminating rather than merely green:
//
//   - The first row is the positive control: an identity that agrees is not a halt,
//     so no later row can pass because the comparison halts on everything.
//   - The seeded record is asserted to hold the coordinate BEFORE the comparison
//     runs, so a row expecting a halt cannot pass on a record that was never
//     written — which is the shape a halt-on-nothing would take.
//   - Every halting row matches the condition rather than "an error was returned",
//     and asserts the values the message must name. A warning and a halt both
//     produce prose; only the returned condition tells them apart.
func TestVerdictTrustRefusesAChangedIdentityUnlessTheRunAsksForIt(t *testing.T) {
	const coordinate = "linter/hold-probe"
	const recordedVersion = "1.0.0"
	recordedDigest := "sha256:" + strings.Repeat("1", 64)
	movedDigest := "sha256:" + strings.Repeat("2", 64)

	cases := []struct {
		name          string
		seedRecord    bool
		resolved      verdictPluginResolution
		mode          catalog.PinReuseMode
		wantHalt      bool
		wantInMessage []string
	}{
		{
			name:       "an_agreeing_identity_is_not_a_halt",
			seedRecord: true,
			resolved:   verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: recordedVersion, Digest: recordedDigest},
			mode:       catalog.PinReuseMode{},
		},
		{
			name:          "a_moved_version_halts",
			seedRecord:    true,
			resolved:      verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: "2.0.0", Digest: recordedDigest},
			mode:          catalog.PinReuseMode{},
			wantHalt:      true,
			wantInMessage: []string{coordinate, pluginstrust.FileName, recordedVersion, "2.0.0"},
		},
		{
			name:          "a_moved_digest_halts",
			seedRecord:    true,
			resolved:      verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: recordedVersion, Digest: movedDigest},
			mode:          catalog.PinReuseMode{},
			wantHalt:      true,
			wantInMessage: []string{coordinate, pluginstrust.FileName, recordedDigest, movedDigest},
		},
		{
			// One message, both axes: a changed version and changed bytes are one
			// disagreement about one identity, so the diagnostic names all four values
			// rather than reporting whichever axis was compared first.
			name:          "both_axes_moved_are_one_halt_naming_all_four_values",
			seedRecord:    true,
			resolved:      verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: "2.0.0", Digest: movedDigest},
			mode:          catalog.PinReuseMode{},
			wantHalt:      true,
			wantInMessage: []string{coordinate, recordedVersion, "2.0.0", recordedDigest, movedDigest},
		},
		{
			name:       "an_identity_the_record_does_not_hold_is_a_first_sighting",
			seedRecord: false,
			resolved:   verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: "2.0.0", Digest: movedDigest},
			mode:       catalog.PinReuseMode{},
		},
		{
			name:       "a_full_refresh_asked_for_the_new_identity",
			seedRecord: true,
			resolved:   verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: "2.0.0", Digest: movedDigest},
			mode:       catalog.PinReuseMode{Upgrade: true},
		},
		{
			name:       "an_upgrade_naming_it_asked_for_the_new_identity",
			seedRecord: true,
			resolved:   verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: "2.0.0", Digest: movedDigest},
			mode:       catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{coordinate}},
		},
		{
			name:          "an_upgrade_naming_something_else_still_halts",
			seedRecord:    true,
			resolved:      verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: "2.0.0", Digest: movedDigest},
			mode:          catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"core/database"}},
			wantHalt:      true,
			wantInMessage: []string{coordinate},
		},
		{
			// A verify-only run writes no record, so it can adopt nothing: it reports
			// whether the committed files still describe the resolution a pull would
			// perform now, and the record is one of those files.
			name:          "a_verify_only_run_covers_the_record_and_halts",
			seedRecord:    true,
			resolved:      verdictPluginResolution{BareURI: coordinate, Coordinate: coordinate, Version: "2.0.0", Digest: movedDigest},
			mode:          catalog.PinReuseMode{VerifyOnly: true},
			wantHalt:      true,
			wantInMessage: []string{coordinate, recordedVersion, "2.0.0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			releasePath := filepath.Join(root, "release.yaml")
			if tc.seedRecord {
				seed := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
				seed.Upsert(pluginstrust.TrustedPlugin{Plugin: coordinate, ResolvedVersion: recordedVersion, Digest: recordedDigest})
				if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), seed); err != nil {
					t.Fatalf("seed record: %v", err)
				}
				// Anti-needle: a halt asserted against a record that was never written
				// would be a halt on nothing.
				if _, held := verdicttest.TrustRecordAt(t, releasePath).Find(coordinate); !held {
					t.Fatalf("seeded record does not hold %s; every halt below would be unattributable", coordinate)
				}
			}

			err := holdVerdictPluginsToTheirRecord(releasePath, []verdictPluginResolution{tc.resolved}, tc.mode)
			if !tc.wantHalt {
				if err != nil {
					t.Fatalf("want no halt; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want a halt; got nil — a resolution disagreeing with the record must stop the run")
			}
			if !errors.Is(err, errVerdictPluginIdentityChanged) {
				t.Errorf("the halt must carry the changed-identity condition; got %v", err)
			}
			for _, want := range tc.wantInMessage {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt must name %q; got %v", want, err)
				}
			}
			wantCommand := "aggrega pull --upgrade --release " + releasePath + " " + coordinate
			if !strings.Contains(err.Error(), wantCommand) {
				t.Errorf("the halt must print the command that adopts the new identity, verbatim and runnable;\nwant it to contain %q\ngot %v", wantCommand, err)
			}
		})
	}
}

// TestVerdictTrustHoldDeclinesWhatTheBinaryCarries is the other half of the
// acquired-artifact rule: the comparison consults the same reduction the write
// does, so a coordinate the binary carries is not held against the record.
//
// It matters because the two sides would otherwise disagree in the one direction
// that cannot be recovered from: the write declines to record such a coordinate, so
// a comparison that did NOT decline would halt against an entry describing a
// different artifact — and no upgrade could ever replace it.
func TestVerdictTrustHoldDeclinesWhatTheBinaryCarries(t *testing.T) {
	const carried = "linter/carried-probe"
	restore := embedded.SeedForTest(carried, verdicttest.HeaderOnlyWasm)
	t.Cleanup(restore)

	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	seed := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
	seed.Upsert(pluginstrust.TrustedPlugin{
		Plugin: carried, ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64),
	})
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), seed); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	// The resolution disagrees with the record on both axes, so anything that did
	// not decline would halt here.
	err := holdVerdictPluginsToTheirRecord(releasePath, []verdictPluginResolution{{
		BareURI: carried, Coordinate: carried, Version: "9.9.9", Digest: "sha256:" + strings.Repeat("b", 64),
	}}, catalog.PinReuseMode{})
	if err != nil {
		t.Fatalf("a coordinate the binary carries must not be held against the record; got %v", err)
	}

	// The discriminator: the same disagreement on a coordinate the binary does NOT
	// carry halts, so the pass above is attributable to the decline rather than to a
	// comparison that never runs.
	const acquired = "linter/acquired-probe"
	seed.Upsert(pluginstrust.TrustedPlugin{
		Plugin: acquired, ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("c", 64),
	})
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), seed); err != nil {
		t.Fatalf("re-seed record: %v", err)
	}
	err = holdVerdictPluginsToTheirRecord(releasePath, []verdictPluginResolution{{
		BareURI: acquired, Coordinate: acquired, Version: "9.9.9", Digest: "sha256:" + strings.Repeat("d", 64),
	}}, catalog.PinReuseMode{})
	if !errors.Is(err, errVerdictPluginIdentityChanged) {
		t.Fatalf("an acquired coordinate's changed identity must halt, or the decline above proves nothing; got %v", err)
	}
}

// TestVerdictTrustHoldRefusesAnUnreadableRecord holds the comparison to the same
// fail-closed reading the write follows: a record that does not satisfy its schema
// is a halt, not an absent record to pass over. Reading it as absent would turn a
// damaged record into a silent skip of the whole comparison.
func TestVerdictTrustHoldRefusesAnUnreadableRecord(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	const garbage = "version: 1\nplugins:\n  - plugin: linter/x\n"
	if err := os.WriteFile(pluginstrust.PathFor(releasePath), []byte(garbage), 0o644); err != nil {
		t.Fatalf("write invalid record: %v", err)
	}

	err := holdVerdictPluginsToTheirRecord(releasePath, []verdictPluginResolution{{
		BareURI: "linter/probe", Coordinate: "linter/probe", Version: "1.0.0",
		Digest: "sha256:" + strings.Repeat("5", 64),
	}}, catalog.PinReuseMode{})
	if err == nil {
		t.Fatal("a record that fails its schema must halt rather than be read as absent")
	}
	if errors.Is(err, errVerdictPluginIdentityChanged) {
		t.Errorf("an unreadable record is not a changed identity; the two conditions must stay distinct: %v", err)
	}
}

// TestVerdictUpgradeTargetsCoverAttachmentsAndTheRecord pins what a targeted
// upgrade may name for a plugin the lockfile does not pin: every configured
// attachment's authored reference with any version suffix removed, AND every
// identity coordinate the record holds.
//
// Two rows are load-bearing for different reasons.
//
// The version-suffix row: a target carrying the suffix would be a string the
// re-resolution never matches, so the upgrade would be admitted and then
// re-resolve nothing — accepted and inert, which is the outcome hardest to notice.
//
// The recorded-but-detached row: that coordinate matches no attachment at all, so
// deriving the accepted targets from the attachments alone would reject it. It is
// the exact string the refusal over a stale entry prints, so rejecting it would
// make that refusal print a command the binary then refuses — and the entry could
// never be dropped.
func TestVerdictUpgradeTargetsCoverAttachmentsAndTheRecord(t *testing.T) {
	cfg := workspaceconfig.Config{
		Plugins: workspaceconfig.PluginsConfig{
			Linter: []workspaceconfig.PluginRef{
				{URI: "linter/publishing-linter:1.2.0"},
				{URI: "linter/bare-linter"},
				{URI: ""},
			},
			Governance: []workspaceconfig.PluginRef{
				{URI: "governance/policy:3.0.0", Role: plugintype.RoleIRVerdict},
				{URI: "governance/labeller:1.0.0", Role: plugintype.RoleLabelTransform},
			},
		},
	}

	t.Run("with_no_record_the_attachments_stand_alone", func(t *testing.T) {
		got := verdictUpgradeTargets(cfg, nil)
		sort.Strings(got)
		want := []string{"governance/policy", "linter/bare-linter", "linter/publishing-linter"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("verdict upgrade targets = %v; want %v\n"+
				"the version suffix is dropped because a named target is matched against the authored reference, "+
				"and a transform-role attachment is not recorded so it is not a target here", got, want)
		}
	})

	t.Run("a_recorded_but_detached_coordinate_is_accepted", func(t *testing.T) {
		record := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
		// Still attached: contributes nothing new, because the attachment already did.
		record.Upsert(pluginstrust.TrustedPlugin{
			Plugin: "linter/bare-linter", ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("1", 64),
		})
		// Detached: named by no attachment, so only the record can vouch for it.
		record.Upsert(pluginstrust.TrustedPlugin{
			Plugin: "linter/detached-probe", ResolvedVersion: "2.0.0", Digest: "sha256:" + strings.Repeat("2", 64),
		})
		// An empty address vouches for nothing, the same skip every other consumer takes.
		record.Plugins = append(record.Plugins, pluginstrust.TrustedPlugin{Plugin: ""})

		got := verdictUpgradeTargets(cfg, record)
		if !slices.Contains(got, "linter/detached-probe") {
			t.Errorf("a coordinate the record holds and nothing attaches must be an accepted target; got %v\n"+
				"without it the command the stale-entry refusal prints is refused as naming nothing", got)
		}
		if slices.Contains(got, "") {
			t.Errorf("an empty recorded address must not become a target; got %v", got)
		}
		// The discriminator: the attachment-only set does NOT carry it, so the row
		// above is earned by the record rather than by the configuration.
		if slices.Contains(verdictUpgradeTargets(cfg, nil), "linter/detached-probe") {
			t.Fatal("the attachment-only set already carries the detached coordinate, so this test cannot discriminate")
		}
	})
}

// TestVerdictTrustRecordHasOneComparisonSite is the structural companion to the
// one-writer guard: the record is compared from exactly one place, and that place
// is the pull.
//
// A second comparison site is the failure this locks out, and it would not show up
// as a wrong answer — it would show up as two answers. One site could hold a
// resolution to the record while another adopted it, and which one a given
// invocation reached would depend on the path taken. The guard also fails if the
// comparison is dropped from the pull path altogether, which is the other way the
// refusal could silently stop existing.
func TestVerdictTrustRecordHasOneComparisonSite(t *testing.T) {
	callers := enclosingCallers(t)

	got := make([]string, 0, len(callers["pullops.holdVerdictPluginsToTheirRecord"]))
	for name := range callers["pullops.holdVerdictPluginsToTheirRecord"] {
		got = append(got, name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "pullops.Run" {
		t.Errorf("callers of holdVerdictPluginsToTheirRecord = %v; want exactly [pullops.Run]\n"+
			"the resolved identities are held to the record by the pull and by nothing else; a second site "+
			"could adopt what the first refused, and which one ran would depend on the path taken", got)
	}

	// The reduction both sides read: neither the comparison nor the decision about
	// what the record becomes may filter the resolutions for itself, or the two
	// could consider different sets.
	//
	// The second reader is the decision rather than the write, and that is the
	// stronger form of the same property: a run that writes and a run that reports
	// what it would write both reach it through that one site, so a reporting path
	// cannot become a third reader with a set of its own.
	readers := make([]string, 0, len(callers["pullops.recordableVerdictResolutions"]))
	for name := range callers["pullops.recordableVerdictResolutions"] {
		readers = append(readers, name)
	}
	sort.Strings(readers)
	if strings.Join(readers, ",") != "pullops.holdVerdictPluginsToTheirRecord,pullops.verdictTrustAfterPull" {
		t.Errorf("callers of recordableVerdictResolutions = %v; want exactly [pullops.holdVerdictPluginsToTheirRecord pullops.verdictTrustAfterPull]", readers)
	}
}

// seedTrustRecord writes a record beside releasePath holding one entry per
// coordinate, and fails the test if the file it wrote does not hold them — so a
// later assertion about an entry surviving or going cannot pass over a record that
// was never written.
func seedTrustRecord(t *testing.T, releasePath string, coordinates ...string) {
	t.Helper()
	seed := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
	for i, coordinate := range coordinates {
		seed.Upsert(pluginstrust.TrustedPlugin{
			Plugin:          coordinate,
			ResolvedVersion: "1.0.0",
			Digest:          "sha256:" + strings.Repeat(string(rune('a'+i)), 64),
		})
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), seed); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	written := verdicttest.TrustRecordAt(t, releasePath)
	for _, coordinate := range coordinates {
		if _, held := written.Find(coordinate); !held {
			t.Fatalf("seeded record does not hold %s; every assertion below would be unattributable (it holds %v)",
				coordinate, verdicttest.RecordedCoordinates(written))
		}
	}
}

// TestVerdictTrustPrunesOnlyOnAnExplicitUpgrade is the discriminating table for
// the two invocations that may drop a recorded entry, and for the ones that may
// not. Every row runs over the identical detached configuration, so the only thing
// separating them is what the run asked for.
//
// The two holding rows are the ones that protect the guard, and they must fail if
// the trigger is ever widened. An ordinary pull that dropped the entry would turn
// a removed gate into a silently accepted removal, which is the whole thing the
// refusal exists to catch; a verify-only run that dropped it would be a
// verification mutating what it verifies.
func TestVerdictTrustPrunesOnlyOnAnExplicitUpgrade(t *testing.T) {
	const stale = "linter/stale-probe"

	cases := []struct {
		name     string
		mode     catalog.PinReuseMode
		wantHeld bool
	}{
		{name: "ordinary_pull_holds_the_stale_entry", mode: catalog.PinReuseMode{}, wantHeld: true},
		{name: "verify_only_holds_the_stale_entry", mode: catalog.PinReuseMode{VerifyOnly: true}, wantHeld: true},
		{name: "full_refresh_prunes_it", mode: catalog.PinReuseMode{Upgrade: true}},
		{
			name: "targeted_upgrade_naming_it_prunes_it",
			mode: catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{stale}},
		},
		{
			name:     "targeted_upgrade_naming_another_holds_it",
			mode:     catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{"linter/somebody-else"}},
			wantHeld: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			releasePath := filepath.Join(root, "release.yaml")
			seedTrustRecord(t, releasePath, stale)

			// Nothing attached and nothing resolved: the entry is accounted for by
			// nothing, which is the condition every row shares.
			if _, err := recordVerdictPluginTrust("", releasePath, nil, nil, tc.mode, io.Discard); err != nil {
				t.Fatalf("recordVerdictPluginTrust: %v", err)
			}

			_, held := verdicttest.TrustRecordAt(t, releasePath).Find(stale)
			if held != tc.wantHeld {
				t.Errorf("entry held = %t; want %t", held, tc.wantHeld)
			}
		})
	}
}

// TestVerdictTrustPruneSubjectIsTheAttachmentSetNotTheRecordableSet is the
// discrimination that decides whether pruning is safe at all, and it is written
// over the two classes the recordable reduction drops while the configuration still
// names them:
//
//   - A plugin the binary carries. It is declined at the write deliberately, so it
//     contributes no recordable resolution — but it is attached, and its entry (put
//     there by a binary that carried nothing, or by a hand edit) must survive.
//   - An attachment whose plugin could not be identified. It contributes no
//     resolution either, and the answer to "what does the configuration account
//     for" is then INCOMPLETE, so nothing at all is pruned.
//
// Both rows run under a full refresh, the widest trigger there is. A prune keyed on
// the recordable set instead of the attachment set passes every other test in this
// file and deletes a live gate's reviewed identity here.
func TestVerdictTrustPruneSubjectIsTheAttachmentSetNotTheRecordableSet(t *testing.T) {
	t.Run("a_coordinate_the_binary_carries_is_still_attached", func(t *testing.T) {
		const carried = "linter/carried-probe"
		restore := embedded.SeedForTest(carried, verdicttest.HeaderOnlyWasm)
		t.Cleanup(restore)

		root := t.TempDir()
		releasePath := filepath.Join(root, "release.yaml")
		seedTrustRecord(t, releasePath, carried)

		// Attached and resolved, but declined from the record because the binary
		// carries it — so it appears in the resolutions and not in the recordable set.
		if _, err := recordVerdictPluginTrust("", releasePath,
			attachingVerdictPlugins(carried),
			[]verdictPluginResolution{{
				BareURI: carried, Coordinate: carried, Version: "1.0.0",
				Digest: "sha256:" + strings.Repeat("9", 64),
			}}, catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}

		if _, held := verdicttest.TrustRecordAt(t, releasePath).Find(carried); !held {
			t.Error("an entry whose plugin is still attached must survive a full refresh, even though the record declines to write it")
		}
	})

	t.Run("a_binary_carried_attachment_does_not_make_the_answer_incomplete", func(t *testing.T) {
		const carried = "linter/carried-probe"
		restore := embedded.SeedForTest(carried, verdicttest.HeaderOnlyWasm)
		t.Cleanup(restore)

		root := t.TempDir()
		releasePath := filepath.Join(root, "release.yaml")
		seedTrustRecord(t, releasePath, carried, "linter/stale-probe")

		// The carried plugin is attached and identified; only the record declines to
		// write it. So the answer is COMPLETE, and the unrelated stale entry goes.
		//
		// This is the other direction of the same confusion, and it fails silently the
		// safe-looking way: reading the coordinates off the recordable set would leave
		// this attachment unidentified, the answer incomplete, and the prune inert — so
		// every workspace attaching a plugin the binary carries would keep its dead end
		// while every test asserting an entry SURVIVES still passed.
		if _, err := recordVerdictPluginTrust("", releasePath,
			attachingVerdictPlugins(carried),
			[]verdictPluginResolution{{
				BareURI: carried, Coordinate: carried, Version: "1.0.0",
				Digest: "sha256:" + strings.Repeat("9", 64),
			}}, catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}

		held := verdicttest.TrustRecordAt(t, releasePath)
		if _, isHeld := held.Find("linter/stale-probe"); isHeld {
			t.Error("a plugin the binary carries is still identified, so the answer is complete and an unrelated stale entry must be pruned")
		}
		if _, isHeld := held.Find(carried); !isHeld {
			t.Error("the carried plugin is attached, so its own entry must survive")
		}
	})

	t.Run("an_unidentifiable_attachment_suppresses_the_prune", func(t *testing.T) {
		root := t.TempDir()
		releasePath := filepath.Join(root, "release.yaml")
		seedTrustRecord(t, releasePath, "linter/stale-probe")

		// One attachment configured and no resolution for it: the plugin could not be
		// identified, so it MIGHT be the one accounting for the entry.
		if _, err := recordVerdictPluginTrust("", releasePath,
			attachingVerdictPlugins("linter/unreadable-probe"), nil,
			catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}
		if _, held := verdicttest.TrustRecordAt(t, releasePath).Find("linter/stale-probe"); !held {
			t.Error("an attachment whose plugin cannot be identified must suppress the prune; dropping an entry on the strength of a failed read names the wrong cause")
		}

		// The discriminator: with that attachment removed the answer is complete
		// again and the identical entry goes — so the row above is earned by the
		// unreadable attachment rather than by a prune that never runs.
		if _, err := recordVerdictPluginTrust("", releasePath, nil, nil,
			catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}
		if _, held := verdicttest.TrustRecordAt(t, releasePath).Find("linter/stale-probe"); held {
			t.Fatal("the prune never fires at all, so the suppression above proves nothing")
		}
	})
}

// TestVerdictTrustPruneEmptiesTheRecordAndLeavesNoNewOne covers the two ends of the
// removed early return in one test, because they are the same question asked of a
// record that exists and of one that does not.
//
// The workspace that detached its LAST verdict attachment resolves nothing, so the
// write path is reached with an empty resolution set — the case an early return on
// that set could never serve, and the case that was permanently halted. It must
// reach the record and empty it. The workspace that never had a record must still
// end with no file: reaching the record is not the same as creating one.
func TestVerdictTrustPruneEmptiesTheRecordAndLeavesNoNewOne(t *testing.T) {
	t.Run("the_last_entry_goes_and_the_file_stays_readable", func(t *testing.T) {
		root := t.TempDir()
		releasePath := filepath.Join(root, "release.yaml")
		seedTrustRecord(t, releasePath, "linter/only-probe")

		if _, err := recordVerdictPluginTrust("", releasePath, nil, nil,
			catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}

		emptied := verdicttest.TrustRecordAt(t, releasePath)
		if len(emptied.Plugins) != 0 {
			t.Fatalf("the record still holds %v", verdicttest.RecordedCoordinates(emptied))
		}
		if pins := emptied.PinMaps(); len(pins.Pins) != 0 {
			t.Errorf("an emptied record pins nothing; got %+v", pins.Pins)
		}
	})

	t.Run("no_record_and_nothing_to_record_leaves_no_file", func(t *testing.T) {
		root := t.TempDir()
		releasePath := filepath.Join(root, "release.yaml")

		var diag bytes.Buffer
		if _, err := recordVerdictPluginTrust("", releasePath, nil, nil,
			catalog.PinReuseMode{Upgrade: true}, &diag); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}
		if _, err := os.Stat(pluginstrust.PathFor(releasePath)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a workspace with nothing to record and no record must end with no file; stat err = %v", err)
		}
		if diag.Len() != 0 {
			t.Errorf("nothing happened, so nothing should be said; diagnostic = %q", diag.String())
		}
	})
}

// TestVerdictTrustAnnouncesEveryChangeAndOnlyAChange is the invocation half of the
// reporting rule, and it is a table because the rule is per-change rather than
// per-file: a pull that says "the record moved" tells a reader nothing they can
// act on, so each way an entry can move is driven through the real writing path
// and the line it prints is asserted.
//
// Three things make it discriminating rather than merely green:
//
//   - Every row asserts what must NOT be said as well as what must. A row that
//     only checked for its own substring would pass on an announcement that also
//     named every unchanged plugin, which is the failure mode that turns the line
//     into a banner.
//   - The no-change row is the positive control in reverse: it fixes the same
//     record and the same resolution and requires silence, so no other row can
//     pass because the announcement fires unconditionally. It also asserts the
//     file was left byte-identical, so "said nothing" cannot mean "wrote and said
//     nothing".
//   - Each row's seeded record is asserted to hold what it claims before the pull
//     runs, so a row expecting a change to an existing entry cannot pass over a
//     record that was never there.
func TestVerdictTrustAnnouncesEveryChangeAndOnlyAChange(t *testing.T) {
	const subject = "linter/announce-probe"
	const second = "linter/second-probe"
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	marker := &pluginstrust.Deprecation{Message: "superseded", Severity: "warning"}
	signature := &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe", BundleDigest: digestB}

	cases := []struct {
		name        string
		seed        []pluginstrust.TrustedPlugin
		attachments []string
		resolutions []verdictPluginResolution
		mode        catalog.PinReuseMode
		wantSilent  bool
		wantSaid    []string
		wantNotSaid []string
	}{
		{
			// A first resolution says both things: the file is new and must be
			// committed, and this is the identity it now holds. Two events, two
			// lines — the creation line carries the commit instruction and the
			// entry line carries the identity, and neither repeats the other.
			name:        "a_first_resolution_names_the_file_and_the_identity",
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA}},
			wantSaid: []string{
				"plugin trust: created", pluginstrust.FileName, "commit",
				subject + ": recorded for the first time, at version 1.0.0 with digest " + digestA,
			},
		},
		{
			name:        "a_new_plugin_in_an_existing_record_is_recorded_without_a_creation_line",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			attachments: []string{subject, second},
			resolutions: []verdictPluginResolution{
				{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA},
				{BareURI: second, Coordinate: second, Version: "2.0.0", Digest: digestB},
			},
			wantSaid: []string{second + ": recorded for the first time, at version 2.0.0 with digest " + digestB},
			// The unchanged entry must not be named, and no file was created.
			wantNotSaid: []string{"created", subject},
		},
		{
			name:        "a_moved_identity_names_both_sides",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "2.0.0", Digest: digestB}},
			mode:        catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{subject}},
			wantSaid: []string{
				subject + ": identity adopted, version 1.0.0 with digest " + digestA +
					" replaced by version 2.0.0 with digest " + digestB,
			},
			wantNotSaid: []string{"recorded for the first time", "created"},
		},
		{
			// The ordinary pull that adds a marker to an entry it already holds:
			// the identity is untouched, so the line says what was added and names
			// the identity the marker is recorded against.
			name:        "a_deprecation_recorded_on_a_held_entry_is_said",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA, Deprecation: marker}},
			wantSaid: []string{
				subject + ": deprecation recorded, against the artifact this entry already names at version 1.0.0 with digest " + digestA,
			},
			wantNotSaid: []string{"identity adopted", "cleared"},
		},
		{
			// The fifth way the record moves, and the one no other row reaches: a
			// run that asked for this plugin to be re-resolved found the same bytes
			// and no marker on them, so the entry loses the deprecation it carried
			// while its identity stays exactly as it was.
			name:        "a_deprecation_cleared_by_a_re_resolution_is_said",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA, Deprecation: marker}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA}},
			mode:        catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{subject}},
			wantSaid: []string{
				subject + ": deprecation cleared, from the entry at version 1.0.0 with digest " + digestA +
					", because re-resolving it found no marker on those bytes",
			},
			wantNotSaid: []string{"identity adopted", "recorded for the first time"},
		},
		{
			name: "a_dropped_entry_names_the_identity_that_went",
			seed: []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			mode: catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{subject}},
			wantSaid: []string{
				subject + ": entry dropped, at version 1.0.0 with digest " + digestA + ", because nothing attaches it any more",
			},
			wantNotSaid: []string{"created", "identity adopted"},
		},
		{
			// An ordinary pull verified a signature for the bytes a held entry
			// already names: the fact is recorded and the identity left alone.
			name:        "a_signature_recorded_on_a_held_entry_is_said",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA, Signature: signature}},
			wantSaid: []string{
				subject + ": signature recorded, against the artifact this entry already names at version 1.0.0 with digest " + digestA,
			},
			wantNotSaid: []string{"identity adopted", "deprecation", "cleared"},
		},
		{
			// Unlike a withdrawal, the signature fact is the answer every pull gives
			// afresh, so an ordinary pull that verified none clears the one held.
			name:        "a_signature_cleared_on_a_held_entry_is_said",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA, Signature: signature}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA}},
			wantSaid: []string{
				subject + ": signature cleared, from the entry at version 1.0.0 with digest " + digestA +
					", because this pull verified no signature for those bytes",
			},
			wantNotSaid: []string{"identity adopted", "deprecation", "recorded"},
		},
		{
			// A re-resolution that found the same identity and both facts new moves
			// the entry twice, and each move is said.
			name:        "a_re_resolution_moving_both_facts_says_each",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA,
				Deprecation: marker, Signature: signature}},
			mode: catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{subject}},
			wantSaid: []string{
				subject + ": deprecation recorded, against the artifact this entry already names at version 1.0.0 with digest " + digestA,
				subject + ": signature recorded, against the artifact this entry already names at version 1.0.0 with digest " + digestA,
			},
			wantNotSaid: []string{"identity adopted", "cleared"},
		},
		{
			// The signature's own silence: a held fact verified again moves nothing.
			name:        "a_held_signature_verified_again_says_nothing",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA, Signature: signature}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA, Signature: signature}},
			wantSilent:  true,
		},
		{
			// The control. Same record, same resolution, nothing moved — so nothing
			// is said AND nothing is written. An announcement that fired here would
			// teach a reader to skip the line that matters.
			name:        "an_ordinary_pull_that_moves_nothing_says_nothing",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA}},
			wantSilent:  true,
		},
		{
			// The SECOND silence, and it is a different code path rather than a
			// restatement of the row above: an ordinary pull holds a recorded entry
			// without comparing it, while a run that asked for re-resolution compares
			// what it resolved against what is held and finds them equal. Only this
			// row reaches that comparison, so without it a defect there would leave
			// every upgrade announcing a change to a record it left untouched.
			name:        "a_re_resolution_finding_the_same_identity_says_nothing",
			seed:        []pluginstrust.TrustedPlugin{{Plugin: subject, ResolvedVersion: "1.0.0", Digest: digestA}},
			attachments: []string{subject},
			resolutions: []verdictPluginResolution{{BareURI: subject, Coordinate: subject, Version: "1.0.0", Digest: digestA}},
			mode:        catalog.PinReuseMode{Upgrade: true, UpgradeTargets: []string{subject}},
			wantSilent:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			releasePath := filepath.Join(root, "release.yaml")
			recordPath := pluginstrust.PathFor(releasePath)

			var before []byte
			if len(tc.seed) > 0 {
				seed := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
				for _, entry := range tc.seed {
					seed.Upsert(entry)
				}
				if err := pluginstrust.Write(recordPath, seed); err != nil {
					t.Fatalf("seed record: %v", err)
				}
				written := verdicttest.TrustRecordAt(t, releasePath)
				for _, entry := range tc.seed {
					held, ok := written.Find(entry.Plugin)
					if !ok || !held.Equal(entry) {
						t.Fatalf("the seeded record does not hold %s as written, so this row would be unattributable; it holds %v",
							entry.Plugin, verdicttest.RecordedCoordinates(written))
					}
				}
				read, rerr := os.ReadFile(recordPath)
				if rerr != nil {
					t.Fatalf("read seeded record: %v", rerr)
				}
				before = read
			}

			var diag bytes.Buffer
			if _, err := recordVerdictPluginTrust("", releasePath,
				attachingVerdictPlugins(tc.attachments...), tc.resolutions, tc.mode, &diag); err != nil {
				t.Fatalf("recordVerdictPluginTrust: %v", err)
			}
			said := diag.String()
			// The whole announcement, so a failure below reads as "this is what it
			// said" rather than only "the substring was missing" — the two are
			// different findings and only the first is actionable.
			t.Logf("announced:\n%s", said)

			if tc.wantSilent {
				if said != "" {
					t.Errorf("nothing moved, so nothing should be said; got %q", said)
				}
				after, rerr := os.ReadFile(recordPath)
				if rerr != nil {
					t.Fatalf("read record back: %v", rerr)
				}
				if !bytes.Equal(before, after) {
					t.Errorf("nothing was said and yet the record moved; silence must mean the file was left alone")
				}
				return
			}

			if said == "" {
				t.Fatalf("the record moved and nothing was said about it")
			}
			for _, want := range tc.wantSaid {
				if !strings.Contains(said, want) {
					t.Errorf("the announcement must say %q; got %q", want, said)
				}
			}
			for _, unwanted := range tc.wantNotSaid {
				if strings.Contains(said, unwanted) {
					t.Errorf("the announcement must not say %q; got %q", unwanted, said)
				}
			}
			// Every line is one change, and every line carries the label a reader
			// scans for. A stray line with another prefix would be a second reporting
			// home growing beside this one.
			for _, line := range strings.Split(strings.TrimSuffix(said, "\n"), "\n") {
				if !strings.HasPrefix(line, "  plugin trust: ") {
					t.Errorf("every announced line carries the record's own label; got %q", line)
				}
			}
		})
	}
}

// TestVerdictTrustAnnouncementHasOneHomeAndNeverFiresOnAReport is the structural
// half of the rule the table above drives.
//
// The announcement is emitted from exactly one place, and that place is the half
// that WRITES. A line emitted from the reporting half would claim a change to a
// file a preview leaves untouched — which is the opposite of what a preview
// promises — and the promise is one a user-visible page now makes, so nothing
// about it may rest on a reader of this package remembering it.
//
// The reporting half additionally holds no writer at all, which is the stronger
// guarantee: wiring the announcement into it does not compile. This guard catches
// the step before that — a second announcement growing somewhere else in the
// package.
func TestVerdictTrustAnnouncementHasOneHomeAndNeverFiresOnAReport(t *testing.T) {
	callers := enclosingCallers(t)

	announcers := make([]string, 0, len(callers["pullops.announceVerdictTrustChanges"]))
	for name := range callers["pullops.announceVerdictTrustChanges"] {
		announcers = append(announcers, name)
	}
	sort.Strings(announcers)
	if strings.Join(announcers, ",") != "pullops.recordVerdictPluginTrust" {
		t.Errorf("callers of announceVerdictTrustChanges = %v; want exactly [pullops.recordVerdictPluginTrust]\n"+
			"the announcement states a change the record has undergone, so it belongs to the half that writes; "+
			"an empty set names a moved anchor rather than a dropped call, so confirm the function's name before "+
			"reading this as a regression", announcers)
	}

	classifiers := make([]string, 0, len(callers["pullops.classifyVerdictTrustUpsert"]))
	for name := range callers["pullops.classifyVerdictTrustUpsert"] {
		classifiers = append(classifiers, name)
	}
	sort.Strings(classifiers)
	if strings.Join(classifiers, ",") != "pullops.verdictTrustAfterPull" {
		t.Errorf("callers of classifyVerdictTrustUpsert = %v; want exactly [pullops.verdictTrustAfterPull]\n"+
			"what changed is decided where the change is made, because once the entry is replaced the record "+
			"holds one identity and there is nothing left to compare against", classifiers)
	}
}

// TestVerdictAttachmentCoordinatesReportsCompleteness pins the derivation the prune
// reads, on the three shapes that behave differently: every attachment identified,
// one not identified, and no attachments at all.
//
// The last row is the one that matters most and reads counter-intuitively: a
// configuration attaching NOTHING is a COMPLETE answer accounting for nothing,
// which is what makes the whole-gate removal prunable. Reported as incomplete it
// would leave exactly the workspace this mechanism exists for unable to clear its
// record.
func TestVerdictAttachmentCoordinatesReportsCompleteness(t *testing.T) {
	resolved := []verdictPluginResolution{
		{BareURI: "linter/one", Coordinate: "linter/one", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("1", 64)},
		{BareURI: "governance/two", Coordinate: "governance/two", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("2", 64)},
	}

	cases := []struct {
		name         string
		attachments  []workspaceconfig.VerdictAttachment
		want         string
		wantComplete bool
	}{
		{
			name:         "every_attachment_identified",
			attachments:  attachingVerdictPlugins("linter/one:3.2.1", "governance/two"),
			want:         "linter/one,governance/two",
			wantComplete: true,
		},
		{
			name:         "one_attachment_unidentified_makes_the_answer_incomplete",
			attachments:  attachingVerdictPlugins("linter/one:3.2.1", "linter/unreadable"),
			want:         "linter/one",
			wantComplete: false,
		},
		{
			name:         "no_attachments_is_a_complete_answer_accounting_for_nothing",
			attachments:  nil,
			want:         "",
			wantComplete: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accounted, complete := verdictAttachmentCoordinates(tc.attachments, resolved)
			if got := strings.Join(accounted, ","); got != tc.want {
				t.Errorf("accounted = %q; want %q", got, tc.want)
			}
			if complete != tc.wantComplete {
				t.Errorf("complete = %t; want %t", complete, tc.wantComplete)
			}
		})
	}
}
