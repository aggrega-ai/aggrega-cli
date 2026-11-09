// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noWorkspaceNoticeOpening is how the command line's notice opens, for counting
// it in a captured error stream. The sentence's home is noWorkspaceNotice; this
// is the part of it every row reads, so a reworded tail moves no row.
const noWorkspaceNoticeOpening = "note: no Aggrega workspace was found from "

// markedWorkspace returns a fresh folder carrying a workspace configuration, so
// it anchors itself.
func markedWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	return root
}

// namesFolder reports whether text names folder, as given or with its links
// resolved: the folder a command reports is the working directory, which the
// operating system may hand back resolved where a temporary directory is spelled
// through a link.
func namesFolder(t *testing.T, text, folder string) bool {
	t.Helper()
	if strings.Contains(text, folder) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(folder)
	if err != nil {
		t.Fatalf("resolve %s: %v", folder, err)
	}
	return strings.Contains(text, resolved)
}

// TestACommandSaysOnceWhichFolderItUsedWhereNoWorkspaceWasFound runs commands
// through the real command tree from a folder no workspace resolves from, and
// counts the notice on the process's error stream: once, naming that folder, for
// each command that resolves a workspace there; never where something named a
// root, where the folder is a workspace, or for a command that does not resolve
// one. login resolves one for its offline posture alone and is not told.
//
// 🔴 PLANTED DEFECTS: have FoundNoWorkspace also answer true for MarkerNone — a
// named root counted as nothing found — and the --workspace-root and the variable
// rows fail; drop the noWorkspaceNotice call from the lazy resolver and the three
// "says it" rows fail; drop login's withholdNoWorkspaceNotice and its row fails.
//
// Not parallel: every row moves the process working directory and sets the
// process environment, and the notice is written to the process's error stream.
func TestACommandSaysOnceWhichFolderItUsedWhereNoWorkspaceWasFound(t *testing.T) {
	for _, tc := range []struct {
		name string
		// args builds the invocation for a row run in folder; it may set the
		// environment the row names.
		args func(t *testing.T, folder string) []string
		// inWorkspace runs the row inside a marked workspace rather than folder.
		inWorkspace bool
		says        bool
	}{
		{name: "config show says it", says: true,
			args: func(*testing.T, string) []string { return []string{"config", "show"} }},
		{name: "validate says it", says: true,
			args: func(*testing.T, string) []string { return []string{"validate"} }},
		{name: "pull says it", says: true,
			args: func(t *testing.T, folder string) []string {
				release := filepath.Join(folder, "production.yaml")
				writeFixture(t, release, "kind: Release\n")
				return []string{"pull", "--release", release}
			}},
		{name: "--workspace-root names a root",
			args: func(t *testing.T, _ string) []string {
				return []string{"--workspace-root", markedWorkspace(t), "config", "show"}
			}},
		{name: "AGGREGA_WORKSPACE_ROOT names a root",
			args: func(t *testing.T, _ string) []string {
				t.Setenv(workspaceconfig.EnvWorkspaceRoot, markedWorkspace(t))
				return []string{"config", "show"}
			}},
		{name: "the folder is a workspace", inWorkspace: true,
			args: func(*testing.T, string) []string { return []string{"config", "show"} }},
		{name: "version resolves no workspace",
			args: func(*testing.T, string) []string { return []string{"version"} }},
		// The workspace is read before the command's own refusal of a password on
		// standard input with no username, so the row reaches the resolution and
		// reads no input.
		{name: "login reads the workspace for its offline posture alone",
			args: func(*testing.T, string) []string {
				return []string{"login", "registry.example.test", "--password-stdin"}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hometest.Isolate(t)
			t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
			t.Setenv("AGGREGA_OFFLINE", "true")
			folder := folderNoWorkspaceResolvesFrom(t)
			if tc.inWorkspace {
				folder = markedWorkspace(t)
			}
			args := tc.args(t, folder)
			t.Chdir(folder)

			var out string
			stderr := captureStderr(t, func() { out, _ = runAggrega(t, args...) })
			count := strings.Count(stderr, noWorkspaceNoticeOpening)
			switch {
			case tc.says && count != 1:
				t.Errorf("aggrega %s wrote the notice %d times, want once\nstderr:\n%s\noutput:\n%s",
					strings.Join(args, " "), count, stderr, out)
			case tc.says && !namesFolder(t, stderr, folder):
				t.Errorf("aggrega %s wrote a notice that does not name the folder it ran in, %s:\n%s",
					strings.Join(args, " "), folder, stderr)
			case !tc.says && count != 0:
				t.Errorf("aggrega %s wrote the notice although %s:\n%s", strings.Join(args, " "), tc.name, stderr)
			}
		})
	}
}

