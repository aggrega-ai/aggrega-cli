// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Scope symmetry for a refused catalog entity. A Component that violates a NAMED
// envelope rule is refused at load; that refusal makes the entity unresolvable,
// and every scope that reaches the entity must say which file was refused and
// which rule refused it — not merely that some reference did not resolve.
//
// The entity and whole-catalog scopes report the refusal directly. The two
// release-scoped verbs report it as a resolution failure, so each replays the
// recorded refusal alongside the failure. The test asserts on all four so the
// symmetry is pinned rather than asserted for one scope at a time.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// scopeRefusedComponent shadows the fixture catalog's api-server with one that
// declares extends and then authors pillar content — refused by the rule naming
// the inheritance marker an extending Component must declare instead.
const scopeRefusedComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: core/compute/api-base:1.0.0
  inject: ["env"]
  schema:
    provides:
      host: { type: string, required: true }
      service_port: { type: string, required: true }
`

// The rule the envelope names when it refuses the document. It is the one token
// in the diagnostic that tells the author what to change.
const scopeRefusedRule = "extending-component-must-declare-schema-inherited"

// scopeTriad lays down the aggregate/manifest/release triad over the fixture
// catalog and returns the catalog search stack. overlayDir is empty until the
// caller breaks the Component into it.
//
// The stack leads with a seeded provider overlay: the shared fixture catalog
// declares the core providers under its own version, while capability resolution
// selects the version the binary embeds, so without the overlay the lockfile walk
// would find no local entity at the selected version and reach for a registry.
func scopeTriad(t *testing.T) (root string, cleanDirs []string) {
	t.Helper()
	root = t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), cliBackendAggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), cliCleanManifest)
	writeFixture(t, filepath.Join(root, "release.yaml"), cliDefaultRelease)
	writeWorkspaceConfig(t, root, "")
	providers := seedEmbeddedProviderOverlay(t, "provider/helm")
	return root, []string{providers, cliFullstackCatalogDir(t)}
}

// breakComponent writes the refused Component into a higher-precedence overlay
// directory and returns the overlay-first search stack plus the file's path.
func breakComponent(t *testing.T, root string, cleanDirs []string) (dirs []string, compPath string) {
	t.Helper()
	overlay := filepath.Join(root, "overlay")
	compPath = filepath.Join(overlay, "components", "api-server.yaml")
	writeFixture(t, compPath, scopeRefusedComponent)
	return append([]string{overlay}, cleanDirs...), compPath
}

// requireNamesFileAndRule fails unless the reported text carries both the
// refused file's path and the rule that refused it.
func requireNamesFileAndRule(t *testing.T, scope, reported, compPath string) {
	t.Helper()
	if reported == "" {
		t.Fatalf("%s reported nothing", scope)
	}
	if !strings.Contains(reported, compPath) {
		t.Errorf("%s does not name the refused file %s:\n%s", scope, compPath, reported)
	}
	if !strings.Contains(reported, scopeRefusedRule) {
		t.Errorf("%s does not name the rule %q:\n%s", scope, scopeRefusedRule, reported)
	}
}

// TestRefusedEntityNamedInEntityAndCatalogScopes covers the two scopes that read
// the entity directly.
func TestRefusedEntityNamedInEntityAndCatalogScopes(t *testing.T) {
	root, cleanDirs := scopeTriad(t)
	dirs, compPath := breakComponent(t, root, cleanDirs)
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	t.Run("entity", func(t *testing.T) {
		var out bytes.Buffer
		err := dispatchValidate(context.Background(), rw, compPath, dirs, false, &out)
		if err == nil {
			t.Fatal("the entity scope must refuse the document")
		}
		requireNamesFileAndRule(t, "validate <entity>", formatCompilerError(err), compPath)
	})

	t.Run("whole-catalog", func(t *testing.T) {
		var diag, out bytes.Buffer
		err := validateCatalogWide(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), dirs, &diag, &out)
		if err == nil {
			t.Fatal("the whole-catalog scope must refuse the document")
		}
		requireNamesFileAndRule(t, "validate (whole catalog)", formatCompilerError(err)+diag.String(), compPath)
	})
}

// TestRefusedEntityNamedInReleaseScopes is this file's reason for existing: the
// two release-scoped verbs an author actually runs. Both report the refusal as an
// unresolvable entity, and both must carry the refused file and its rule with
// that report. Leg 1 establishes that the workspace otherwise builds and
// validates clean, so every diagnostic in leg 2 is attributable to the refusal.
func TestRefusedEntityNamedInReleaseScopes(t *testing.T) {
	root, cleanDirs := scopeTriad(t)
	t.Chdir(root)
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	releasePath := filepath.Join(root, "release.yaml")

	// Leg 1 — the clean workspace builds, which also lays down the lockfile the
	// build gate requires. An author reaches the release scopes with a lockfile
	// already present; breaking a Component afterwards is the shape under test.
	var diag, out bytes.Buffer
	if err := dispatchBuildWithDiag(context.Background(), rw, releasePath, cleanDirs, false,
		buildDiagOptions{}, buildops.RunOptions{AutoPull: true}, &diag, &out); err != nil {
		t.Fatalf("the clean workspace must build: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, "release.lock")); serr != nil {
		t.Fatalf("the clean build must leave a lockfile: %v", serr)
	}
	if err := validateRelease(context.Background(), rw, releasePath, cleanDirs, compilerops.VerbosityDetailed, false, &diag, &out); err != nil {
		t.Fatalf("the clean workspace must validate: %v", err)
	}

	// Leg 2 — break the Component and re-run both release scopes.
	dirs, compPath := breakComponent(t, root, cleanDirs)

	t.Run("build --release", func(t *testing.T) {
		var diag, out bytes.Buffer
		err := dispatchBuildWithDiag(context.Background(), rw, releasePath, dirs, false,
			buildDiagOptions{}, buildops.RunOptions{}, &diag, &out)
		if err == nil {
			t.Fatal("the build must halt on a refused in-closure entity")
		}
		requireNamesFileAndRule(t, "build --release", formatCompilerError(err), compPath)
	})

	t.Run("validate --release", func(t *testing.T) {
		var diag, out bytes.Buffer
		err := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityDetailed, false, &diag, &out)
		if err == nil {
			t.Fatal("the release scope must halt on a refused in-closure entity")
		}
		requireNamesFileAndRule(t, "validate --release", formatCompilerError(err)+diag.String(), compPath)
	})
}
