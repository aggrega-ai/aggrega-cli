// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestFoundNoWorkspaceIsTheResolutionThatFellBackWithNothingNamed walks every way
// a resolution settles its root and asks the one predicate of each: it holds for
// the resolution that anchored nothing and was handed no root, and for that one
// alone. The folder of a loose Release — one whose own folder anchors nothing —
// is the start the shared rule re-resolves from for a run about that Release, so
// it is among the starts.
//
// The rows reach the predicate's two halves separately. A named root that anchors
// nothing is the row a predicate counting a named root as nothing found would
// fail; a marked folder is the row a predicate ignoring the marker would fail.
//
// 🔴 PLANTED DEFECTS: have FoundNoWorkspace also answer true for MarkerNone — a
// named root counted as nothing found — and the "a named root" row fails; have it
// answer true whatever the marker, and every marked row fails.
func TestFoundNoWorkspaceIsTheResolutionThatFellBackWithNothingNamed(t *testing.T) {
	t.Parallel()
	empty := noWorkspaceFolder(t)
	named := noWorkspaceFolder(t)

	configured := t.TempDir()
	write(t, filepath.Join(configured, workspaceconfig.ConfigFilename), "workspace: {}\n")
	dotted := t.TempDir()
	write(t, filepath.Join(dotted, ".aggrega", "keep"), "")

	loose := noWorkspaceFolder(t)
	write(t, filepath.Join(loose, "production.yaml"), "kind: Release\n")
	write(t, filepath.Join(configured, "releases", "release.yaml"), "kind: Release\n")

	lookup := func(string) string { return "" }
	for _, tc := range []struct {
		name            string
		start, flagRoot string
		want            bool
	}{
		{name: "nothing anchored and nothing named", start: empty, want: true},
		{name: "a named root", start: empty, flagRoot: named},
		{name: "a workspace configuration anchors the root", start: configured},
		{name: "an .aggrega directory anchors the root", start: dotted},
		{name: "a folder beneath a workspace", start: filepath.Join(configured, "releases")},
		{name: "the folder of a loose Release", start: loose, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw, err := workspaceconfig.Resolve(tc.start, tc.flagRoot, lookup)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got := rw.FoundNoWorkspace(); got != tc.want {
				t.Errorf("FoundNoWorkspace() = %v, want %v (root %s, marker %v)", got, tc.want, rw.Root(), rw.Marker())
			}
			// The refusal of a cache write is asked of the same predicate, so it is
			// returned exactly where the predicate holds.
			refusal := rw.CacheWriteRefusal()
			if (refusal != nil) != tc.want {
				t.Errorf("CacheWriteRefusal() = %v, want a refusal: %v", refusal, tc.want)
			}
		})
	}
	var none *workspaceconfig.ResolvedWorkspace
	if none.FoundNoWorkspace() || none.CacheWriteRefusal() != nil {
		t.Error("a nil resolution was reported as having found no workspace")
	}
}

