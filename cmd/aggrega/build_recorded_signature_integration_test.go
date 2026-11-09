// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// The build's check of recorded signature facts over facts a real pull recorded:
// a Component and its Recipe are published signed to a real in-process registry,
// a consumer pulls them under a signing requirement — so each row records the
// verified signature and its bundle is cached — and the build then runs with no
// fill over that lockfile and over edited copies of it. One edit or one setting per
// case, each against the pristine lockfile the pull wrote.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

const recordedSignatureComponent = "test/svc"

// publishSignedService pushes a Component and the Recipe implementing it to host,
// each signed with signer through the production publish path.
func publishSignedService(t *testing.T, host string, signer signing.Signer) {
	t.Helper()
	pubws := t.TempDir()
	writeWorkspaceConfig(t, pubws, "workspace:\n  catalog_dirs: []\noffline: false\ndefault_registry: \""+host+"\"\n")
	component := filepath.Join(pubws, "component", "test", "svc", "1.0.0", "component.yaml")
	writeFixture(t, component, `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
`)
	recipe := filepath.Join(pubws, "recipe", "helm", "svc", "1.0.0", "recipe.yaml")
	writeFixture(t, recipe, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra `+"`port`"+` 8080 }}
`)
	sign := true
	for _, entity := range []string{component, recipe} {
		if err := pushRun(t.Context(), pushConfig{
			Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: pubws}),
			EntityPath: entity,
			Registry:   host,
			Sign:       &sign,
			Signer:     signer,
		}); err != nil {
			t.Fatalf("publish %s signed: %v", entity, err)
		}
	}
}

// recordedSignatureStance is the trust configuration one case runs under.
type recordedSignatureStance struct {
	topRequired, registryRequired, advisory, hostSkip, offline, autoPull bool
}

// config renders the consumer's configuration for the stance: the registry the
// service was published to as the default, its public key pinned, and nothing
// read from a catalog directory.
func (s recordedSignatureStance) config(host string, pubPEM []byte) string {
	var b strings.Builder
	b.WriteString("workspace:\n  catalog_dirs: []\n")
	b.WriteString("default_registry: \"" + host + "\"\n")
	if s.autoPull {
		b.WriteString("auto_pull: true\n")
	} else {
		b.WriteString("auto_pull: false\n")
	}
	if s.offline {
		b.WriteString("offline: true\n")
	} else {
		b.WriteString("offline: false\n")
	}
	if s.topRequired {
		b.WriteString("signing:\n  required: true\n")
	}
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

// writeSignedServiceConsumer writes a consumer of the published service — an
// Aggregate beside the Release using the Component, and a Manifest binding the
// Recipe — and returns the workspace and the Release's path. Its configuration is
// the caller's to write.
func writeSignedServiceConsumer(t *testing.T) (ws, releasePath string) {
	t.Helper()
	ws = t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc: helm/svc:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
	return ws, filepath.Join(ws, "release.yaml")
}

// TestIntegration_TheBuildHoldsEveryPulledRowToItsRecordedSignature is the matrix
// over a real pull's facts.
func TestIntegration_TheBuildHoldsEveryPulledRowToItsRecordedSignature(t *testing.T) {
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

	ws, releasePath := writeSignedServiceConsumer(t)
	lockPath := lockfile.PathFor(releasePath)
	writeWorkspaceConfig(t, ws, recordedSignatureStance{topRequired: true}.config(host, pubPEM))
	var pullOut, pullDiag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
		ReleasePath: releasePath, Out: &pullOut, DiagOut: &pullDiag}); err != nil {
		t.Fatalf("pull the signed service under a requirement: %v\n%s", err, pullDiag.String())
	}
	pristine, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the pull wrote: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	var componentFact *lockfile.VerifiedSignature
	for _, e := range lf.Entities {
		if e.URI == recordedSignatureComponent {
			componentFact = e.Signature
		}
	}
	if componentFact == nil || componentFact.BundleDigest == "" {
		t.Fatalf("the pull recorded no signature for %q, so no case below starts from a recorded fact: %+v",
			recordedSignatureComponent, lf.Entities)
	}

	// editComponentRow rewrites the pristine lockfile with the Component's row
	// changed by edit.
	editComponentRow := func(t *testing.T, edit func(*lockfile.Entity)) {
		t.Helper()
		copyOf, _, err := lockfile.LoadLockfile(lockPath)
		if err != nil {
			t.Fatalf("load the lockfile: %v", err)
		}
		for i := range copyOf.Entities {
			if copyOf.Entities[i].URI == recordedSignatureComponent {
				edit(&copyOf.Entities[i])
			}
		}
		if err := lockfile.WriteLockfile(lockPath, copyOf); err != nil {
			t.Fatalf("write the edited lockfile: %v", err)
		}
	}
	stripSignature := func(e *lockfile.Entity) { e.Signature = nil }
	stripSignatureAndManifest := func(e *lockfile.Entity) {
		e.Signature = nil
		e.ManifestDigest = ""
	}
	bundlePath := oci.SignatureBundlePath(ws, ".aggrega/cache", componentFact.BundleDigest)

	rows := []struct {
		name     string
		stance   recordedSignatureStance
		edit     func(*lockfile.Entity)
		tamper   bool
		want     error
		wantText []string
	}{
		{name: "the lockfile as the pull wrote it builds", stance: recordedSignatureStance{topRequired: true}},
		{name: "the Component row's signature removed is refused", stance: recordedSignatureStance{topRequired: true},
			edit: stripSignature, want: lockfile.ErrSignatureNotRecorded(),
			wantText: []string{`"` + recordedSignatureComponent + `"`, "`signing.required` at the top level"}},
		{name: "offline, the same row is refused", stance: recordedSignatureStance{topRequired: true, offline: true},
			edit: stripSignature, want: lockfile.ErrSignatureNotRecorded()},
		{name: "removing the manifest digest too does not admit it", stance: recordedSignatureStance{topRequired: true},
			edit: stripSignatureAndManifest, want: lockfile.ErrSignatureNotRecorded()},
		{name: "the registry's own requirement refuses it", stance: recordedSignatureStance{registryRequired: true},
			edit: stripSignature, want: lockfile.ErrSignatureNotRecorded(),
			wantText: []string{"`signing.required` on the `registries` entry for \"" + host + "\""}},
		{name: "an advisory registry admits it", stance: recordedSignatureStance{registryRequired: true, advisory: true},
			edit: stripSignature},
		{name: "the registry's own skip admits it", stance: recordedSignatureStance{registryRequired: true, hostSkip: true},
			edit: stripSignature},
		{name: "with no requirement it builds", stance: recordedSignatureStance{}, edit: stripSignature},
		{name: "a tampered cached bundle is refused", stance: recordedSignatureStance{topRequired: true},
			tamper: true, want: oci.ErrSignatureBundleDigestMismatch(),
			wantText: []string{`"` + recordedSignatureComponent + `"`}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := os.WriteFile(lockPath, pristine, 0o644); err != nil {
				t.Fatalf("restore the pristine lockfile: %v", err)
			}
			if row.edit != nil {
				editComponentRow(t, row.edit)
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
			writeWorkspaceConfig(t, ws, row.stance.config(host, pubPEM))
			var out, diag bytes.Buffer
			err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath, nil, false, &diag, &out)
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
}
