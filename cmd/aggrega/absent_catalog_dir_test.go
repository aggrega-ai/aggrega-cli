// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// absentCatalogDirNotice is the part of the notice every line about a configured
// directory where nothing exists carries after the directory, given the words
// naming the setting that lists it.
func absentCatalogDirNotice(settingWords string) string {
	return " is listed under " + settingWords + " and does not exist, so nothing is read from it"
}

// noticeLinesNaming returns every line of stream that is the notice for a
// directory whose path ends in dir, under the setting settingWords names.
func noticeLinesNaming(stream, dir, settingWords string) []string {
	var lines []string
	for _, line := range strings.Split(stream, "\n") {
		if strings.HasPrefix(line, "  catalog: ") && strings.Contains(line, dir+absentCatalogDirNotice(settingWords)) {
			lines = append(lines, line)
		}
	}
	return lines
}

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

// TestPullAndBuildReadAConfiguredCatalogDirectoryWhereNothingExistsAlike is the
// invocation proof that `aggrega pull` and the `aggrega build` it feeds read a
// configured catalog directory where nothing exists the same way: both run past
// it, both say so naming the directory, and the build — which verifies the
// lockfile the pull wrote — writes exactly what it writes for the same Release
// with the directory not listed at all.
//
// The directory is listed the way a copy of a worked example taken without its
// sibling checkout lists it: a path leading out of the workspace to a directory
// that is not there. Each row's workspace differs from its reference workspace in
// that listing alone, so a row fails on how the listing is read and on nothing
// else. The reference is built in a workspace of its own, by the same commands,
// and the two rendered trees are compared file for file and byte for byte.
//
// The rows hold the listing in each place a configuration can carry it — the
// workspace's list and a Release's own entry — and the build that fills its own
// lockfile, which reads the stack through its fill and must say so once rather
// than twice. The last row is the boundary of the rule: a directory typed with
// --catalog that does not exist is still refused by the build's load, naming it,
// because a flag is typed for one invocation and a mistyped one is answered by the
// refusal.
func TestPullAndBuildReadAConfiguredCatalogDirectoryWhereNothingExistsAlike(t *testing.T) {
	// Not parallel: every command runs from the workspace it builds, which moves
	// the process working directory, and its notices are read from the process
	// stderr.
	src, err := filepath.Abs(releaseCatalogFixture)
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	const (
		workspaceWords = "`workspace.catalog_dirs` in .aggrega-config.yaml"
		releaseWords   = "`workspace.catalog_dirs`, or a Release's own `catalog_dirs` under `workspace.releases`, in .aggrega-config.yaml"
	)
	// missingSibling is the listing every row adds: a sibling checkout's catalog,
	// beside the workspace, that is not there.
	missingSibling := filepath.Join("no-such-hub", "aggrega-catalog")
	for _, tc := range []struct {
		name    string
		release string
		// config is the workspace configuration listing the directory where nothing
		// exists; reference is the same configuration without it.
		config, reference string
		// words names the setting the notice names the directory under.
		words string
		// autoPull builds with --auto-pull and runs no pull of its own.
		autoPull bool
		// flag is a --catalog directory the build is given, relative to the root.
		flag string
	}{
		{
			name:      "a directory the workspace's list names is read as holding nothing by the pull and by its build",
			release:   "release-shared.yaml",
			config:    "workspace:\n  catalog_dirs:\n    - catalog-shared\n    - ../no-such-hub/aggrega-catalog\nauto_pull: false\n",
			reference: "workspace:\n  catalog_dirs:\n    - catalog-shared\nauto_pull: false\n",
			words:     workspaceWords,
		},
		{
			name:    "a directory a Release's own entry names is read as holding nothing by the pull and by its build",
			release: "release-own.yaml",
			config: "workspace:\n  catalog_dirs:\n    - catalog-shared\n  releases:\n    release-own.yaml:\n      catalog_dirs:\n" +
				"        - catalog-own\n        - ../no-such-hub/aggrega-catalog\nauto_pull: false\n",
			reference: "workspace:\n  catalog_dirs:\n    - catalog-shared\n  releases:\n    release-own.yaml:\n      catalog_dirs:\n" +
				"        - catalog-own\nauto_pull: false\n",
			words: releaseWords,
		},
		{
			name:      "a build that fills its own lockfile reads it as holding nothing and says so once",
			release:   "release-shared.yaml",
			config:    "workspace:\n  catalog_dirs:\n    - catalog-shared\n    - ../no-such-hub/aggrega-catalog\nauto_pull: false\n",
			reference: "workspace:\n  catalog_dirs:\n    - catalog-shared\nauto_pull: false\n",
			words:     workspaceWords,
			autoPull:  true,
		},
		{
			name:      "a --catalog directory that does not exist is still refused by the build, naming it",
			release:   "release-shared.yaml",
			config:    "workspace:\n  catalog_dirs:\n    - catalog-shared\nauto_pull: false\n",
			reference: "workspace:\n  catalog_dirs:\n    - catalog-shared\nauto_pull: false\n",
			flag:      "no-such-flag-dir",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Both workspaces are laid down before either command moves the working
			// directory into one of them.
			root := releaseCatalogWorkspace(t, src, tc.config)
			referenceRoot := releaseCatalogWorkspace(t, src, tc.reference)

			want := buildFromTheCommandLine(t, referenceRoot, tc.release)

			t.Chdir(root)
			var pullStderr string
			if !tc.autoPull {
				var pullErr error
				pullStderr, pullErr = runVerb(t, root, "pull", "--release", tc.release)
				if pullErr != nil {
					t.Fatalf("pull refused: %v\nstderr:\n%s", pullErr, pullStderr)
				}
			}
			buildArgs := []string{"build", "--release", tc.release}
			if tc.autoPull {
				buildArgs = append(buildArgs, "--auto-pull")
			}
			if tc.flag != "" {
				buildArgs = append(buildArgs, "--catalog", tc.flag)
			}
			buildStderr, buildErr := runVerb(t, root, buildArgs...)

			if tc.flag != "" {
				if buildErr == nil {
					t.Fatalf("the build ran over a --catalog directory that does not exist; stderr:\n%s", buildStderr)
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
				if strings.Contains(pullStderr+buildStderr, absentCatalogDirNotice(workspaceWords)) {
					t.Errorf("a --catalog directory was noted as a configured one\npull stderr:\n%s\nbuild stderr:\n%s", pullStderr, buildStderr)
				}
				return
			}

			if buildErr != nil {
				t.Fatalf("the build refused what its pull ran past: %v\nbuild stderr:\n%s\npull stderr:\n%s", buildErr, buildStderr, pullStderr)
			}
			if !tc.autoPull {
				if lines := noticeLinesNaming(pullStderr, missingSibling, tc.words); len(lines) != 1 {
					t.Errorf("the pull printed the notice naming %s %d time(s); want once\nstderr:\n%s", missingSibling, len(lines), pullStderr)
				}
			}
			if lines := noticeLinesNaming(buildStderr, missingSibling, tc.words); len(lines) != 1 {
				t.Errorf("the build printed the notice naming %s %d time(s); want once\nstderr:\n%s", missingSibling, len(lines), buildStderr)
			}
			if _, statErr := os.Stat(lockfile.PathFor(filepath.Join(root, tc.release))); statErr != nil {
				t.Errorf("no lockfile beside %s, so the build verified nothing: %v", tc.release, statErr)
			}
			got := renderedTree(t, filepath.Join(root, artifactout.DefaultOutputDir))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the build over the listing wrote what the build without it did not\n got: %v\nwant: %v", got, want)
			}
		})
	}
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
