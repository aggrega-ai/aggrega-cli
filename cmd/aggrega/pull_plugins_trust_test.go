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
	"slices"
	"sort"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
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
	recorded, err := recordVerdictPluginTrust(releasePath,
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
	recorded, err := recordVerdictPluginTrust(releasePath,
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
	recorded, err := recordVerdictPluginTrust(releasePath,
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
	if _, err := recordVerdictPluginTrust(releasePath,
		attachingVerdictPlugins("linter/held-probe", "linter/added-probe"),
		[]verdictPluginResolution{
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

			if _, err := recordVerdictPluginTrust(releasePath,
				attachingVerdictPlugins("linter/held-probe"),
				[]verdictPluginResolution{{
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
	attached := attachingVerdictPlugins("linter/notice-probe")
	if _, err := recordVerdictPluginTrust(releasePath, attached, resolutions, catalog.PinReuseMode{}, &first); err != nil {
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
	if _, err := recordVerdictPluginTrust(releasePath, attached, resolutions, catalog.PinReuseMode{}, &second); err != nil {
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

	_, err := recordVerdictPluginTrust(releasePath,
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
			// A verify-only run writes nothing, so it can adopt nothing: it reports
			// whether the committed files still describe what resolves today, and the
			// record is one of those files.
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
				if _, held := trustRecordAt(t, releasePath).Find(coordinate); !held {
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
	restore := embedded.SeedForTest(carried, headerOnlyWasm)
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

	got := make([]string, 0, len(callers["holdVerdictPluginsToTheirRecord"]))
	for name := range callers["holdVerdictPluginsToTheirRecord"] {
		got = append(got, name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "pullRun" {
		t.Errorf("callers of holdVerdictPluginsToTheirRecord = %v; want exactly [pullRun]\n"+
			"the resolved identities are held to the record by the pull and by nothing else; a second site "+
			"could adopt what the first refused, and which one ran would depend on the path taken", got)
	}

	// The reduction both sides read: neither the comparison nor the write may
	// filter the resolutions for itself, or the two could consider different sets.
	readers := make([]string, 0, len(callers["recordableVerdictResolutions"]))
	for name := range callers["recordableVerdictResolutions"] {
		readers = append(readers, name)
	}
	sort.Strings(readers)
	if strings.Join(readers, ",") != "holdVerdictPluginsToTheirRecord,recordVerdictPluginTrust" {
		t.Errorf("callers of recordableVerdictResolutions = %v; want exactly [holdVerdictPluginsToTheirRecord recordVerdictPluginTrust]", readers)
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
	written := trustRecordAt(t, releasePath)
	for _, coordinate := range coordinates {
		if _, held := written.Find(coordinate); !held {
			t.Fatalf("seeded record does not hold %s; every assertion below would be unattributable (it holds %v)",
				coordinate, recordedCoordinates(written))
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
			if _, err := recordVerdictPluginTrust(releasePath, nil, nil, tc.mode, io.Discard); err != nil {
				t.Fatalf("recordVerdictPluginTrust: %v", err)
			}

			_, held := trustRecordAt(t, releasePath).Find(stale)
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
		restore := embedded.SeedForTest(carried, headerOnlyWasm)
		t.Cleanup(restore)

		root := t.TempDir()
		releasePath := filepath.Join(root, "release.yaml")
		seedTrustRecord(t, releasePath, carried)

		// Attached and resolved, but declined from the record because the binary
		// carries it — so it appears in the resolutions and not in the recordable set.
		if _, err := recordVerdictPluginTrust(releasePath,
			attachingVerdictPlugins(carried),
			[]verdictPluginResolution{{
				BareURI: carried, Coordinate: carried, Version: "1.0.0",
				Digest: "sha256:" + strings.Repeat("9", 64),
			}}, catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}

		if _, held := trustRecordAt(t, releasePath).Find(carried); !held {
			t.Error("an entry whose plugin is still attached must survive a full refresh, even though the record declines to write it")
		}
	})

	t.Run("a_binary_carried_attachment_does_not_make_the_answer_incomplete", func(t *testing.T) {
		const carried = "linter/carried-probe"
		restore := embedded.SeedForTest(carried, headerOnlyWasm)
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
		if _, err := recordVerdictPluginTrust(releasePath,
			attachingVerdictPlugins(carried),
			[]verdictPluginResolution{{
				BareURI: carried, Coordinate: carried, Version: "1.0.0",
				Digest: "sha256:" + strings.Repeat("9", 64),
			}}, catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}

		held := trustRecordAt(t, releasePath)
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
		if _, err := recordVerdictPluginTrust(releasePath,
			attachingVerdictPlugins("linter/unreadable-probe"), nil,
			catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}
		if _, held := trustRecordAt(t, releasePath).Find("linter/stale-probe"); !held {
			t.Error("an attachment whose plugin cannot be identified must suppress the prune; dropping an entry on the strength of a failed read names the wrong cause")
		}

		// The discriminator: with that attachment removed the answer is complete
		// again and the identical entry goes — so the row above is earned by the
		// unreadable attachment rather than by a prune that never runs.
		if _, err := recordVerdictPluginTrust(releasePath, nil, nil,
			catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}
		if _, held := trustRecordAt(t, releasePath).Find("linter/stale-probe"); held {
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

		if _, err := recordVerdictPluginTrust(releasePath, nil, nil,
			catalog.PinReuseMode{Upgrade: true}, io.Discard); err != nil {
			t.Fatalf("recordVerdictPluginTrust: %v", err)
		}

		emptied := trustRecordAt(t, releasePath)
		if len(emptied.Plugins) != 0 {
			t.Fatalf("the record still holds %v", recordedCoordinates(emptied))
		}
		if pins := emptied.PinMaps(); len(pins.Pins) != 0 {
			t.Errorf("an emptied record pins nothing; got %+v", pins.Pins)
		}
	})

	t.Run("no_record_and_nothing_to_record_leaves_no_file", func(t *testing.T) {
		root := t.TempDir()
		releasePath := filepath.Join(root, "release.yaml")

		var diag bytes.Buffer
		if _, err := recordVerdictPluginTrust(releasePath, nil, nil,
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

// TestPullRefusesADamagedRecordEvenWithNothingAttached locks the reading a pull
// now takes of a record that exists and does not satisfy its schema: it halts,
// whether or not the workspace attaches anything.
//
// It is the same fail-closed reading every other consumer of the file takes, and
// the asymmetry it removes is the one that mattered: a build and a release-scoped
// lint already halted on a damaged record unconditionally, so a pull that passed
// over it left the two commands disagreeing about whether the committed file was
// usable.
//
// The halt is not attributable to ONE read, and that is worth stating rather than
// guessing at: a pull now reads the record at two points — once to decide which
// upgrade targets it accepts, and once at the write — and EITHER one is sufficient
// on its own. So a change that made one of them lenient would leave this test
// green, and only making both lenient loses the halt.
//
// The A/B is the second half: the identical workspace with the file removed pulls
// clean, so the halt is attributable to the record's bytes rather than to the
// fixture.
func TestPullRefusesADamagedRecordEvenWithNothingAttached(t *testing.T) {
	root := t.TempDir()
	releasePath := authoringFixture(t, root)
	recordPath := pluginstrust.PathFor(releasePath)
	// Schema-invalid: an entry missing the required resolved_version and digest.
	const damaged = "version: 1\nplugins:\n  - plugin: linter/x\n"
	if err := os.WriteFile(recordPath, []byte(damaged), 0o644); err != nil {
		t.Fatalf("write damaged record: %v", err)
	}

	pull := func() error {
		var stdout, stderr bytes.Buffer
		return pullRun(pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath,
			CatalogDirs: []string{filepath.Join(root, "catalog")},
			DiagOut:     &stderr,
			Out:         &stdout,
		})
	}

	if err := pull(); err == nil {
		t.Fatal("a record that does not satisfy its schema must halt the pull rather than be passed over")
	}
	onDisk, rerr := os.ReadFile(recordPath)
	if rerr != nil {
		t.Fatalf("read the record back: %v", rerr)
	}
	if string(onDisk) != damaged {
		t.Errorf("a refused pull must leave the record exactly as it was; got %q", string(onDisk))
	}

	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("remove the damaged record: %v", err)
	}
	if err := pull(); err != nil {
		t.Fatalf("with no record the identical workspace must pull clean, or the halt above proves nothing; got %v", err)
	}
}
