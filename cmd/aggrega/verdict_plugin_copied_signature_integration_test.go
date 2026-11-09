// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// A plugin attached at a verdict role is held to which artifact its recorded
// signature was made for wherever it is loaded, not only by a build. A governance
// plugin (attached at ir-verdict) and a linter plugin are published signed to a
// real in-process registry, so their signatures are made for two different
// manifests; a consumer pulls them under a signing requirement, so the record
// beside the lockfile carries both verified facts and both bundles are cached. One
// plugin's entry is then given the other plugin's `signature` — an edit to the
// record alone, every bundle left intact — and a Release-scoped validation, which
// runs the governance plugin, and a Release-scoped lint, which loads the linter,
// are run over it.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIntegration_LintAndValidateRefuseAVerdictPluginSignatureCopiedFromAnotherEntry
// is the two surfaces that load a verdict plugin outside a build, each over the
// pull's own record and over the record with the plugin's entry carrying the other
// plugin's signature, under a requirement and under none.
func TestIntegration_LintAndValidateRefuseAVerdictPluginSignatureCopiedFromAnotherEntry(t *testing.T) {
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

	pubws := t.TempDir()
	writeWorkspaceConfig(t, pubws, "workspace:\n  catalog_dirs: []\noffline: false\ndefault_registry: \""+host+"\"\n")
	governancePath := filepath.Join(pubws, "plugin", "governance", "copy-gate", "1.0.0", "plugin.yaml")
	writeFixture(t, governancePath, `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: copy-gate
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
	const governanceURI = "governance/copy-gate:1.0.0"
	governance := types.PluginCoordinate("governance", "copy-gate")
	linterEntity, linterVersion := genNonEmbeddedLinterEntity(t, linterGuest, "copy-load-gate")
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: pubws}),
		EntityPath: linterEntity, PluginPath: linterGuest, Registry: host, Sign: &sign, Signer: signer}); err != nil {
		t.Fatalf("publish the linter signed: %v", err)
	}
	linter := types.PluginCoordinate("linter", "copy-load-gate")
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
	for _, coordinate := range []string{governance, linter} {
		entry, held := verdicttest.TrustRecordAt(t, releasePath).Find(coordinate)
		if !held || entry.Signature == nil || entry.Signature.BundleDigest == "" {
			t.Fatalf("the pull cached the signed plugin %q and wrote no signature fact for it: %+v", coordinate, entry)
		}
	}

	surfaces := []struct {
		name          string
		coordinate    string
		other         string
		run           func(t *testing.T) (string, error)
		refusedAsCopy func(err error, printed string) bool
	}{
		{name: "validate --release", coordinate: governance, other: linter,
			run: func(t *testing.T) (string, error) {
				var out, diag bytes.Buffer
				err := validateRelease(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath,
					nil, "detailed", false, false, &diag, &out)
				return diag.String(), err
			},
			refusedAsCopy: func(err error, printed string) bool {
				return err != nil && strings.Contains(printed, oci.ErrSignatureForAnotherArtifact().Error())
			}},
		{name: "lint --release", coordinate: linter, other: governance,
			run: func(t *testing.T) (string, error) {
				var out, diag bytes.Buffer
				err := lintops.RunRelease(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath,
					nil, nil, &out, &diag, nil)
				return diag.String(), err
			},
			refusedAsCopy: func(err error, _ string) bool { return errors.Is(err, oci.ErrSignatureForAnotherArtifact()) }},
	}
	for _, surface := range surfaces {
		copyOnto := func(t *testing.T) {
			t.Helper()
			record := verdicttest.TrustRecordAt(t, releasePath)
			from, _ := record.Find(surface.other)
			fact := *from.Signature
			for i := range record.Plugins {
				if record.Plugins[i].Plugin == surface.coordinate {
					record.Plugins[i].Signature = &fact
				}
			}
			if err := pluginstrust.Write(recordPath, record); err != nil {
				t.Fatalf("write the edited record: %v", err)
			}
		}
		rows := []struct {
			name     string
			required bool
			copied   bool
		}{
			{name: "the record as the pull wrote it", required: true},
			{name: "the other plugin's signature copied onto its entry, under a requirement", required: true, copied: true},
			{name: "the other plugin's signature copied onto its entry, under no requirement", copied: true},
		}
		for _, row := range rows {
			t.Run(surface.name+": "+row.name, func(t *testing.T) {
				if err := os.WriteFile(recordPath, pristine, 0o644); err != nil {
					t.Fatalf("restore the pristine record: %v", err)
				}
				t.Cleanup(func() { _ = os.WriteFile(recordPath, pristine, 0o644) })
				writeWorkspaceConfig(t, ws, config(row.required))
				if row.copied {
					copyOnto(t)
				}
				printed, err := surface.run(t)
				if !row.copied {
					if err != nil {
						t.Fatalf("%s must go ahead over the pull's own record: %v\n%s", surface.name, err, printed)
					}
					return
				}
				if !surface.refusedAsCopy(err, printed) {
					t.Fatalf("want the signature-for-another-artifact refusal, got %v\n%s", err, printed)
				}
				if !strings.Contains(err.Error()+printed, `"`+surface.coordinate) {
					t.Errorf("the refusal does not name the plugin %q:\n%v\n%s", surface.coordinate, err, printed)
				}
				if !strings.Contains(err.Error()+printed, recordPath+" records it") {
					t.Errorf("the refusal does not name the record that carries the copied signature:\n%v\n%s", err, printed)
				}
			})
		}
	}
}