// TestTheNoticeIsWrittenOnceAndWithheldWhereTheCommandMarksIt drives the
// resolution chokepoint itself, from a folder no workspace resolves from: three
// reads of the workspace write the notice once, and a resolver marked to withhold
// it writes nothing.
//
// Not parallel: the resolver resolves from the process working directory.
func TestTheNoticeIsWrittenOnceAndWithheldWhereTheCommandMarksIt(t *testing.T) {
	folder := folderNoWorkspaceResolvesFrom(t)
	t.Chdir(folder)

	var once bytes.Buffer
	l := &lazyWorkspace{lookup: firstRunLookup(nil), errOut: &once}
	for i := 0; i < 3; i++ {
		if _, err := l.get(); err != nil {
			t.Fatalf("get (read %d): %v", i+1, err)
		}
	}
	if n := strings.Count(once.String(), noWorkspaceNoticeOpening); n != 1 {
		t.Errorf("three reads wrote the notice %d times, want once: %q", n, once.String())
	}

	ctx := withWorkspaceResolver(context.Background(), "", firstRunLookup(nil))
	withholdNoWorkspaceNotice(ctx)
	marked, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace)
	if !ok {
		t.Fatal("withWorkspaceResolver attached no resolver")
	}
	var withheld bytes.Buffer
	marked.errOut = &withheld
	if _, err := marked.get(); err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(withheld.String(), noWorkspaceNoticeOpening) {
		t.Errorf("a resolver marked to withhold the notice wrote it: %q", withheld.String())
	}
}

// TestARootThatIsNotAnExistingFolderIsRefusedNamingWhereItCameFrom runs the
// real command tree with a workspace root that is not there, given by the flag
// and by the variable, to commands that resolve a workspace, and reads the
// refusal: it names whichever supplied the value — the variable with the desktop
// setting that sets it — and quotes the value as given, and carries the reason
// the folder could not be used. A command that resolves no workspace is not
// refused for a root it never reads.
//
// 🔴 PLANTED DEFECT: restore the explicit branch of the shared resolution to take
// the root as given, and the two refusing rows fail — config show answers on the
// built-in defaults and validate answers about catalog directories.
//
// Not parallel: every row sets the process environment and the working directory.
func TestARootThatIsNotAnExistingFolderIsRefusedNamingWhereItCameFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		// run returns the arguments and sets the environment for missing.
		run     func(t *testing.T, missing string) []string
		source  string
		refused bool
	}{
		{name: "config show --workspace-root", source: "--" + workspaceconfig.WorkspaceRootFlagName, refused: true,
			run: func(_ *testing.T, missing string) []string {
				return []string{"--workspace-root", missing, "config", "show"}
			}},
		{name: "validate under AGGREGA_WORKSPACE_ROOT", source: workspaceconfig.EnvWorkspaceRootSource, refused: true,
			run: func(t *testing.T, missing string) []string {
				t.Setenv(workspaceconfig.EnvWorkspaceRoot, missing)
				return []string{"validate"}
			}},
		{name: "version --workspace-root",
			run: func(_ *testing.T, missing string) []string {
				return []string{"--workspace-root", missing, "version"}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hometest.Isolate(t)
			t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
			t.Setenv("AGGREGA_OFFLINE", "true")
			t.Chdir(folderNoWorkspaceResolvesFrom(t))
			missing := filepath.Join(t.TempDir(), "no-such-folder")
			args := tc.run(t, missing)

			var err error
			captureStderr(t, func() { _, err = runAggrega(t, args...) })
			if !tc.refused {
				if err != nil {
					t.Errorf("aggrega %s was refused for a root it never reads: %v", strings.Join(args, " "), err)
				}
				return
			}
			if err == nil {
				t.Fatalf("aggrega %s answered over a workspace root that is not there", strings.Join(args, " "))
			}
			if want := tc.source + ` names the workspace root "` + missing + `"`; !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal reads %q; want it to carry %q", err, want)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the refusal %q does not carry the reason the folder could not be used", err)
			}
		})
	}
}

