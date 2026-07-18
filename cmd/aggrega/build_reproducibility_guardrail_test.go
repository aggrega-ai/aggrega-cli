// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// guardrailFrozenLookup answers only from vars, yielding "" for every other key,
// so the build-path resolution (the global layer read and the second,
// tree-only provenance pass) reads a fixed environment.
func guardrailFrozenLookup(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// TestBuildReproducibilityGuardrailWarnsAndSucceeds drives the build path with a
// provider binding supplied only by the global config layer. The guardrail must
// emit a warning on the diagnostics writer naming the binding and the
// `aggrega config capture` remedy, and it must never be the cause of a build
// failure — the warning lands before the plugin-consuming pipeline. Once the
// binding is captured into the workspace tree, the warning is gone.
func TestBuildReproducibilityGuardrailWarnsAndSucceeds(t *testing.T) {
	xdg := t.TempDir()
	writeFixture(t, filepath.Join(xdg, "aggrega", "config.yaml"),
		"plugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n")

	root := t.TempDir()
	// A workspace-anchoring config with no provider binding of its own, so the
	// helm provider resolves only from the global layer.
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, "kind: Release\n")

	lookup := guardrailFrozenLookup(map[string]string{"XDG_CONFIG_HOME": xdg})

	rw, err := workspaceconfig.Resolve(root, "", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var diag, out bytes.Buffer
	// The build itself may fail later for unrelated reasons (no real catalog or
	// closure here); the guardrail warning is emitted before the pipeline, so it
	// is present regardless of the build's eventual outcome. The guardrail must
	// not itself be the failure.
	buildErr := dispatchBuild(rw, releasePath, []string{root}, false, &diag, &out)

	warn := diag.String()
	if !strings.Contains(warn, "aggrega config capture") {
		t.Errorf("guardrail warning missing the remedy `aggrega config capture`; diag=%q (buildErr=%v)", warn, buildErr)
	}
	if !strings.Contains(warn, "plugins.providers") || !strings.Contains(warn, "helm") {
		t.Errorf("guardrail warning did not name the global-only binding; diag=%q", warn)
	}

	// Capture the effective build-critical config into the workspace, then a
	// fresh resolution of the same tree must carry the binding locally and the
	// guardrail must fall silent.
	dest := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := workspaceconfig.CaptureBuildCritical(dest, rw.Config(), false); err != nil {
		t.Fatalf("CaptureBuildCritical: %v", err)
	}
	rw2, err := workspaceconfig.Resolve(root, "", lookup)
	if err != nil {
		t.Fatalf("Resolve (post-capture): %v", err)
	}
	var diag2, out2 bytes.Buffer
	_ = dispatchBuild(rw2, releasePath, []string{root}, false, &diag2, &out2)
	if strings.Contains(diag2.String(), "aggrega config capture") {
		t.Errorf("guardrail still warned after capture; diag=%q", diag2.String())
	}
}

// TestBuildReproducibilityGuardrailSilentForGateSocket drives the build path with
// a verdict-gate socket (policy) supplied only by the global config layer. A gate
// socket does not shape the rendered output, so the build-time guardrail — which
// speaks to the byte-determining sockets — must stay silent about it: a gate
// binding is meant to vary by environment and capture deliberately leaves it
// global-only by default.
func TestBuildReproducibilityGuardrailSilentForGateSocket(t *testing.T) {
	xdg := t.TempDir()
	writeFixture(t, filepath.Join(xdg, "aggrega", "config.yaml"),
		"plugins:\n  policy:\n    - uri: oci://h/pol:1\n")

	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, "kind: Release\n")

	lookup := guardrailFrozenLookup(map[string]string{"XDG_CONFIG_HOME": xdg})
	rw, err := workspaceconfig.Resolve(root, "", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var diag, out bytes.Buffer
	_ = dispatchBuild(rw, releasePath, []string{root}, false, &diag, &out)
	if strings.Contains(diag.String(), "aggrega config capture") {
		t.Errorf("guardrail warned about a global-only gate socket; diag=%q", diag.String())
	}
}
