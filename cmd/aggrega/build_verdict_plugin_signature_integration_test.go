// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// The build's hold on a verdict-role plugin's recorded signature, over facts a real
// pull recorded: a linter plugin is published signed to a real in-process
// registry, a consumer whose Release resolves from its own catalog directory
// attaches it and pulls under a signing requirement — so the record beside the
// lockfile carries the verified fact and the bundle is cached — and the build then
// runs with no fill over that record and over edited copies of it. The lockfile
// holds no row for the plugin, so every refusal below can only come from the
// record. One edit or one setting per case, each against the pristine record.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// publishSignedVerdictPlugin publishes a verdict-socket plugin to host as the
// single OCI manifest a pull caches it from, and attaches signer's signature over
// that manifest as a referrer — the shape the signing publish path leaves, packed
// directly because this fixture carries an import-free stub module rather than a
// compiled guest.
func publishSignedVerdictPlugin(t *testing.T, host, socket, name, version string, signer signing.Signer) string {
	t.Helper()
	return publishSignedVerdictPluginEntity(t, host, socket, name, version, verdicttest.PluginEntityYAML(socket, name, version), signer)
}

// publishSignedVerdictPluginEntity is publishSignedVerdictPlugin with the Plugin
// entity supplied by the caller, for a fixture whose entity says something the
// stub module does not — a host function it does not import, say.
func publishSignedVerdictPluginEntity(t *testing.T, host, socket, name, version, entityYAML string, signer signing.Signer) string {
	t.Helper()
	ctx := context.Background()
	reference := host + "/" + catalog.OCILocation("Plugin", socket, name)
	repo, err := oci.NewRemoteRepository(reference, http.DefaultClient)
	if err != nil {
		t.Fatalf("open registry repository %s: %v", reference, err)
	}
	manifestDigest, err := oci.PushPluginManifest(ctx, repo, version, []byte(entityYAML), verdicttest.HeaderOnlyWasm, nil)
	if err != nil {
		t.Fatalf("publish verdict plugin %s: %v", reference, err)
	}
	bundle, artifactType, err := signer.Sign(ctx, manifestDigest)
	if err != nil {
		t.Fatalf("sign verdict plugin %s: %v", reference, err)
	}
	if _, err := oci.AttachReferrer(ctx, repo, version, artifactType, bundle); err != nil {
		t.Fatalf("attach the verdict plugin's signature %s: %v", reference, err)
	}
	return types.PluginCoordinate(socket, name) + ":" + version
}

// verdictSignatureStance is the trust configuration one case runs under.
type verdictSignatureStance struct {
	topRequired, registryRequired, advisory, hostSkip, offline bool
}

