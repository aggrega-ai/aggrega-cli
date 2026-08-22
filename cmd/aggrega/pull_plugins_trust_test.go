// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// headerOnlyWasm is a valid, import-free Wasm 1.0 module: the four magic bytes
// and the version word, and nothing else. It parses as a module with no imports,
// so a plugin entity carrying no host-function stamp cross-checks against it
// cleanly — which is what lets these tests exercise the identity plumbing without
// compiling a real guest.
var headerOnlyWasm = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// verdictPluginEntityYAML is a schema-valid Plugin entity for a verdict-socket
// plugin at the given socket, bare name and version.
func verdictPluginEntityYAML(socket, name, version string) string {
	return "apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Plugin\n" +
		"metadata:\n" +
		"  name: " + name + "\n" +
		"  version: \"" + version + "\"\n" +
		"  pluginType: " + socket + "\n" +
		"spec:\n" +
		"  abiVersion: v1alpha1\n" +
		"  exports:\n" +
		"    - name: validate\n"
}

// cachedVerdictPlugin describes a verdict-socket plugin seeded into a workspace:
// the versioned entity URI an attachment names, the identity coordinate a record
// is addressed by, the content digest of the plugin's .wasm layer, and the OCI
// manifest digest — which is a DIFFERENT digest, and is the one a record must not
// carry.
type cachedVerdictPlugin struct {
	URI            string
	Coordinate     string
	WasmDigest     string
	ManifestDigest string
	ManifestBytes  []byte
	EntityYAML     string
}

