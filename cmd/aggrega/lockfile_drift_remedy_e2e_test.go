// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// What this file holds is the command line's half of one promise: a build that
// refuses a lockfile for drift tells the reader, on the terminal, which command
// clears it, and that command does clear it.
//
// Every case runs the real command tree from inside a copy of one workspace whose
// Release reaches something on every axis a lockfile pins, and makes exactly one
// edit. The halt is read the way a terminal shows it — through the renderer the
// command's exit path uses — because the advice is worth nothing where it is carried
// on the error and not printed. The remedy is compared against LockfileDriftRemedy,
// the one place it is worded, so rewording it cannot leave this file asserting a
// sentence the product no longer prints.

// lockfileDriftAxesFixture is the shared workspace whose one Release reaches every
// axis a lockfile pins, relative to this package's directory.
const lockfileDriftAxesFixture = "../../internal/compiler/testdata/lockfile-drift-axes"

// driftAxesRelease is the fixture's Release, named as a person at the workspace root
// types it.
const driftAxesRelease = "r.yaml"

// driftAxesPlugin is the transform-role attachment the fixture configures, whose
// .wasm is laid beside its entity by driftAxesWorkspace.
const driftAxesPlugin = "governance/tag-injector"

// driftAxesWorkspace copies the fixture into a fresh workspace, lays the transform
// guest beside its Plugin entity under the name the plugin cascade reads, and
// returns the root and the guest's path there. The copy is what every command reads
// and writes, so the tracked fixture is never driven in place.
func driftAxesWorkspace(t *testing.T) (root, guestPath string) {
	t.Helper()
	src, err := filepath.Abs(lockfileDriftAxesFixture)
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm"))
	if err != nil {
		t.Fatalf("read the guest fixture: %v", err)
	}
	root = t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	guestPath = driftAxesGuestPath(root)
	writeFixtureBytes(t, guestPath, guest)
	return root, guestPath
}

// driftAxesGuestPath is where the transform guest sits in a copy of the fixture rooted
// at root.
func driftAxesGuestPath(root string) string {
	return filepath.Join(root, "catalog", "plugin", "governance", "tag-injector", "1.0.0",
		pluginpath.PluginLeaf(driftAxesPlugin))
}

// copyPulledWorkspace copies the workspace at src, lockfile and recorded pins
// included, into a fresh directory and returns the copy's root. Nothing either file
// records names the directory it was written in, so a copy starts from the same
// agreement between the lockfile and the workspace as the original.
func copyPulledWorkspace(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the pulled workspace: %v", err)
	}
	return root
}

// runDriftCommand runs one command through the real command tree with its process
// stderr captured, so the progress a build draws does not interleave with the test's
// own output, and returns the command's error.
func runDriftCommand(t *testing.T, args ...string) error {
	t.Helper()
	var err error
	captureStderr(t, func() { _, err = runAggrega(t, args...) })
	return err
}

// mustRunDriftCommand is runDriftCommand for a step the case needs to succeed, and
// fails the test with what a terminal would have shown when it does not.
func mustRunDriftCommand(t *testing.T, step string, args ...string) {
	t.Helper()
	if err := runDriftCommand(t, args...); err != nil {
		t.Fatalf("%s: aggrega %s failed:\n%s", step, strings.Join(args, " "), formatCompilerError(err))
	}
}

