// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// runVerb runs one command through the real command tree from root, with the
// process stderr captured, and returns what the command printed there and its
// error.
func runVerb(t *testing.T, root string, args ...string) (stderr string, err error) {
	t.Helper()
	var out string
	stderr = captureStderr(t, func() { out, err = runAggrega(t, append([]string{"--workspace-root", root}, args...)...) })
	if err != nil {
		stderr += "\ncommand output:\n" + out
	}
	return stderr, err
}

// TestCommandsRefuseAConfiguredCatalogDirectoryThatDoesNotExist is the invocation
// proof that every command reading a Release's catalog refuses a directory the
// workspace configuration lists where nothing exists, with one refusal naming the
// entry as written, the path it resolved to, the configuration file and line, and
// the list it sits in — and writes nothing: no lockfile, no rendered file.
//
// The directory is listed the way a copy of a worked example taken without its
// sibling checkout lists it: a path leading out of the workspace to a directory
// that is not there. Each row's workspace differs from its reference workspace in
// that listing alone, and the reference — the control — is pulled and built by
// the same commands, so a row fails on how the listing is read and on nothing
// else. The rows hold the listing in each place a configuration carries it — the
// workspace's list and a Release's own entry — and a build that would fill its
// own lockfile, which is refused before the fill writes one. The last row is the
// boundary of the rule: a directory typed with --catalog that does not exist is
// refused by the build's load instead, as a catalog error naming it, because a
// flag is typed for one invocation and is not configuration.
func TestCommandsRefuseAConfiguredCatalogDirectoryThatDoesNotExist(t *testing.T) {
	// Not parallel: every command runs from the workspace it reads, which moves the
	// process working directory, and its output is read from the process stderr.
	src, err := filepath.Abs(releaseCatalogFixture)
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	const (
		written      = "../no-such-hub/aggrega-catalog"
		wsSetting    = "`workspace.catalog_dirs`"
		ownSetting   = "`catalog_dirs` of the `workspace.releases` entry \"release-own.yaml\""
		sharedConfig = "workspace:\n  catalog_dirs:\n    - catalog-shared\nauto_pull: false\n"
	)
	for _, tc := range []struct {
		name    string
		release string
		// config is the workspace configuration listing the directory where nothing
		// exists; reference is the same configuration without it.
		config, reference string
		// line is the entry's line in config, and setting the list it sits in.
		line    int
		setting string
		// autoPull builds with --auto-pull and runs no pull of its own.
		autoPull bool
		// flag is a --catalog directory the build is given, relative to the root.
		flag string
	}{
		{
			name:      "a directory the workspace's list names is refused by every command reading the catalog",
			release:   "release-shared.yaml",
			config:    "workspace:\n  catalog_dirs:\n    - catalog-shared\n    - " + written + "\nauto_pull: false\n",
			reference: sharedConfig,
			line:      4,
			setting:   wsSetting,
		},
		{
			name:    "a directory a Release's own entry names is refused by every command reading that Release",
			release: "release-own.yaml",
			config: "workspace:\n  catalog_dirs:\n    - catalog-shared\n  releases:\n    release-own.yaml:\n      catalog_dirs:\n" +
				"        - catalog-own\n        - " + written + "\nauto_pull: false\n",
			reference: "workspace:\n  catalog_dirs:\n    - catalog-shared\n  releases:\n    release-own.yaml:\n      catalog_dirs:\n" +
				"        - catalog-own\nauto_pull: false\n",
			line:    8,
			setting: ownSetting,
		},
		{
			name:      "a build that would fill its own lockfile is refused before it writes one",
			release:   "release-shared.yaml",
			config:    "workspace:\n  catalog_dirs:\n    - catalog-shared\n    - " + written + "\nauto_pull: false\n",
			reference: sharedConfig,
			line:      4,
			setting:   wsSetting,
			autoPull:  true,
		},
		{
			name:      "a --catalog directory that does not exist is refused by the build's load, naming it",
			release:   "release-shared.yaml",
			config:    sharedConfig,
			reference: sharedConfig,
			flag:      "no-such-flag-dir",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Both workspaces are laid down before either command moves the working
			// directory into one of them.
			root := releaseCatalogWorkspace(t, src, tc.config)
			referenceRoot := releaseCatalogWorkspace(t, src, tc.reference)

			// The control: the same files without the listing pull and build.
			if tree := buildFromTheCommandLine(t, referenceRoot, tc.release); len(tree) == 0 {
				t.Fatalf("the reference build wrote nothing, so the row proves nothing")
			}

			t.Chdir(root)
			if tc.flag != "" {
				if stderr, err := runVerb(t, root, "pull", "--release", tc.release); err != nil {
					t.Fatalf("pull refused: %v\nstderr:\n%s", err, stderr)
				}
				stderr, buildErr := runVerb(t, root, "build", "--release", tc.release, "--catalog", tc.flag)
				if buildErr == nil {
					t.Fatalf("the build ran over a --catalog directory that does not exist; stderr:\n%s", stderr)
				}
				var load *cerrors.CompilerError
				if !errors.As(buildErr, &load) || load.Category() != cerrors.CatalogError {
					t.Fatalf("the build's refusal carries no catalog error: %v", buildErr)
				}
				if !sameMissingPath(t, load.Source().File, filepath.Join(root, tc.flag)) {
					t.Errorf("the refusal names %q; want the --catalog directory %q", load.Source().File, filepath.Join(root, tc.flag))
				}
				if !errors.Is(buildErr, fs.ErrNotExist) {
					t.Errorf("the refusal does not carry the file system's not-exist condition: %v", buildErr)
				}
				if errors.Is(buildErr, workspaceconfig.ErrConfigInvalid()) {
					t.Errorf("a --catalog directory was refused as a configured one: %v", buildErr)
				}
				return
			}

			want := fmt.Sprintf("%s:%d: the %s entry %q names %s, which does not exist; create that directory, or remove the entry: %v",
				filepath.Join(root, workspaceconfig.ConfigFilename), tc.line, tc.setting, written,
				filepath.Join(filepath.Dir(root), "no-such-hub", "aggrega-catalog"), workspaceconfig.ErrConfigInvalid())
			build := []string{"build", "--release", tc.release}
			if tc.autoPull {
				build = append(build, "--auto-pull")
			}
			verbs := [][]string{
				build,
				{"inspect", tc.release, "--ir-graph"},
				{"validate", "--release", tc.release},
			}
			if !tc.autoPull {
				verbs = append([][]string{{"pull", "--release", tc.release}}, verbs...)
			}
			for _, args := range verbs {
				stderr, err := runVerb(t, root, args...)
				switch {
				case err == nil:
					t.Errorf("aggrega %s read past the listed directory; stderr:\n%s", strings.Join(args, " "), stderr)
				case !errors.Is(err, workspaceconfig.ErrConfigInvalid()):
					t.Errorf("aggrega %s refused with %v, which is not the configuration refusal", strings.Join(args, " "), err)
				case !strings.Contains(err.Error(), want):
					t.Errorf("aggrega %s refused with\n%s\nwant it to carry\n%s", strings.Join(args, " "), err.Error(), want)
				}
			}
			if _, statErr := os.Stat(lockfile.PathFor(filepath.Join(root, tc.release))); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("a lockfile was written beside %s over a refused stack: %v", tc.release, statErr)
			}
			if _, statErr := os.Stat(filepath.Join(root, artifactout.DefaultOutputDir)); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("rendered output was written over a refused stack: %v", statErr)
			}
		})
	}
}