// seedCachedVerdictPlugin lays a verdict-socket plugin into a workspace the way a
// completed pull leaves it: the Plugin entity in a catalog directory, and in the
// content cache the entity's config blob, the OCI manifest committing to it, and
// the .wasm blob addressed by its own content digest.
//
// The manifest is built by the production packer rather than assembled here, so
// the .wasm layer digest and the manifest digest are the real pair — which is what
// makes a test that confuses the two able to fail.
func seedCachedVerdictPlugin(t *testing.T, root, catalogDir, socket, name, version string) cachedVerdictPlugin {
	t.Helper()

	entityYAML := verdictPluginEntityYAML(socket, name, version)
	coordinate := types.PluginCoordinate(socket, name)
	uri := coordinate + ":" + version

	store := memory.New()
	if _, err := oci.PushPluginManifest(context.Background(), store, version, []byte(entityYAML), headerOnlyWasm, nil); err != nil {
		t.Fatalf("pack plugin manifest for %s: %v", uri, err)
	}
	pulled, err := oci.FetchPluginEntity(context.Background(), store, version)
	if err != nil {
		t.Fatalf("read back packed plugin manifest for %s: %v", uri, err)
	}

	entityPath := filepath.Join(catalogDir, "plugin", socket, name, version, "plugin.yaml")
	if err := os.MkdirAll(filepath.Dir(entityPath), 0o755); err != nil {
		t.Fatalf("mkdir catalog entity dir: %v", err)
	}
	if err := os.WriteFile(entityPath, []byte(entityYAML), 0o644); err != nil {
		t.Fatalf("write catalog entity %s: %v", entityPath, err)
	}

	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	wasmDigest := digest.ContentDigest(headerOnlyWasm)
	if err := oci.WriteCached(root, cacheDir, uri, version, []byte(entityYAML)); err != nil {
		t.Fatalf("seed cached entity blob for %s: %v", uri, err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, uri, version, pulled.ManifestBytes); err != nil {
		t.Fatalf("seed cached manifest for %s: %v", uri, err)
	}
	if err := oci.WriteCached(root, cacheDir, coordinate, wasmDigest, headerOnlyWasm); err != nil {
		t.Fatalf("seed cached .wasm for %s: %v", coordinate, err)
	}

	return cachedVerdictPlugin{
		URI:            uri,
		Coordinate:     coordinate,
		WasmDigest:     wasmDigest,
		ManifestDigest: pulled.Digest,
		ManifestBytes:  pulled.ManifestBytes,
		EntityYAML:     entityYAML,
	}
}

// trustRecordAt loads the trust record beside releasePath, failing the test when
// it is absent or unreadable.
func trustRecordAt(t *testing.T, releasePath string) *pluginstrust.Trust {
	t.Helper()
	trust, err := pluginstrust.Load(pluginstrust.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load trust record beside %s: %v", releasePath, err)
	}
	return trust
}

// recordedCoordinates returns the coordinates a trust record holds, sorted.
func recordedCoordinates(trust *pluginstrust.Trust) []string {
	var out []string
	for _, p := range trust.Plugins {
		out = append(out, p.Plugin)
	}
	sort.Strings(out)
	return out
}

// TestVerdictTrustNoVerdictConfigurationWritesNoFile proves the negative
// criterion: a pull over a workspace that attaches no governance plugin and no
// linter plugin produces NO trust record — not an empty one — and says nothing
// about it on either sink.
//
// An empty file would be the wrong outcome and the easy one to ship by accident:
// it reads as "these attachments were checked and there were none", which is a
// claim, where absence is the honest first-run state.
func TestVerdictTrustNoVerdictConfigurationWritesNoFile(t *testing.T) {
	root := t.TempDir()
	releasePath := authoringFixture(t, root)

	var stdout, stderr bytes.Buffer
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("pullRun: %v (stderr=%q)", err, stderr.String())
	}

	recordPath := pluginstrust.PathFor(releasePath)
	if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a pull with no verdict-returning attachment must write no record at %s; stat err = %v", recordPath, err)
	}
	if strings.Contains(stderr.String(), pluginstrust.FileName) {
		t.Errorf("nothing was recorded, so nothing should be said about the record; stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Pull complete") {
		t.Fatalf("expected the pull summary on stdout; got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "trusted_plugins") {
		t.Errorf("the completion line must carry no count when nothing was recorded; got %q", stdout.String())
	}
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
	seeded := seedCachedVerdictPlugin(t, root, catalogDir, "governance", "trust-probe", "2.4.1")

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
	seeded := seedCachedVerdictPlugin(t, root, catalogDir, "governance", "trust-probe", "2.4.1")

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
	restore := embedded.SeedForTest(carried, headerOnlyWasm)
	t.Cleanup(restore)

	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")

	var diag bytes.Buffer
	recorded, err := recordVerdictPluginTrust(releasePath, []verdictPluginResolution{
		{BareURI: carried, Coordinate: carried, Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64)},
		{BareURI: "linter/acquired-probe", Coordinate: "linter/acquired-probe", Version: "2.0.0", Digest: "sha256:" + strings.Repeat("b", 64)},
	}, catalog.PinReuseMode{}, &diag)
	if err != nil {
		t.Fatalf("recordVerdictPluginTrust: %v", err)
	}
	if recorded != 1 {
		t.Errorf("recorded count = %d; want 1 (the carried coordinate is declined)", recorded)
	}
	trust := trustRecordAt(t, releasePath)
	if want := []string{"linter/acquired-probe"}; strings.Join(recordedCoordinates(trust), ",") != strings.Join(want, ",") {
		t.Errorf("record holds %v; want exactly %v", recordedCoordinates(trust), want)
	}
}