// appendToFixture appends text to the file at path, failing the test when the file
// is not there, so an edit aimed at a file the fixture stopped carrying cannot pass
// as a drift that was never made.
func appendToFixture(t *testing.T, path, text string) {
	t.Helper()
	body := mustReadFile(t, path)
	if err := os.WriteFile(path, append(body, []byte(text)...), 0o644); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

// renderedTreeCarries reports whether any file the build wrote under the workspace's
// output directory holds needle.
func renderedTreeCarries(t *testing.T, root, needle string) bool {
	t.Helper()
	for _, content := range renderedTree(t, filepath.Join(root, artifactout.DefaultOutputDir)) {
		if strings.Contains(content, needle) {
			return true
		}
	}
	return false
}

// TestAPlainPullClearsEveryDriftABuildReports drives, for each axis a lockfile pins,
// the sequence a person meets at a terminal: pull, build, change exactly that one
// thing, build again, run the command the refusal names, build a third time.
//
// The first pull and the build over its lockfile are the same for every row, so they
// run once, and each row starts from its own copy of the pulled workspace. The build
// runs in a copy as well: that it passes there is what shows a copy starts with its
// lockfile current, so the refusal a row meets is its own edit's.
//
// Each row asserts three things about the refused build, and each earns its place.
// The refusal carries the drift condition, so the row is about the drift comparison
// and not about some other halt the edit could provoke. It names what moved, so the
// edit drifted the axis the row is for. And what the terminal shows carries the
// remedy, so the advice is in front of the reader rather than only on the error.
// Then the remedy is run as it is worded — a plain pull, with no upgrade — and the
// third build must pass; where the edit reaches rendered output, that output must
// carry it, so the pass is the pull having recorded the change rather than the build
// having stopped comparing.
//
// The last row changes three axes at once, which is one refusal carrying every entry
// and one pull clearing all of them.
func TestAPlainPullClearsEveryDriftABuildReports(t *testing.T) {
	// Not parallel, nor are its rows: every command runs from the workspace it builds,
	// which moves the process working directory, its progress is read off the process
	// stderr, and the command tree's pre-run sets the process-wide display decision.
	pulled, _ := driftAxesWorkspace(t)
	t.Chdir(pulled)
	mustRunDriftCommand(t, "the first pull", "pull", "--release", driftAxesRelease, "--pin-sources")
	t.Chdir(copyPulledWorkspace(t, pulled))
	mustRunDriftCommand(t, "the build over a copy of the lockfile the pull wrote", "build", "--release", driftAxesRelease)

	for _, tc := range []struct {
		name string
		// edit makes the change, given the workspace root and the guest's path there.
		edit func(t *testing.T, root, guestPath string)
		// names is what the refusal must name: each drifted reference, quoted as the
		// entry quotes it.
		names []string
		// rendered, where set, is text the build after the pull must have written.
		rendered string
	}{
		{
			name: "a Component in a catalog directory",
			edit: func(t *testing.T, root, _ string) {
				appendToFixture(t, filepath.Join(root, "catalog", "component", "demo", "web", "1.0.0", "component.yaml"),
					"\n# edited after the pull\n")
			},
			names: []string{`"demo/web"`},
		},
		{
			name: "the Aggregate beside the Release",
			edit: func(t *testing.T, root, _ string) {
				appendToFixture(t, filepath.Join(root, "aggregate.yaml"), "\n# edited after the pull\n")
			},
			names: []string{`"./aggregate.yaml"`},
		},
		{
			name: "a Recipe in a catalog directory",
			edit: func(t *testing.T, root, _ string) {
				appendToFixture(t, filepath.Join(root, "catalog", "recipe", "helm", "svc", "1.0.0", "recipe.yaml"),
					"\n# edited after the pull\n")
			},
			names: []string{`"helm/svc:1.0.0"`},
		},
		{
			name: "a SchemaLibrary",
			edit: func(t *testing.T, root, _ string) {
				rewriteFixture(t, filepath.Join(root, "catalog", "libraries", "base-types.yaml"),
					"default: 8080", "default: 9090")
			},
			names: []string{`"BaseTypes"`},
		},
		{
			name: "a MappingLibrary",
			edit: func(t *testing.T, root, _ string) {
				rewriteFixture(t, filepath.Join(root, "catalog", "libraries", "base-mappings.yaml"),
					`"replicas": 2`, `"replicas": 3`)
			},
			names:    []string{`"BaseMappings"`},
			rendered: "replicas: 3",
		},
		{
			name: "a WorkloadLibrary",
			edit: func(t *testing.T, root, _ string) {
				rewriteFixture(t, filepath.Join(root, "catalog", "libraries", "base-workloads.yaml"),
					"image: svc:latest", "image: svc:2")
			},
			names:    []string{`"BaseWorkloads"`},
			rendered: "image: svc:2",
		},
		{
			name: "an ArtifactLibrary",
			edit: func(t *testing.T, root, _ string) {
				rewriteFixture(t, filepath.Join(root, "catalog", "libraries", "platform-artifacts.yaml"),
					"owner: platform", "owner: ops")
			},
			names:    []string{`"PlatformArtifacts"`},
			rendered: "owner: ops",
		},
		{
			name: "the bytes of a plugin attached at a transform role",
			edit: func(t *testing.T, _, guestPath string) {
				writeFixtureBytes(t, guestPath, appendWasmCustomSection(mustReadFile(t, guestPath)))
			},
			names: []string{`"` + driftAxesPlugin + `"`},
		},
		{
			name: "the Release file",
			edit: func(t *testing.T, root, _ string) {
				appendToFixture(t, filepath.Join(root, driftAxesRelease), "\n# edited after the pull\n")
			},
			names: []string{`"` + driftAxesRelease + `"`},
		},
		{
			name: "a Component, a SchemaLibrary and the Release file at once",
			edit: func(t *testing.T, root, _ string) {
				appendToFixture(t, filepath.Join(root, "catalog", "component", "demo", "web", "1.0.0", "component.yaml"),
					"\n# edited after the pull\n")
				rewriteFixture(t, filepath.Join(root, "catalog", "libraries", "base-types.yaml"),
					"default: 8080", "default: 9090")
				appendToFixture(t, filepath.Join(root, driftAxesRelease), "\n# edited after the pull\n")
			},
			names: []string{`"demo/web"`, `"BaseTypes"`, `"` + driftAxesRelease + `"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := copyPulledWorkspace(t, pulled)
			t.Chdir(root)

			tc.edit(t, root, driftAxesGuestPath(root))

			err := runDriftCommand(t, "build", "--release", driftAxesRelease)
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Fatalf("the build after the edit must refuse with the lockfile's drift condition; got %v", err)
			}
			shown := formatCompilerError(err)
			for _, named := range tc.names {
				if !strings.Contains(shown, named) {
					t.Errorf("the refusal does not name %s, so the edit did not drift the axis this row is for:\n%s",
						named, shown)
				}
			}
			if remedy := compilerops.LockfileDriftRemedy(driftAxesRelease); !strings.Contains(shown, remedy) {
				t.Errorf("what the terminal shows does not carry the remedy %q:\n%s", remedy, shown)
			}

			mustRunDriftCommand(t, "the remedy the refusal names", "pull", "--release", driftAxesRelease)
			mustRunDriftCommand(t, "the build after the remedy", "build", "--release", driftAxesRelease)
			if tc.rendered != "" && !renderedTreeCarries(t, root, tc.rendered) {
				t.Errorf("the build after the remedy did not write %q, so the pull did not record the edit", tc.rendered)
			}
		})
	}
}

// TestAnEditUnderALocalSourceIsClearedByThePinStepThePullNames holds the one axis a
// plain pull does not clear on its own, and what it does instead.
//
// A file under a Recipe's local source is covered by the entity's digest in the
// lockfile and by a pin in the entity.lock beside the Recipe. The build reports the
// first as drift and names the plain pull like any other row; the pull then refuses
// the second, naming the pin step, because recording pins in the author's directory
// is the author's act. Once that step has run, the plain pull clears the drift and
// the build renders the edited file. An upgrade would not have helped: it records no
// pin either, which is why the remedy stays a plain pull.
func TestAnEditUnderALocalSourceIsClearedByThePinStepThePullNames(t *testing.T) {
	root, _ := driftAxesWorkspace(t)
	t.Chdir(root)
	recipe := filepath.Join("catalog", "recipe", "helm-chart", "web", "1.0.0", "recipe.yaml")
	mustRunDriftCommand(t, "the first pull", "pull", "--release", driftAxesRelease, "--pin-sources")
	mustRunDriftCommand(t, "the build over the lockfile the pull wrote", "build", "--release", driftAxesRelease)

	rewriteFixture(t, filepath.Join(root, "catalog", "recipe", "helm-chart", "web", "1.0.0", "chart", "values.yaml"),
		"replicas: 1", "replicas: 4")

	err := runDriftCommand(t, "build", "--release", driftAxesRelease)
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("the build after the edit must refuse with the lockfile's drift condition; got %v", err)
	}
	if shown, remedy := formatCompilerError(err), compilerops.LockfileDriftRemedy(driftAxesRelease); !strings.Contains(shown, remedy) {
		t.Errorf("what the terminal shows does not carry the remedy %q:\n%s", remedy, shown)
	}

	err = runDriftCommand(t, "pull", "--release", driftAxesRelease)
	if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
		t.Fatalf("the plain pull over an edited local source must refuse with the unpinned-entity condition; got %v", err)
	}
	if shown := formatCompilerError(err); !strings.Contains(shown, "aggrega pin") {
		t.Errorf("the pull's refusal does not name the pin step:\n%s", shown)
	}

	mustRunDriftCommand(t, "the pin step the pull names", "pin", recipe)
	mustRunDriftCommand(t, "the remedy the build named", "pull", "--release", driftAxesRelease)
	mustRunDriftCommand(t, "the build after the remedy", "build", "--release", driftAxesRelease)
	if !renderedTreeCarries(t, root, "replicas: 4") {
		t.Error("the build after the remedy did not write the edited chart value")
	}
}
