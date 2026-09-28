// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool's drift round trip over every axis a lockfile pins at once. The
// case beside it drifts one entity; this one drifts one of everything the lockfile
// records — catalog Components and Recipes, the Aggregate, each library kind, the
// bytes of a transform-role plugin and the Release file — and holds that the pull
// tool, called the way the refusal says, clears all of them in one call.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// driftAxesWorkspace copies the shared fixture whose one Release reaches every axis
// a lockfile pins into a fresh workspace, lays the transform guest beside its Plugin
// entity under the name the plugin cascade reads, and returns the root, the Release
// path and the guest's path. The fixture's own configuration names its catalog
// directory and sets the offline posture, so no call below passes 'catalog_dirs':
// naming the same directory twice would put it on the search stack twice.
func driftAxesWorkspace(t *testing.T) (root, releasePath, guestPath string) {
	t.Helper()
	guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm"))
	if err != nil {
		t.Fatalf("read the guest fixture: %v", err)
	}
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "lockfile-drift-axes")
	root = t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture workspace: %v", err)
	}
	guestPath = filepath.Join(root, "catalog", "plugin", "governance", "tag-injector", "1.0.0",
		pluginpath.PluginLeaf("governance/tag-injector"))
	if err := os.WriteFile(guestPath, guest, 0o644); err != nil {
		t.Fatalf("lay the guest beside its entity: %v", err)
	}
	return root, filepath.Join(root, "r.yaml"), guestPath
}

// driftFixtureFile appends text to the fixture file at rel under root, or replaces
// the one occurrence of from with to when from is set, failing the test when the
// file does not carry exactly what the edit expects — so an edit the fixture no
// longer admits cannot pass as a drift that was never made.
func driftFixtureFile(t *testing.T, root, rel, from, to string) {
	t.Helper()
	path := filepath.Join(root, rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s to drift it: %v", rel, err)
	}
	edited := string(body) + to
	if from != "" {
		if n := strings.Count(string(body), from); n != 1 {
			t.Fatalf("%s: want exactly one %q to rewrite, found %d", rel, from, n)
		}
		edited = strings.Replace(string(body), from, to, 1)
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write the drifted %s: %v", rel, err)
	}
}

// TestBuildToolDriftOnEveryAxisIsClearedByThePullTool drifts every axis the lockfile
// pins in one workspace and asserts the whole round trip: the build refuses naming
// each drifted reference and carrying the shared remedy, the pull tool called with
// the same Release and no 'upgrade_targets' succeeds, and the build after it
// succeeds. One call clearing every axis is what makes the refusal's single
// instruction sufficient.
func TestBuildToolDriftOnEveryAxisIsClearedByThePullTool(t *testing.T) {
	t.Parallel()
	root, releasePath, guestPath := driftAxesWorkspace(t)
	s := newTestServer(t)
	s.toolchain = pullToolchainStamp

	pinned := true
	if res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, PinSources: &pinned}); res.IsError {
		t.Fatalf("the first resolution refused, so there is no current lockfile to drift: %s", res.Content[0].Text)
	}
	if res := callBuild(t, s, root, nil, releasePath); res.IsError {
		t.Fatalf("the build over the lockfile the first resolution wrote refused: %s", res.Content[0].Text)
	}

	const edited = "\n# edited after the lockfile was written\n"
	driftFixtureFile(t, root, "catalog/component/demo/web/1.0.0/component.yaml", "", edited)
	driftFixtureFile(t, root, "aggregate.yaml", "", edited)
	driftFixtureFile(t, root, "catalog/recipe/helm/svc/1.0.0/recipe.yaml", "", edited)
	driftFixtureFile(t, root, "catalog/libraries/base-types.yaml", "default: 8080", "default: 9090")
	driftFixtureFile(t, root, "catalog/libraries/base-mappings.yaml", `"replicas": 2`, `"replicas": 3`)
	driftFixtureFile(t, root, "catalog/libraries/base-workloads.yaml", "image: svc:latest", "image: svc:2")
	driftFixtureFile(t, root, "catalog/libraries/platform-artifacts.yaml", "owner: platform", "owner: ops")
	driftFixtureFile(t, root, "r.yaml", "", edited)
	guest, err := os.ReadFile(guestPath)
	if err != nil {
		t.Fatalf("read the guest to drift it: %v", err)
	}
	// A custom section, which a module may carry anywhere and which changes nothing
	// the module does: id 0, size 3, a one-byte name and a one-byte payload.
	if err := os.WriteFile(guestPath, append(guest, 0x00, 0x03, 0x01, 'x', 'y'), 0o644); err != nil {
		t.Fatalf("write the drifted guest: %v", err)
	}

	refused := callBuild(t, s, root, nil, releasePath)
	if !refused.IsError {
		t.Fatalf("the build over a lockfile drifted on every axis did not refuse: %s", refused.Content[0].Text)
	}
	text := refused.Content[0].Text
	for _, named := range []string{`"demo/web"`, `"./aggregate.yaml"`, `"helm/svc:1.0.0"`, `"BaseTypes"`,
		`"BaseMappings"`, `"BaseWorkloads"`, `"PlatformArtifacts"`, `"governance/tag-injector"`, `"r.yaml"`} {
		if !strings.Contains(text, named) {
			t.Errorf("the refusal does not name %s, so that axis did not drift and this case covers less than it says:\n%s",
				named, text)
		}
	}
	if remedy := compilerops.LockfileDriftRemedy(releasePath); !strings.Contains(text, remedy) {
		t.Errorf("the refusal does not carry the shared remedy %q:\n%s", remedy, text)
	}

	if res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root}); res.IsError {
		t.Fatalf("the pull tool, called as the refusal says, refused: %s", res.Content[0].Text)
	}
	built := callBuild(t, s, root, nil, releasePath)
	if built.IsError {
		t.Fatalf("following the remedy the refusal names did not clear every axis: %s", built.Content[0].Text)
	}
	if len(unmarshalBuildPayload(t, built).Files) == 0 {
		t.Fatal("the build after the remedy returned no rendered files")
	}
}
