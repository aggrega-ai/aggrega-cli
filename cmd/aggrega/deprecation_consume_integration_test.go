// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// deprecationConsumerConfigBody builds a workspace config for the
// consume-side deprecation tests: default_registry + auto_pull, and a single
// registry whose signature policy and deprecation-halt dials the caller
// controls. requiresSignatures pins the keyed publisher key and demands a
// signature; !requiresSignatures leaves the registry verify-if-present (an
// unknown-key marker is then dropped rather than halting). haltSeverity sets
// build.halt_on_deprecated_severity; haltAuthored sets build.halt_on_deprecated.
func deprecationConsumerConfigBody(host string, pinnedPubPEM []byte, requiresSignatures bool, haltSeverity string, haltAuthored bool) string {
	var b strings.Builder
	b.WriteString("default_registry: \"" + host + "\"\n")
	b.WriteString("auto_pull: true\n")
	b.WriteString("offline: false\n")
	b.WriteString("build:\n")
	if haltSeverity != "" {
		b.WriteString("  halt_on_deprecated_severity: \"" + haltSeverity + "\"\n")
	}
	if haltAuthored {
		b.WriteString("  halt_on_deprecated: true\n")
	}
	b.WriteString("registries:\n")
	b.WriteString("  - host: \"" + host + "\"\n")
	if requiresSignatures {
		b.WriteString("    signing:\n      required: true\n")
	}
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pinnedPubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// writeDeprecationConsumerCatalog publishes a Component + Recipe
// SIGNED by signer to the registry at host, then writes a consumer workspace
// wired to build the Component over that registry, with the given config body.
// It chdir's into the workspace and returns the Component's bare closure URI —
// the lockfile row key a discovered marker lands on. The `deprecated` flag, when
// set, marks the Component's metadata.deprecated: true at authoring time.
func writeDeprecationConsumerCatalog(t *testing.T, host string, signer signing.Signer, configBody string, authorDeprecated bool) string {
	t.Helper()
	cat := t.TempDir()

	componentBody := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
`
	if authorDeprecated {
		componentBody += "  deprecated: true\n"
	}
	componentBody += `spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
`
	componentPath := filepath.Join(cat, "component/test/svc/1.0.0/component.yaml")
	writeFixture(t, componentPath, componentBody)
	recipePath := filepath.Join(cat, "recipe/helm/svc/1.0.0/recipe.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra `+"`port`"+` 8080 }}
`)
	pushCatalogEntitySigned(t, host, componentPath, signer)
	pushCatalogEntitySigned(t, host, recipePath, signer)

	ws := t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeWorkspaceConfig(t, ws, configBody)
	t.Chdir(ws)
	return "test/svc"
}

// componentDeprecateRef is the registry reference `aggrega deprecate` addresses
// the fixture Component by: <host>/component/test/svc:1.0.0.
func componentDeprecateRef(host string) string {
	return host + "/component/test/svc:1.0.0"
}