// TestTheCacheWriteRefusalNamesTheFolderAndTheWaysOut reads the one sentence a
// refused cache write carries: the folder used, every way to name the workspace
// instead, and the marker that makes the folder one. It is a plain error rather
// than a registered condition, matched by its type.
func TestTheCacheWriteRefusalNamesTheFolderAndTheWaysOut(t *testing.T) {
	t.Parallel()
	folder := noWorkspaceFolder(t)
	rw, err := workspaceconfig.Resolve(folder, "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	err = rw.CacheWriteRefusal()
	var refused *workspaceconfig.CacheWriteRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("CacheWriteRefusal() = %v, want a *CacheWriteRefusedError", err)
	}
	if refused.Root() != folder {
		t.Errorf("the refusal is about %s, want %s", refused.Root(), folder)
	}
	for _, want := range []string{
		folder,
		"--" + workspaceconfig.WorkspaceRootFlagName,
		workspaceconfig.EnvWorkspaceRoot,
		workspaceconfig.WorkspaceRootArgument,
		workspaceconfig.ConfigFilename,
		"workspace:",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
	if strings.Count(err.Error(), "\n") != 0 {
		t.Errorf("the refusal spans several lines, want one sentence: %q", err)
	}
}

// TestANamedRootThatIsNotAnExistingFolderIsRefusedByTheResolution drives the
// explicit-root branch of the shared resolution with a root that is not there and
// with one that is a file, through each entry point a surface resolves through,
// and reads the refusal: it quotes the root exactly as given, carries the reason
// the filesystem gave, and opens with the source a surface names. An existing
// folder is the positive control.
//
// 🔴 PLANTED DEFECT: restore the explicit branch of discoverRoot to take the root
// as given (`filepath.Abs(flagRoot)` and return), and every refusing row fails —
// the resolution then succeeds over a folder that does not exist.
func TestANamedRootThatIsNotAnExistingFolderIsRefusedByTheResolution(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-folder")
	file := filepath.Join(t.TempDir(), "a-file")
	write(t, file, "x")
	present := t.TempDir()
	lookup := func(string) string { return "" }

	entryPoints := []struct {
		name    string
		resolve func(root string) error
	}{
		{"Resolve", func(root string) error { _, err := workspaceconfig.Resolve("", root, lookup); return err }},
		{"ResolveWithoutGlobal", func(root string) error {
			_, err := workspaceconfig.ResolveWithoutGlobal("", root, lookup)
			return err
		}},
		{"ResolveWithFlags", func(root string) error {
			_, err := workspaceconfig.ResolveWithFlags(root, root, workspaceconfig.FlagOverlay{})
			return err
		}},
		{"ResolveOrHold", func(root string) error {
			_, _, err := workspaceconfig.ResolveOrHold(root, root, workspaceconfig.FlagOverlay{})
			return err
		}},
	}
	for _, ep := range entryPoints {
		t.Run(ep.name+"/a folder that is not there", func(t *testing.T) {
			t.Parallel()
			err := ep.resolve(missing)
			var named *workspaceconfig.NamedRootError
			if !errors.As(err, &named) {
				t.Fatalf("resolving the root %s returned %v, want a *NamedRootError", missing, err)
			}
			if named.Given() != missing || named.NotADirectory() {
				t.Errorf("the refusal reports given=%q notADirectory=%v", named.Given(), named.NotADirectory())
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the refusal %q does not carry the reason the folder could not be used", err)
			}
			if want := `the workspace root "` + missing + `" cannot be used`; !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal reads %q, want it to carry %q", err, want)
			}
			sourced := workspaceconfig.NameRootSource(err, "--"+workspaceconfig.WorkspaceRootFlagName)
			if want := `--workspace-root names the workspace root "` + missing + `", which cannot be used`; !strings.Contains(sourced.Error(), want) {
				t.Errorf("the refusal named by its source reads %q, want it to carry %q", sourced, want)
			}
			if !errors.Is(sourced, fs.ErrNotExist) {
				t.Errorf("naming the source dropped the reason: %q", sourced)
			}
			// The variable is named with the desktop setting that sets it, in the
			// words every surface reading the variable refuses its root in.
			byVariable := workspaceconfig.NameRootSource(err, workspaceconfig.EnvWorkspaceRootSource)
			if want := `AGGREGA_WORKSPACE_ROOT — the variable the desktop application's Workspace folder setting ` +
				`sets — names the workspace root "` + missing + `", which cannot be used: `; !strings.HasPrefix(byVariable.Error(), want) {
				t.Errorf("the refusal named by the variable reads %q, want it to open with %q", byVariable, want)
			}
		})
		t.Run(ep.name+"/a file", func(t *testing.T) {
			t.Parallel()
			err := ep.resolve(file)
			var named *workspaceconfig.NamedRootError
			if !errors.As(err, &named) || !named.NotADirectory() {
				t.Fatalf("resolving the file %s returned %v, want a *NamedRootError reporting it is not a directory", file, err)
			}
			if !strings.Contains(err.Error(), "is not a directory") {
				t.Errorf("the refusal reads %q, want it to say the root is not a directory", err)
			}
		})
		t.Run(ep.name+"/an existing folder", func(t *testing.T) {
			t.Parallel()
			if err := ep.resolve(present); err != nil {
				t.Errorf("resolving the existing folder %s was refused: %v", present, err)
			}
		})
	}

	if err := workspaceconfig.NameRootSource(errors.New("unrelated"), "--workspace-root"); err.Error() != "unrelated" {
		t.Errorf("an unrelated error was renamed: %q", err)
	}
	if workspaceconfig.NameRootSource(nil, "--workspace-root") != nil {
		t.Error("a nil error was turned into a refusal")
	}
}

// TestTheEmptyWorkspaceFolderRefusalNamesTheSettingTheVariableAndTheFolderToChoose
// holds the sentence a desktop launch with an empty Workspace folder setting is
// refused in to the approved wording, written out rather than composed, so the
// sentence a desktop user reads is what this test holds: it names the setting,
// the variable that carries it, and the folder to choose. It is matched by its
// type, and naming a root's source leaves it as it is, since it names no root.
//
// 🔴 PLANTED DEFECT: change DesktopWorkspaceFolderSetting, and this test fails on
// the wording while the manifest conformance test fails on the title.
func TestTheEmptyWorkspaceFolderRefusalNamesTheSettingTheVariableAndTheFolderToChoose(t *testing.T) {
	t.Parallel()
	const want = "the desktop application launched this server with its Workspace folder setting empty, so " +
		"AGGREGA_WORKSPACE_ROOT names no workspace root: set Workspace folder to the folder that holds your " +
		"workspace's .aggrega-config.yaml"
	var err error = &workspaceconfig.EmptyWorkspaceFolderError{}
	if err.Error() != want {
		t.Errorf("the refusal reads\n%q\nwant\n%q", err, want)
	}
	var empty *workspaceconfig.EmptyWorkspaceFolderError
	if wrapped := fmt.Errorf("resolve workspace: %w", err); !errors.As(wrapped, &empty) {
		t.Errorf("the refusal is not matched by its type through a wrapper: %v", wrapped)
	}
	if named := workspaceconfig.NameRootSource(err, workspaceconfig.EnvWorkspaceRootSource); named.Error() != want {
		t.Errorf("naming a root's source changed the refusal to %q", named)
	}
	var namedRoot *workspaceconfig.NamedRootError
	if errors.As(err, &namedRoot) {
		t.Error("the refusal of an empty setting is matched as the refusal of a named root")
	}
}
