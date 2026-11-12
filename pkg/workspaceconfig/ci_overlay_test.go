// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestCIOverlayAddsToBaseAndDoesNotRemove: a base `.aggrega-config.yaml`
// declaring a governance attachment plus a CI overlay declaring ANOTHER governance
// attachment resolve to BOTH — base first, CI appended. The overlay adds; it
// never removes what the base declares.
func TestCIOverlayAddsToBaseAndDoesNotRemove(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  governance:\n    - uri: oci://h/base:1\n      role: label-transform\n")
	write(t, filepath.Join(root, ".aggrega-config-append-ci.yaml"),
		"plugins:\n  governance:\n    - uri: oci://h/ci:1\n      role: label-transform\n")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := rw.Config().Plugins.Governance
	if len(got) != 2 {
		t.Fatalf("Governance: got %d entries %v; want 2 (base + CI overlay)", len(got), got)
	}
	if got[0].URI != "oci://h/base:1" {
		t.Errorf("Governance[0]: got %q; want oci://h/base:1 (base is not removed and stays first)", got[0].URI)
	}
	if got[1].URI != "oci://h/ci:1" {
		t.Errorf("Governance[1]: got %q; want oci://h/ci:1 (CI overlay appended)", got[1].URI)
	}
}

// TestCIOverlayFoldsAppendCompanion: a CI overlay may write either the base
// `governance` list or its `governance_append` companion; both fold additively onto the
// resolved list. Exercises the fold-both-forms contract.
func TestCIOverlayFoldsAppendCompanion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  governance:\n    - uri: oci://h/base:1\n      role: label-transform\n")
	write(t, filepath.Join(root, ".aggrega-config-append-ci.yaml"),
		"plugins:\n  governance:\n    - uri: oci://h/ci-base:1\n      role: label-transform\n  governance_append:\n    - uri: oci://h/ci-append:1\n      role: label-transform\n")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var uris []string
	for _, p := range rw.Config().Plugins.Governance {
		uris = append(uris, p.URI)
	}
	want := []string{"oci://h/base:1", "oci://h/ci-base:1", "oci://h/ci-append:1"}
	if !reflect.DeepEqual(uris, want) {
		t.Errorf("Governance URIs: got %v; want %v (base, then CI base list, then CI append companion)", uris, want)
	}
}

// TestCIOverlayResolvesPathsAgainstOwnDir: a CI overlay's governance `uri` and
// `with.sources` resolve declaring-file-relative — against the CI file's OWN
// directory, which here is a child that carries no base config at all.
func TestCIOverlayResolvesPathsAgainstOwnDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")
	child := filepath.Join(root, "svc")
	write(t, filepath.Join(child, ".aggrega-config-append-ci.yaml"),
		"plugins:\n  governance:\n    - uri: ./ci.wasm\n      role: label-transform\n      with:\n        sources:\n          - ./ci-src.def\n")

	rw, err := workspaceconfig.Resolve(child, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Root() != root {
		t.Fatalf("Root: got %q; want %q (base workspace: anchors; CI child does not)", rw.Root(), root)
	}
	pol := rw.Config().Plugins.Governance
	if len(pol) != 1 {
		t.Fatalf("Governance: got %d %v; want 1 (CI overlay in child contributes)", len(pol), pol)
	}
	if got, want := pol[0].URI, filepath.Join(child, "ci.wasm"); got != want {
		t.Errorf("Governance URI: got %q; want %q (resolved against CI file's own dir)", got, want)
	}
	if pol[0].With == nil || len(pol[0].With.Sources) != 1 {
		t.Fatalf("Governance sources: got %+v; want 1 entry", pol[0].With)
	}
	if got, want := pol[0].With.Sources[0], filepath.Join(child, "ci-src.def"); got != want {
		t.Errorf("Governance source: got %q; want %q (resolved against CI file's own dir)", got, want)
	}
}

// TestCIOverlayIgnoresScalarWithWarning: a CI overlay that sets a scalar
// (default_registry) leaves the base scalar UNCHANGED and emits a warning
// naming the ignored key — the overlay is additive-only, never a scalar
// override, and it says so loudly.
func TestCIOverlayIgnoresScalarWithWarning(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\ndefault_registry: base.io\n")
	write(t, filepath.Join(root, ".aggrega-config-append-ci.yaml"),
		"default_registry: ci.io\n")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "base.io" {
		t.Errorf("DefaultRegistry: got %q; want base.io (CI overlay cannot scalar-override the base)", got)
	}
	warnings := rw.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("Warnings: got %d %v; want exactly 1 naming the ignored key", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "default_registry") {
		t.Errorf("Warning %q does not name the ignored key default_registry", warnings[0])
	}
}

// TestCIOverlayDoesNotAnchorRoot: a directory containing ONLY the CI overlay
// (no base `.aggrega-config.yaml` with a `workspace:` block) does NOT anchor a
// workspace root — root discovery reads only the base filename — yet the
// overlay still contributes its additive attachments.
func TestCIOverlayDoesNotAnchorRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config-append-ci.yaml"),
		"plugins:\n  governance:\n    - uri: oci://h/ci:1\n      role: label-transform\n")

	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rw.Marker() != workspaceconfig.MarkerCWD {
		t.Errorf("Marker: got %q; want CWD (a CI overlay alone must not anchor a root)", rw.Marker())
	}
	if rw.Root() != root {
		t.Errorf("Root: got %q; want %q (CWD fallback)", rw.Root(), root)
	}
	// The overlay still contributes its additive attachment.
	if pol := rw.Config().Plugins.Governance; len(pol) != 1 || pol[0].URI != "oci://h/ci:1" {
		t.Errorf("Governance: got %v; want [oci://h/ci:1] (CI-only dir contributes attachments)", pol)
	}
}

// TestCIOverlayDeterministic: resolving identical input twice yields an
// identical merged Config and identical warnings (deterministic output).
func TestCIOverlayDeterministic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\ndefault_registry: base.io\nplugins:\n  governance:\n    - uri: oci://h/base:1\n      role: label-transform\n")
	write(t, filepath.Join(root, ".aggrega-config-append-ci.yaml"),
		"default_registry: ci.io\nplugins:\n  governance:\n    - uri: oci://h/ci:1\n      role: label-transform\n")

	rw1, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve #1: %v", err)
	}
	rw2, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve #2: %v", err)
	}
	if !reflect.DeepEqual(rw1.Config(), rw2.Config()) {
		t.Errorf("merged Config differs across identical resolutions:\n #1: %+v\n #2: %+v", rw1.Config(), rw2.Config())
	}
	if !reflect.DeepEqual(rw1.Warnings(), rw2.Warnings()) {
		t.Errorf("warnings differ across identical resolutions:\n #1: %v\n #2: %v", rw1.Warnings(), rw2.Warnings())
	}
}
