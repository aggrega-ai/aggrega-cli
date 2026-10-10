// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// A plugin attached at a verdict role is held to the signature its record carries
// wherever it is loaded, not only by a build. A governance plugin (attached at
// ir-verdict) and a linter plugin are published signed to a real in-process
// registry; a consumer whose Release resolves entirely from that registry pulls them
// under a signing requirement, so the record beside the lockfile carries both
// verified facts and both bundles are cached. A Release-scoped validation, which
// runs the governance plugin, and a Release-scoped lint, which loads the linter, are
// then run over that record and over edited copies of it. One edit per case, each
// against the pristine record.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIntegration_LintAndValidateHoldAVerdictPluginToItsRecordedSignature is the
// matrix over a real pull's record, for the two surfaces that load a verdict plugin
// outside a build.
func TestIntegration_LintAndValidateHoldAVerdictPluginToItsRecordedSignature(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	policyGuest := filepath.Join(wd, "..", "..", "internal", "wasm", "testdata", "policy-allow-go.wasm")
	linterGuest := buildVersionedLinterWasm(t, "1.0.0")

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	host := startRegistry(t, nil)
	publishSignedService(t, host, signer)

	// Both plugins are real guests, published signed through the production push,
	// because each surface loads its plugin before it runs anything: a validation
	// dispatches the governance plugin, and a Release-scoped lint reads the linter's
	// describe() and then hands it the Release. The linter is the publishing linter's
	// guest, built to report a version, with its entity generated from that guest so
	// the publish's cross-check accepts the pair, and published under a name of its
	// own so it resolves from the content cache rather than from the copy inside the
	// binary.
	pubws := t.TempDir()
	writeWorkspaceConfig(t, pubws, "workspace:\n  catalog_dirs: []\noffline: false\ndefault_registry: \""+host+"\"\n")
	governancePath := filepath.Join(pubws, "plugin", "governance", "allow-gate", "1.0.0", "plugin.yaml")
	writeFixture(t, governancePath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: allow-gate
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  exports:
    - name: verdict_ir
`)
	sign := true
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: pubws}),
		EntityPath: governancePath, PluginPath: policyGuest, Registry: host, Sign: &sign, Signer: signer}); err != nil {
		t.Fatalf("publish the governance plugin signed: %v", err)
	}
	const governanceURI = "governance/allow-gate:1.0.0"
	governance := types.PluginCoordinate("governance", "allow-gate")
	linterEntity, linterVersion := genNonEmbeddedLinterEntity(t, linterGuest, "load-gate")
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: pubws}),
		EntityPath: linterEntity, PluginPath: linterGuest, Registry: host, Sign: &sign, Signer: signer}); err != nil {
		t.Fatalf("publish the linter signed: %v", err)
	}
	linter := types.PluginCoordinate("linter", "load-gate")
	linterURI := linter + ":" + linterVersion

	ws, releasePath := writeSignedServiceConsumer(t)
	recordPath := pluginstrust.PathFor(releasePath)
	config := func(required bool) string {
		body := recordedSignatureStance{topRequired: required}.config(host, pubPEM)
		return body + "plugins:\n  governance:\n    - uri: " + governanceURI + "\n      role: ir-verdict\n" +
			"  linter:\n    - uri: " + linterURI + "\n"
	}
	writeWorkspaceConfig(t, ws, config(true))
	var pullDiag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
		ReleasePath: releasePath, Out: io.Discard, DiagOut: &pullDiag}); err != nil {
		t.Fatalf("pull under a requirement: %v\n%s", err, pullDiag.String())
	}
	pristine, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read the record the pull wrote: %v", err)
	}
	facts := map[string]lockfile.VerifiedSignature{}
	for _, coordinate := range []string{governance, linter} {
		entry, held := verdicttest.TrustRecordAt(t, releasePath).Find(coordinate)
		if !held || entry.Signature == nil || entry.Signature.BundleDigest == "" {
			t.Fatalf("the pull cached the signed plugin %q and wrote no signature fact for it: %+v", coordinate, entry)
		}
		facts[coordinate] = *entry.Signature
	}

	// Each surface reports its refusal its own way: a validation prints every
	// diagnostic and returns a count, so its refusal is read from what it printed,
	// by the condition's registered message; a lint returns the condition itself.
	surfaces := []struct {
		name       string
		coordinate string
		run        func(t *testing.T) (string, error)
		refused    func(err error, printed string, want error) bool
	}{
		{name: "validate --release", coordinate: governance,
			run: func(t *testing.T) (string, error) {
				var out, diag bytes.Buffer
				err := validateRelease(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath,
					nil, "detailed", false, false, &diag, &out)
				return diag.String(), err
			},
			refused: func(err error, printed string, want error) bool {
				return err != nil && strings.Contains(printed, want.Error())
			}},
		{name: "lint --release", coordinate: linter,
			run: func(t *testing.T) (string, error) {
				var out, diag bytes.Buffer
				err := lintops.RunRelease(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath,
					nil, nil, &out, &diag, nil)
				return diag.String(), err
			},
			refused: func(err error, _ string, want error) bool { return errors.Is(err, want) }},
	}
	for _, surface := range surfaces {
		stripOne := func(t *testing.T) {
			t.Helper()
			record := verdicttest.TrustRecordAt(t, releasePath)
			for i := range record.Plugins {
				if record.Plugins[i].Plugin == surface.coordinate {
					record.Plugins[i].Signature = nil
				}
			}
			if err := pluginstrust.Write(recordPath, record); err != nil {
				t.Fatalf("strip the record's signature: %v", err)
			}
		}
		bundlePath := oci.SignatureBundlePath(ws, ".aggrega/cache", facts[surface.coordinate].BundleDigest)
		rows := []struct {
			name     string
			required bool
			edit     func(*testing.T)
			tamper   bool
			want     error
		}{
			{name: "the record as the pull wrote it", required: true},
			{name: "the plugin's signature removed from the record", required: true, edit: stripOne,
				want: lockfile.ErrSignatureNotRecorded()},
			{name: "the record deleted", required: true, edit: func(t *testing.T) {
				if err := os.Remove(recordPath); err != nil {
					t.Fatalf("delete the record: %v", err)
				}
			}, want: lockfile.ErrSignatureNotRecorded()},
			{name: "the plugin's cached bundle tampered", required: true, tamper: true,
				want: oci.ErrSignatureBundleDigestMismatch()},
			{name: "the signature removed, no requirement", edit: stripOne},
		}
		for _, row := range rows {
			t.Run(surface.name+": "+row.name, func(t *testing.T) {
				if err := os.WriteFile(recordPath, pristine, 0o644); err != nil {
					t.Fatalf("restore the pristine record: %v", err)
				}
				t.Cleanup(func() { _ = os.WriteFile(recordPath, pristine, 0o644) })
				writeWorkspaceConfig(t, ws, config(row.required))
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
				printed, err := surface.run(t)
				if row.want == nil {
					if err != nil {
						t.Fatalf("%s must go ahead: %v\n%s", surface.name, err, printed)
					}
					return
				}
				if !surface.refused(err, printed, row.want) {
					t.Fatalf("want %v, got %v\n%s", row.want, err, printed)
				}
				if !strings.Contains(err.Error()+printed, `"`+surface.coordinate) {
					t.Errorf("the refusal does not name the plugin %q:\n%v\n%s", surface.coordinate, err, printed)
				}
			})
		}
	}
}