// TestIntegration_ConsumeRetroactiveDeprecation drives the whole two-moment
// consume path end-to-end against a real in-process registry: publish + sign a
// Component through the production push, retroactively deprecate it through the
// production `aggrega deprecate` (a signed marker attached as a referrer), then
// LOCK it through the production pullRun (which discovers + verifies + records
// the marker into the lockfile) and BUILD it through dispatchBuild (which
// enforces from the lockfile only, touching no registry).
//
// The severity arms prove the threshold gate; the author-time arm proves the
// second source under the same gate; the untrusted-marker arm proves a marker
// signed by a key the policy does not trust is not honored as authoritative.
func TestIntegration_ConsumeRetroactiveDeprecation(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	// deprecate attaches a signed marker to the fixture Component through the
	// production deprecate flow with the given signer (defaults to the publisher).
	deprecate := func(t *testing.T, host, severity, message string, markerSigner signing.Signer) {
		t.Helper()
		if markerSigner == nil {
			markerSigner = signer
		}
		if derr := deprecateRun(deprecateConfig{
			Reference: componentDeprecateRef(host),
			Severity:  severity,
			Reason:    message,
			Message:   message,
			Signer:    markerSigner,
			EnvLookup: func(string) string { return "" },
		}); derr != nil {
			t.Fatalf("deprecateRun (%s): %v", severity, derr)
		}
	}

	componentURI := "test/svc"

	t.Run("warning_severity_advises_build_succeeds", func(t *testing.T) {
		host := startRegistry(t, nil)
		// halt threshold "error": a "warning" marker is below it → advisory only.
		cfg := deprecationConsumerConfigBody(host, pubPEM, true, "error", false)
		writeDeprecationConsumerCatalog(t, host, signer, cfg, false)
		deprecate(t, host, "warning", "migrate to test/svc2 soon", nil)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("pull with a warning-severity marker must succeed; got: %v", perr)
		}
		// The marker was recorded onto the Component's lockfile row.
		rec := deprecationRowRecord(t, componentURI)
		if rec == nil {
			t.Fatal("a verified warning marker was not recorded on the lockfile row")
		}
		if rec.Severity != "warning" {
			t.Fatalf("recorded severity = %q, want warning", rec.Severity)
		}
		if rec.Signer == nil || rec.Signer.Mode != "keyed" {
			t.Fatalf("recorded marker signer is missing or not keyed: %+v", rec.Signer)
		}

		// Build succeeds: warning is below the halt threshold, so it advises.
		if berr := dispatchBuild(nil, "release.yaml", nil, false, io.Discard, io.Discard); berr != nil {
			t.Fatalf("build with a below-threshold marker must succeed; got: %v", berr)
		}
	})

	t.Run("error_severity_halts_build", func(t *testing.T) {
		host := startRegistry(t, nil)
		cfg := deprecationConsumerConfigBody(host, pubPEM, true, "error", false)
		writeDeprecationConsumerCatalog(t, host, signer, cfg, false)
		deprecate(t, host, "error", "no longer fit for new use", nil)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("pull with an error-severity marker must succeed (halt is at build); got: %v", perr)
		}
		berr := dispatchBuild(nil, "release.yaml", nil, false, io.Discard, io.Discard)
		if !errors.Is(berr, deprecation.ErrDeprecatedEntity) {
			t.Fatalf("build must HALT with ErrDeprecatedEntity at severity==threshold; got: %v", berr)
		}
	})

	t.Run("retracted_severity_halts_build", func(t *testing.T) {
		host := startRegistry(t, nil)
		// Threshold "retracted": only a retracted marker halts; below it advises.
		cfg := deprecationConsumerConfigBody(host, pubPEM, true, "retracted", false)
		writeDeprecationConsumerCatalog(t, host, signer, cfg, false)
		deprecate(t, host, "retracted", "withdrawn; use test/svc2", nil)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("pull with a retracted marker must succeed; got: %v", perr)
		}
		berr := dispatchBuild(nil, "release.yaml", nil, false, io.Discard, io.Discard)
		if !errors.Is(berr, deprecation.ErrDeprecatedEntity) {
			t.Fatalf("build must HALT with ErrDeprecatedEntity for a retracted marker at retracted threshold; got: %v", berr)
		}
	})

	t.Run("author_time_deprecated_halts_under_halt_on_deprecated", func(t *testing.T) {
		host := startRegistry(t, nil)
		// No retroactive marker; the Component itself is metadata.deprecated:true.
		// halt_on_deprecated:true → the author-time source halts.
		cfg := deprecationConsumerConfigBody(host, pubPEM, true, "error", true)
		writeDeprecationConsumerCatalog(t, host, signer, cfg, true)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("pull of an author-deprecated component must succeed; got: %v", perr)
		}
		berr := dispatchBuild(nil, "release.yaml", nil, false, io.Discard, io.Discard)
		if !errors.Is(berr, deprecation.ErrDeprecatedEntity) {
			t.Fatalf("an author-deprecated entity must HALT under halt_on_deprecated; got: %v", berr)
		}
	})

	t.Run("author_time_deprecated_advises_when_halt_off", func(t *testing.T) {
		host := startRegistry(t, nil)
		// halt_on_deprecated omitted (false): author-deprecation advises only.
		cfg := deprecationConsumerConfigBody(host, pubPEM, true, "error", false)
		writeDeprecationConsumerCatalog(t, host, signer, cfg, true)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("pull must succeed; got: %v", perr)
		}
		if berr := dispatchBuild(nil, "release.yaml", nil, false, io.Discard, io.Discard); berr != nil {
			t.Fatalf("author-deprecation with halt off must ADVISE, not halt; got: %v", berr)
		}
	})

	t.Run("untrusted_marker_not_honored", func(t *testing.T) {
		host := startRegistry(t, nil)
		// A verify-if-present registry (no signing.required): the artifact
		// signatures still verify against the pinned key, but a marker signed by a
		// DIFFERENT key does not match the pinned key and is not honored — so it is
		// dropped, the row carries no deprecation record, and the build succeeds
		// even at a halting threshold. This isolates "untrusted marker" from
		// "trusted marker": were the marker honored, the retracted severity would
		// halt the build.
		cfg := deprecationConsumerConfigBody(host, pubPEM, false, "warning", false)
		writeDeprecationConsumerCatalog(t, host, signer, cfg, false)

		// Sign the marker with a key the workspace does not pin.
		otherPriv, _, gerr := signing.GenerateKeyedPEM()
		if gerr != nil {
			t.Fatalf("GenerateKeyedPEM (untrusted): %v", gerr)
		}
		otherSigner, gerr := signing.LoadKeyedSigner(otherPriv, nil)
		if gerr != nil {
			t.Fatalf("LoadKeyedSigner (untrusted): %v", gerr)
		}
		deprecate(t, host, "retracted", "attacker-supplied retraction", otherSigner)

		if perr := pullRun(pullConfig{ReleasePath: "release.yaml"}); perr != nil {
			t.Fatalf("pull under a verify-if-present policy must succeed even with an untrusted marker; got: %v", perr)
		}
		if rec := deprecationRowRecord(t, componentURI); rec != nil {
			t.Fatalf("an untrusted marker must NOT be recorded as authoritative; got: %+v", rec)
		}
		if berr := dispatchBuild(nil, "release.yaml", nil, false, io.Discard, io.Discard); berr != nil {
			t.Fatalf("an unrecorded (untrusted) marker must not affect the build; got: %v", berr)
		}
	})
}

// deprecationRowRecord loads the written lockfile and returns the deprecation
// record on the row for componentURI, or nil when the row carries none.
func deprecationRowRecord(t *testing.T, componentURI string) *lockfile.DeprecationRecord {
	t.Helper()
	lf, _, lerr := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if lerr != nil {
		t.Fatalf("load lockfile: %v", lerr)
	}
	for i := range lf.Entities {
		if lf.Entities[i].URI == componentURI {
			return lf.Entities[i].Deprecation
		}
	}
	t.Fatalf("component row %q absent from lockfile", componentURI)
	return nil
}