// TestTheCommandLineAndTheAssistantServerNameTheSameFolder runs the built binary
// in one folder no workspace resolves from: as a command, and as the assistant
// server called over its own streams with no launch root — once through a tool of
// its own and once through the tool generated from the same command. The command
// writes the notice on its error stream; the server's own tool says it in its
// answer; the generated tool's answer carries the command's own line as the
// command printed it. All three name that one folder, because all three ask the
// one predicate of the same resolution.
//
// 🔴 PLANTED DEFECTS: drop the withNoWorkspaceNotice call at the server's one
// exit and the server's own tool row fails; drop the noWorkspaceNotice call from
// the lazy resolver and the command and generated-tool rows fail.
func TestTheCommandLineAndTheAssistantServerNameTheSameFolder(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	folder := folderNoWorkspaceResolvesFrom(t)

	cmd := exec.Command(bin, "config", "show")
	cmd.Dir = folder
	cmd.Env = builtBinaryEnv(t, "AGGREGA_OFFLINE=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("aggrega config show in %s: %v\nstderr:\n%s", folder, err, stderr.String())
	}
	var line string
	for _, l := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(l, noWorkspaceNoticeOpening) {
			line = l
		}
	}
	if line == "" || !namesFolder(t, line, folder) {
		t.Fatalf("the command wrote no notice naming %s:\n%s", folder, stderr.String())
	}

	own, isError := callToolOverStdio(t, bin, folder, "list-releases", map[string]any{})
	if isError {
		t.Fatalf("list-releases was refused:\n%s", own)
	}
	if n := strings.Count(own, "No Aggrega workspace was found from "); n != 1 || !namesFolder(t, own, folder) {
		t.Errorf("the server's own tool says the notice %d times, or not of %s:\n%s", n, folder, own)
	}

	generated, isError := callToolOverStdio(t, bin, folder, "cli-config-show", map[string]any{})
	if isError {
		t.Fatalf("cli-config-show was refused:\n%s", generated)
	}
	if !strings.Contains(generated, line) {
		t.Errorf("the generated tool's answer does not carry the command's own line %q:\n%s", line, generated)
	}
}

// looseRegistryRelease lays down, in folder, a Release whose Components and
// Features only a registry serves — the fixture the registry-reference tests pull
// — under file names no root marker matches, so the folder still anchors nothing,
// with the Recipes it binds in the conventional catalog directory beside it. It
// returns the Release's path.
func looseRegistryRelease(t *testing.T, folder string) string {
	t.Helper()
	for rel, body := range registryReferenceRealizations {
		writeFixture(t, filepath.Join(folder, "catalog", filepath.FromSlash(rel)), body)
	}
	staging := t.TempDir()
	registryReferenceRelease(t, staging, registryReferenceForms{})
	for from, to := range map[string]string{
		"aggregate.yaml": "callers-aggregate.yaml",
		"manifest.yaml":  "callers-manifest.yaml",
		"release.yaml":   "callers.yaml",
	} {
		body, err := os.ReadFile(filepath.Join(staging, from))
		if err != nil {
			t.Fatalf("read the fixture's %s: %v", from, err)
		}
		writeFixture(t, filepath.Join(folder, to), string(body))
	}
	return filepath.Join(folder, "callers.yaml")
}