// TestVerdictTrustDeclinesEverythingCarriedWritesNoFile is the boundary case the
// decline shares with an unconfigured workspace: when every attachment resolves to
// a coordinate the binary carries, there is nothing to record and therefore no
// file, rather than a file holding an empty list.
func TestVerdictTrustDeclinesEverythingCarriedWritesNoFile(t *testing.T) {
	const carried = "linter/carried-probe"
	restore := embedded.SeedForTest(carried, headerOnlyWasm)
	t.Cleanup(restore)

	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")

	var diag bytes.Buffer
	recorded, err := recordVerdictPluginTrust(releasePath, []verdictPluginResolution{
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
	recorded, err := recordVerdictPluginTrust(releasePath, []verdictPluginResolution{{
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
	if _, err := recordVerdictPluginTrust(releasePath, []verdictPluginResolution{
		{BareURI: "linter/held-probe", Coordinate: "linter/held-probe", Version: "9.9.9", Digest: fresh},
		{BareURI: "linter/added-probe", Coordinate: "linter/added-probe", Version: "2.0.0", Digest: added},
	}, catalog.PinReuseMode{}, &diag); err != nil {
		t.Fatalf("recordVerdictPluginTrust: %v", err)
	}

	trust := trustRecordAt(t, releasePath)
	got, ok := trust.Find("linter/held-probe")
	if !ok {
		t.Fatalf("the present entry disappeared; record holds %v", recordedCoordinates(trust))
	}
	if got.Digest != held {
		t.Errorf("present entry digest = %s; want the held %s (an ordinary pull must not overwrite it)", got.Digest, held)
	}
	if got.ResolvedVersion != "1.0.0" {
		t.Errorf("present entry version = %q; want the held 1.0.0", got.ResolvedVersion)
	}
	addedRec, ok := trust.Find("linter/added-probe")
	if !ok {
		t.Fatalf("the missing entry was not filled; record holds %v", recordedCoordinates(trust))
	}
	if addedRec.Digest != added {
		t.Errorf("filled entry digest = %s; want %s", addedRec.Digest, added)
	}
	if diag.Len() != 0 {
		t.Errorf("the record already existed, so nothing should announce a creation; diagnostic = %q", diag.String())
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

			if _, err := recordVerdictPluginTrust(releasePath, []verdictPluginResolution{{
				BareURI: "linter/held-probe", Coordinate: "linter/held-probe", Version: "9.9.9", Digest: fresh,
			}}, tc.mode, io.Discard); err != nil {
				t.Fatalf("recordVerdictPluginTrust: %v", err)
			}

			got, ok := trustRecordAt(t, releasePath).Find("linter/held-probe")
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
	if _, err := recordVerdictPluginTrust(releasePath, resolutions, catalog.PinReuseMode{}, &first); err != nil {
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
	if _, err := recordVerdictPluginTrust(releasePath, resolutions, catalog.PinReuseMode{}, &second); err != nil {
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

	_, err := recordVerdictPluginTrust(releasePath, []verdictPluginResolution{{
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

// TestTrustedPluginsSummary pins the completion-line field: absent at zero,
// present with the count otherwise. Absent rather than `=0` because a workspace
// attaching no verdict-returning plugin was never asked the question.
func TestTrustedPluginsSummary(t *testing.T) {
	if got := trustedPluginsSummary(0); got != "" {
		t.Errorf("trustedPluginsSummary(0) = %q; want the empty string", got)
	}
	if got := trustedPluginsSummary(3); got != ", trusted_plugins=3" {
		t.Errorf("trustedPluginsSummary(3) = %q; want %q", got, ", trusted_plugins=3")
	}
}

// TestVerdictTrustRecordHasOneWriter is the structural half of the auto-pull
// criterion: the record is written from exactly one place, and that place is the
// pull. A build reaches it only by running the pull step; the build's own
// lockfile-driven resolution has no route to it, so no second writer can appear
// without this guard failing.
func TestVerdictTrustRecordHasOneWriter(t *testing.T) {
	callers := enclosingCallers(t)

	got := make([]string, 0, len(callers["recordVerdictPluginTrust"]))
	for name := range callers["recordVerdictPluginTrust"] {
		got = append(got, name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "pullRun" {
		t.Errorf("callers of recordVerdictPluginTrust = %v; want exactly [pullRun]\n"+
			"the record is written by the pull and by nothing else; a second writer would let a "+
			"resolution that acquired nothing overwrite identities it never resolved", got)
	}

	writers := make([]string, 0, len(callers["pluginstrust.Write"]))
	for name := range callers["pluginstrust.Write"] {
		writers = append(writers, name)
	}
	sort.Strings(writers)
	if strings.Join(writers, ",") != "recordVerdictPluginTrust" {
		t.Errorf("callers of pluginstrust.Write = %v; want exactly [recordVerdictPluginTrust]", writers)
	}

	if !callers["pullRun"]["acquireLockfile"] {
		t.Errorf("acquireLockfile does not call pullRun; the auto-pull route to the record runs the pull path verbatim (callers seen: %v)",
			callers["pullRun"])
	}
}