// config renders the consumer's configuration for the stance: the Release resolved
// from the workspace's own catalog directory, the registry the plugin was published
// to as the default with its public key pinned, and the plugin attached at the
// linter socket.
func (s verdictSignatureStance) config(root, host, uri string, pubPEM []byte) string {
	var b strings.Builder
	b.WriteString("workspace:\n  catalog_dirs:\n    - " + filepath.Join(root, "catalog") + "\n")
	b.WriteString("default_registry: \"" + host + "\"\n")
	b.WriteString("auto_pull: false\n")
	if s.offline {
		b.WriteString("offline: true\n")
	} else {
		b.WriteString("offline: false\n")
	}
	if s.topRequired {
		b.WriteString("signing:\n  required: true\n")
	}
	b.WriteString("plugins:\n  linter:\n    - uri: " + uri + "\n")
	b.WriteString("registries:\n  - host: \"" + host + "\"\n")
	if s.registryRequired {
		b.WriteString("    signing:\n      required: true\n")
	}
	if s.advisory {
		b.WriteString("    advisory: true\n")
	}
	if s.hostSkip {
		b.WriteString("    skip_signature_verification: true\n")
	}
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// TestIntegration_TheBuildHoldsAVerdictPluginToItsRecordedSignature is the matrix
// over a real pull's record, and the pull's repair of a record that lost its fact.
func TestIntegration_TheBuildHoldsAVerdictPluginToItsRecordedSignature(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	host := startRegistry(t, nil)
	uri := publishSignedVerdictPlugin(t, host, "linter", "signed-gate", "1.0.0", signer)
	coordinate := types.PluginCoordinate("linter", "signed-gate")

	root := t.TempDir()
	releasePath := authoringFixture(t, root)
	recordPath := pluginstrust.PathFor(releasePath)
	t.Chdir(root)

	// pull runs a pull under the stance and fails where its own render — the one
	// that discovers external inputs, and that the check runs in — could not finish:
	// that render precedes the record's write, so it must be holding the plugin to
	// the record this run composed and not to the file it is replacing.
	pull := func(t *testing.T, stance verdictSignatureStance) error {
		t.Helper()
		writeWorkspaceConfig(t, root, stance.config(root, host, uri, pubPEM))
		var out, diag bytes.Buffer
		err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath, Out: &out, DiagOut: &diag})
		t.Logf("pull diagnostics:\n%s", diag.String())
		if strings.Contains(diag.String(), "could not finish rendering") {
			t.Errorf("the pull's own render did not finish:\n%s", diag.String())
		}
		return err
	}
	if err := pull(t, verdictSignatureStance{topRequired: true}); err != nil {
		t.Fatalf("pull the signed verdict plugin under a requirement: %v", err)
	}
	pristine, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read the record the pull wrote: %v", err)
	}
	t.Logf("record the pull wrote:\n%s", pristine)
	entry, held := verdicttest.TrustRecordAt(t, releasePath).Find(coordinate)
	if !held || entry.Signature == nil || entry.Signature.BundleDigest == "" {
		t.Fatalf("the pull cached a signed verdict plugin and wrote no signature fact for it: %+v", entry)
	}
	fact := *entry.Signature
	// The lockfile holds no row for the plugin, so a refusal below is the record's.
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	for _, row := range lf.VerifiedFactRows() {
		if strings.Contains(row.URI, "signed-gate") {
			t.Fatalf("a verdict-role plugin must carry no lockfile row; found %q", row.URI)
		}
	}

	stripSignature := func(t *testing.T) {
		t.Helper()
		record := verdicttest.TrustRecordAt(t, releasePath)
		for i := range record.Plugins {
			record.Plugins[i].Signature = nil
		}
		if err := pluginstrust.Write(recordPath, record); err != nil {
			t.Fatalf("strip the record's signature: %v", err)
		}
	}
	deleteRecord := func(t *testing.T) {
		t.Helper()
		if err := os.Remove(recordPath); err != nil {
			t.Fatalf("delete the record: %v", err)
		}
	}
	bundlePath := oci.SignatureBundlePath(root, ".aggrega/cache", fact.BundleDigest)
	notRecorded := []string{`"` + coordinate + `"`, pluginstrust.FileName + " records no signature for it"}

	rows := []struct {
		name     string
		stance   verdictSignatureStance
		edit     func(*testing.T)
		tamper   bool
		want     error
		wantText []string
	}{
		{name: "the record as the pull wrote it builds", stance: verdictSignatureStance{topRequired: true}},
		{name: "the record's signature removed is refused", stance: verdictSignatureStance{topRequired: true},
			edit: stripSignature, want: lockfile.ErrSignatureNotRecorded(),
			wantText: append(notRecorded, "`signing.required` at the top level")},
		{name: "offline, the same plugin is refused", stance: verdictSignatureStance{topRequired: true, offline: true},
			edit: stripSignature, want: lockfile.ErrSignatureNotRecorded(), wantText: notRecorded},
		{name: "deleting the record does not admit it", stance: verdictSignatureStance{topRequired: true},
			edit: deleteRecord, want: lockfile.ErrSignatureNotRecorded(), wantText: notRecorded},
		{name: "the registry's own requirement refuses it", stance: verdictSignatureStance{registryRequired: true},
			edit: stripSignature, want: lockfile.ErrSignatureNotRecorded(),
			wantText: append(notRecorded, "`signing.required` on the `registries` entry for \""+host+"\"")},
		{name: "an advisory registry admits it", stance: verdictSignatureStance{registryRequired: true, advisory: true},
			edit: stripSignature},
		{name: "the registry's own skip admits it", stance: verdictSignatureStance{registryRequired: true, hostSkip: true},
			edit: stripSignature},
		{name: "with no requirement it builds", stance: verdictSignatureStance{}, edit: stripSignature},
		{name: "a tampered cached bundle is refused", stance: verdictSignatureStance{topRequired: true},
			tamper: true, want: oci.ErrSignatureBundleDigestMismatch(), wantText: []string{`"` + coordinate + `"`}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := os.WriteFile(recordPath, pristine, 0o644); err != nil {
				t.Fatalf("restore the pristine record: %v", err)
			}
			if row.edit != nil {
				row.edit(t)
			}
			if row.tamper {
				saved, err := os.ReadFile(bundlePath)
				if err != nil {
					t.Fatalf("read the cached bundle: %v", err)
				}
				if err := os.WriteFile(bundlePath, []byte("rewritten after the pull"), 0o644); err != nil {
					t.Fatalf("tamper the cached bundle: %v", err)
				}
				t.Cleanup(func() { _ = os.WriteFile(bundlePath, saved, 0o644) })
			}
			writeWorkspaceConfig(t, root, row.stance.config(root, host, uri, pubPEM))
			var out, diag bytes.Buffer
			err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false, &diag, &out)
			if row.want == nil {
				if err != nil {
					t.Fatalf("the build must go ahead: %v", err)
				}
				return
			}
			if !errors.Is(err, row.want) {
				t.Fatalf("want %v, got %v", row.want, err)
			}
			for _, text := range row.wantText {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("the refusal does not carry %q:\n%v", text, err)
				}
			}
		})
	}

	// The remedy the refusal names repairs the record: an ordinary pull over the
	// stripped record verifies the plugin again and writes the same fact back onto
	// the entry it already holds, and the build then goes ahead.
	t.Run("an ordinary pull writes the lost fact back", func(t *testing.T) {
		if err := os.WriteFile(recordPath, pristine, 0o644); err != nil {
			t.Fatalf("restore the pristine record: %v", err)
		}
		stripSignature(t)
		if err := pull(t, verdictSignatureStance{topRequired: true}); err != nil {
			t.Fatalf("pull over the stripped record: %v", err)
		}
		repaired, held := verdicttest.TrustRecordAt(t, releasePath).Find(coordinate)
		if !held || repaired.Signature == nil || *repaired.Signature != fact {
			t.Fatalf("the pull did not write the verified fact back: %+v; want %+v", repaired.Signature, fact)
		}
		var out, diag bytes.Buffer
		if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false, &diag, &out); err != nil {
			t.Fatalf("the build over the repaired record must go ahead: %v", err)
		}
	})
}