// assertNoCacheAndStillNoWorkspace fails where folder holds an artifact cache, or
// where a resolution started beneath it now anchors at it: a cache directory is a
// workspace marker, so a write there would have made the folder a workspace.
func assertNoCacheAndStillNoWorkspace(t *testing.T, folder string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(folder, ".aggrega")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s/.aggrega exists after the run (stat: %v)", folder, err)
	}
	sub := filepath.Join(folder, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("create %s: %v", sub, err)
	}
	rw, err := workspaceconfig.Resolve(sub, "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve from %s: %v", sub, err)
	}
	if !rw.FoundNoWorkspace() {
		t.Errorf("a resolution from %s anchors at %s (marker %v) after the run, where before it anchored nothing",
			sub, rw.Root(), rw.Marker())
	}
}

// TestAPullThatWouldFillTheCacheIsRefusedInAFolderNoWorkspaceResolvesFrom runs a
// pull that completes against the registry double the registry-reference tests
// use, once in a folder nobody marked and nothing named and once in the same
// layout marked as a workspace.
//
// In the marked folder it completes and caches what it fetched under the folder,
// which is what makes the other row's absence of a cache the refusal's doing. In
// the unmarked folder it is refused before anything is fetched — the registry is
// asked for nothing — with the refusal naming the folder, no lockfile is written,
// no .aggrega appears, and a resolution started beneath the folder still anchors
// nothing.
//
// 🔴 PLANTED DEFECT: remove the CacheWriteRefusal call from pullops.Run, and the
// unmarked row fails — the pull completes, fetches from the registry, and creates
// the folder's .aggrega directory.
func TestAPullThatWouldFillTheCacheIsRefusedInAFolderNoWorkspaceResolvesFrom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		marked  bool
		refused bool
	}{
		{name: "a folder nobody marked and nothing named", refused: true},
		{name: "the same layout marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			folder := folderNoWorkspaceResolvesFrom(t)
			release := looseRegistryRelease(t, folder)
			config := "default_registry: hub.aggrega.ai\n" + fakeRegistryTrust
			if tc.marked {
				config = "workspace: {}\n" + config
			}
			writeWorkspaceConfig(t, folder, config)
			rw, err := workspaceconfig.Resolve(folder, "", func(string) string { return "" })
			if err != nil {
				t.Fatalf("resolve %s: %v", folder, err)
			}
			if rw.FoundNoWorkspace() == tc.marked {
				t.Fatalf("precondition: the folder resolves as found-nothing=%v, the row needs %v", rw.FoundNoWorkspace(), !tc.marked)
			}
			registry := &inProcessEntityRegistry{fetched: map[string]int{}}
			var diag bytes.Buffer
			err = pullRun(context.Background(), pullConfig{
				ws:          rw,
				ReleasePath: release,
				DiagOut:     &diag,
				Out:         &bytes.Buffer{},
				diag:        buildDiagOptions{quiet: true},
				puller:      registry,
			})

			if !tc.refused {
				if err != nil {
					t.Fatalf("the pull in a workspace failed, so the refused row proves nothing: %v\n%s", err, diag.String())
				}
				if _, serr := os.Stat(filepath.Join(folder, ".aggrega")); serr != nil {
					t.Fatalf("the pull in a workspace left no cache under it (%v), so the refused row's missing cache proves nothing", serr)
				}
				return
			}
			var refused *workspaceconfig.CacheWriteRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("the pull returned %v, want the refusal of a cache written in a folder that is no workspace\n%s", err, diag.String())
			}
			if refused.Root() != folder {
				t.Errorf("the refusal names %s, want %s", refused.Root(), folder)
			}
			if fetched := registry.fetches(); len(fetched) != 0 {
				t.Errorf("the refused pull fetched %v from the registry", fetched)
			}
			assertNoLockfile(t, release, "the refused pull")
			assertNoCacheAndStillNoWorkspace(t, folder)
		})
	}
}