// stackFor returns the catalog search stack rw resolves for release and flags,
// failing the test on a refusal: a test that means to read a stack lists
// directories that are there.
func stackFor(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, release string, flags []string) []string {
	t.Helper()
	dirs, err := rw.ResolveCatalogDirs(release, flags)
	if err != nil {
		t.Fatalf("the catalog search stack for %q was refused: %v", release, err)
	}
	return dirs
}

// namedStackFor returns the named part of the stack stackFor returns, failing the
// test on a refusal as stackFor does.
func namedStackFor(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, release string, flags []string) []string {
	t.Helper()
	dirs, err := rw.NamedCatalogDirs(release, flags)
	if err != nil {
		t.Fatalf("the named part of the catalog search stack for %q was refused: %v", release, err)
	}
	return dirs
}

// passesFor returns the passes a whole-catalog check runs in rw, failing the test
// on a refusal as stackFor does.
func passesFor(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, flags []string) []workspaceconfig.CatalogPass {
	t.Helper()
	passes, err := serve.WholeCatalogPasses(rw, flags)
	if err != nil {
		t.Fatalf("the whole-catalog passes were refused: %v", err)
	}
	return passes
}

// sameMissingPath reports whether got and want name one path where nothing
// exists: the same final name in one directory. The directory is compared with
// its links resolved, because a working directory may be reported in another
// spelling of the same place, while the final name cannot be resolved at all.
func sameMissingPath(t *testing.T, got, want string) bool {
	t.Helper()
	if filepath.Base(got) != filepath.Base(want) {
		return false
	}
	gotDir, err := filepath.EvalSymlinks(filepath.Dir(got))
	if err != nil {
		return false
	}
	wantDir, err := filepath.EvalSymlinks(filepath.Dir(want))
	if err != nil {
		t.Fatalf("resolve %s: %v", filepath.Dir(want), err)
	}
	return gotDir == wantDir
}