// TestARegistryUpdateIsRefusedInAFolderNoWorkspaceResolvesFrom runs the index
// update against the in-memory index source the update tests use, once in a
// folder nobody marked and nothing named and once in the same folder marked as a
// workspace. Marked, it writes the index under the folder; unmarked, it is
// refused before any registry is walked, naming the folder, and leaves no cache.
//
// 🔴 PLANTED DEFECT: remove the CacheWriteRefusal call from registryops.Update,
// and the unmarked row fails — the walk writes the folder's index and creates its
// .aggrega directory.
//
// Not parallel: it points the process-global index-target seam at its source.
func TestARegistryUpdateIsRefusedInAFolderNoWorkspaceResolvesFrom(t *testing.T) {
	src := fakeIndexSource{
		repos: []string{"component/core/api-server"},
		targets: map[string]oras.ReadOnlyTarget{
			"component/core/api-server": seedComponentRepo(t, []string{"1.0.0"}, oci.SearchableMetadata{
				Description: "Stateless HTTP API server", Category: "compute", Kind: "Component",
				Labels: componentFixtureLabels(),
			}),
		},
	}
	withIndexTarget(t, src)
	frozen := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

	for _, tc := range []struct {
		name    string
		marked  bool
		refused bool
	}{
		{name: "a folder nobody marked and nothing named", refused: true},
		{name: "the same folder marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			folder := folderNoWorkspaceResolvesFrom(t)
			config := "default_registry: hub.aggrega.ai\nregistries:\n  - host: hub.aggrega.ai\n    name: hub\n"
			if tc.marked {
				config = "workspace: {}\n" + config
			}
			writeWorkspaceConfig(t, folder, config)
			rw, err := workspaceconfig.Resolve(folder, "", func(string) string { return "" })
			if err != nil {
				t.Fatalf("resolve %s: %v", folder, err)
			}
			err = registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &bytes.Buffer{}, now: frozen})
			if !tc.refused {
				if err != nil {
					t.Fatalf("the update in a workspace failed, so the refused row proves nothing: %v", err)
				}
				if _, serr := os.Stat(oci.IndexRoot(folder, rw.Config().ResolvedArtifactCacheDir())); serr != nil {
					t.Fatalf("the update in a workspace wrote no index under it (%v), so the refused row's missing cache proves nothing", serr)
				}
				return
			}
			var refused *workspaceconfig.CacheWriteRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("the update returned %v, want the refusal of a cache written in a folder that is no workspace", err)
			}
			if refused.Root() != folder {
				t.Errorf("the refusal names %s, want %s", refused.Root(), folder)
			}
			assertNoCacheAndStillNoWorkspace(t, folder)
		})
	}
}

// localCatalogNoteRelease lays down, in a marked workspace that is offline, a
// Release whose Manifest binds a Recipe no tier holds, so a pull of it fails on a
// reference that resolved to nothing — the failure the workspace's account of
// where it reads local entities explains. It returns the workspace and the
// Release.
func localCatalogNoteRelease(t *testing.T) (root, release string) {
	t.Helper()
	root = t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\noffline: true\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    web:
      type: example/compute/missing:1.0.0
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/missing:1.0.0: compose/missing:1.0.0
`)
	release = filepath.Join(root, "release.yaml")
	writeFixture(t, release, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: app-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: app
  manifest: app-manifest
  boundaries:
    app:
      directory: app
      components:
        web: {}
`)
	return root, release
}

// TestAPullRefusalCarriesTheLocalCatalogNoteTheResolutionComposes is the
// command line's row of the note's parity: the pull fails on a reference that
// resolved to nothing, and its refusal carries the workspace's own account of
// where local entities are read from, as the resolution composes it, with no hook
// of this command's supplying it. The assistant's tool and the language server
// assert the same note against the same oracle in their own packages.
func TestAPullRefusalCarriesTheLocalCatalogNoteTheResolutionComposes(t *testing.T) {
	t.Parallel()
	root, release := localCatalogNoteRelease(t)
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	note := strings.TrimSpace(rw.LocalCatalogAdviceNote(release))
	if note == "" {
		t.Fatal("precondition: the workspace composes no note for this Release, so the row would compare nothing")
	}
	err := pullRun(context.Background(), pullConfig{
		ws: rw, ReleasePath: release, DiagOut: &bytes.Buffer{}, Out: &bytes.Buffer{}, diag: buildDiagOptions{quiet: true},
	})
	if err == nil {
		t.Fatal("the pull of a Release binding a Recipe no tier holds completed")
	}
	if !strings.Contains(err.Error(), note) {
		t.Errorf("the refusal does not carry the workspace's note\nrefusal: %s\nnote:    %s", err, note)
	}
}
