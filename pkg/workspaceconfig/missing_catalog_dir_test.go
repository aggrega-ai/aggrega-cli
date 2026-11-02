// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAConfiguredCatalogDirectoryThatIsNotADirectoryIsRefused pins the one
// decision every surface reads a configured catalog directory through: a
// directory the configuration lists — at any layer that can list one, for the
// workspace or for one Release — that is not a directory is refused, by the stack,
// its named part, the whole-catalog passes and a Release's reach alike, with one
// error naming the entry as written, the path it resolved to, the file and line
// listing it and the list it sits in.
//
// Every row resolves one workspace through the real cascade. The first row is the
// positive control: the listed directory is there, empty, and nothing refuses.
// Each row after it holds one entry to one answer: refused with exactly the
// message the row states, or — a `--catalog` directory, a directory a flag also
// names, a Release whose own list does not carry the missing directory — not
// refused. The stack is returned as listed beside a refusal, which a row checks
// too.
func TestAConfiguredCatalogDirectoryThatIsNotADirectoryIsRefused(t *testing.T) {
	t.Parallel()
	const (
		exists     = "which does not exist"
		createIt   = "create that directory"
		isAFile    = "which is not a directory"
		listIt     = "list the directory you meant"
		dangles    = "which is a link leading to a path that does not exist"
		pointIt    = "point the link at a directory"
		wsList     = "`workspace.catalog_dirs`"
		wsAppend   = "`workspace.catalog_dirs_append`"
		relList    = "`catalog_dirs` of the `workspace.releases` entry \"release.yaml\""
		relAppend  = "`catalog_dirs_append` of the `workspace.releases` entry \"release.yaml\""
		configFile = workspaceconfig.ConfigFilename
	)
	type refusal struct {
		// file is the file listing the entry, relative to the root, and line its
		// 1-based line there; an empty file names the environment variable.
		file    string
		line    int
		setting string
		written string
		// dir is the path the entry resolved to, relative to the root.
		dir    string
		what   string
		remedy string
	}
	for _, tc := range []struct {
		name   string
		config string
		// ci is the CI-owned overlay beside the configuration, global the global
		// configuration file at global/config.yaml under the root, and env the value
		// of AGGREGA_CATALOG_DIRS, each written only where set.
		ci, global, env string
		// make creates each of these directories, relative to the root; file creates
		// each as a file; dangling creates each as a link leading nowhere.
		make, file, dangling []string
		// release asks for that Release's stack, relative to the root; empty asks for
		// the workspace's own.
		release string
		// flags are --catalog values, relative to the root.
		flags []string
		// stack is the stack returned, relative to the root, in order.
		stack []string
		// refused is the refusal, or nil where nothing is refused.
		refused *refusal
	}{
		{name: "control: a listed directory that is there, empty, is read and refused nowhere",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./listed\n", make: []string{"own", "listed"},
			stack: []string{"own", "listed"}},
		{name: "a directory the workspace's list names where nothing exists is refused at its line",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./no-such-catalog\n", make: []string{"own"},
			stack:   []string{"own", "no-such-catalog"},
			refused: &refusal{configFile, 4, wsList, "./no-such-catalog", "no-such-catalog", exists, createIt}},
		{name: "a directory the workspace's append names where nothing exists is refused under the append",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n  catalog_dirs_append:\n    - ./gone-append\n", make: []string{"own"},
			stack:   []string{"own", "gone-append"},
			refused: &refusal{configFile, 5, wsAppend, "./gone-append", "gone-append", exists, createIt}},
		{name: "a directory a Release's own list names where nothing exists is refused for that Release",
			config:  "workspace:\n  catalog_dirs:\n    - ./own\n  releases:\n    release.yaml:\n      catalog_dirs:\n        - ./own\n        - ./gone-for-release\n",
			make:    []string{"own"},
			release: "release.yaml",
			stack:   []string{"own", "gone-for-release"},
			refused: &refusal{configFile, 8, relList, "./gone-for-release", "gone-for-release", exists, createIt}},
		{name: "a directory a Release's addition names where nothing exists is refused under the addition",
			config:  "workspace:\n  catalog_dirs:\n    - ./own\n  releases:\n    release.yaml:\n      catalog_dirs_append:\n        - ./gone-extra\n",
			make:    []string{"own"},
			release: "release.yaml",
			stack:   []string{"own", "gone-extra"},
			refused: &refusal{configFile, 7, relAppend, "./gone-extra", "gone-extra", exists, createIt}},
		{name: "a Release whose own list leaves the missing workspace directory out is not refused for it",
			config:  "workspace:\n  catalog_dirs:\n    - ./no-such-catalog\n  releases:\n    release.yaml:\n      catalog_dirs:\n        - ./own\n",
			make:    []string{"own"},
			release: "release.yaml",
			stack:   []string{"own"}},
		{name: "a directory listed twice where nothing exists is refused at its first entry",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./no-such-catalog\n    - no-such-catalog\n", make: []string{"own"},
			stack:   []string{"own", "no-such-catalog"},
			refused: &refusal{configFile, 4, wsList, "./no-such-catalog", "no-such-catalog", exists, createIt}},
		{name: "a flow-style list is refused at the line its key is on",
			config: "workspace:\n  catalog_dirs: [./own, ./no-such-catalog]\n", make: []string{"own"},
			stack:   []string{"own", "no-such-catalog"},
			refused: &refusal{configFile, 2, wsList, "./no-such-catalog", "no-such-catalog", exists, createIt}},
		{name: "every listed directory missing is refused rather than read as the off-switch",
			config: "workspace:\n  catalog_dirs:\n    - ./no-such-catalog\n", make: []string{workspaceconfig.ConventionalCatalogDir},
			stack:   []string{"no-such-catalog", workspaceconfig.ConventionalCatalogDir},
			refused: &refusal{configFile, 3, wsList, "./no-such-catalog", "no-such-catalog", exists, createIt}},
		{name: "a listed path holding a file is refused as not a directory",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./a-file\n", make: []string{"own"}, file: []string{"a-file"},
			stack:   []string{"own", "a-file"},
			refused: &refusal{configFile, 4, wsList, "./a-file", "a-file", isAFile, listIt}},
		{name: "a listed link leading nowhere is refused as a link leading nowhere",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./dangling\n", make: []string{"own"}, dangling: []string{"dangling"},
			stack:   []string{"own", "dangling"},
			refused: &refusal{configFile, 4, wsList, "./dangling", "dangling", dangles, pointIt}},
		{name: "a directory the CI-owned overlay adds where nothing exists is refused in the overlay",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n", ci: "workspace:\n  catalog_dirs_append:\n    - ./gone-ci\n",
			make:    []string{"own"},
			stack:   []string{"own", "gone-ci"},
			refused: &refusal{workspaceconfig.AppendCIConfigFilename, 3, wsAppend, "./gone-ci", "gone-ci", exists, createIt}},
		{name: "a directory the global configuration adds where nothing exists is refused in the global file",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n", global: "workspace:\n  catalog_dirs_append:\n    - ./gone-global\n",
			make:    []string{"own"},
			stack:   []string{"own", "global/gone-global"},
			refused: &refusal{"global/config.yaml", 3, wsAppend, "./gone-global", "global/gone-global", exists, createIt}},
		{name: "a directory the environment variable lists where nothing exists is refused naming the variable",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n", env: "env-gone", make: []string{"own"},
			stack:   []string{"env-gone"},
			refused: &refusal{"", 0, "", "env-gone", "env-gone", exists, createIt}},
		{name: "a --catalog directory where nothing exists is not refused here",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n", make: []string{"own"}, flags: []string{"no-such-flag-dir"},
			stack: []string{"no-such-flag-dir", "own"}},
		{name: "a listed directory a --catalog flag also names is the flag's entry, not refused here",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./no-such-catalog\n", make: []string{"own"}, flags: []string{"no-such-catalog"},
			stack: []string{"no-such-catalog", "own"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			for _, d := range tc.make {
				if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range tc.file {
				write(t, filepath.Join(ws, f), "not a directory\n")
			}
			for _, l := range tc.dangling {
				link(t, filepath.Join(ws, "nowhere", l), filepath.Join(ws, l))
			}
			write(t, filepath.Join(ws, "release.yaml"), "kind: Release\n")
			write(t, filepath.Join(ws, workspaceconfig.ConfigFilename), tc.config)
			if tc.ci != "" {
				write(t, filepath.Join(ws, workspaceconfig.AppendCIConfigFilename), tc.ci)
			}
			env := map[string]string{}
			if tc.global != "" {
				write(t, filepath.Join(ws, "global", "config.yaml"), tc.global)
				env[workspaceconfig.EnvConfigOverride] = filepath.Join(ws, "global", "config.yaml")
			}
			if tc.env != "" {
				env["AGGREGA_CATALOG_DIRS"] = filepath.Join(ws, tc.env)
			}
			rw, err := workspaceconfig.Resolve(ws, ws, func(k string) string { return env[k] })
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			at := func(rel string) string { return filepath.Join(rw.Root(), rel) }
			if tc.global != "" || tc.env != "" {
				at = func(rel string) string { return filepath.Join(ws, rel) }
			}
			release := ""
			if tc.release != "" {
				release = at(tc.release)
			}
			var flags []string
			for _, f := range tc.flags {
				flags = append(flags, at(f))
			}
			wantStack := make([]string, 0, len(tc.stack))
			for _, d := range tc.stack {
				wantStack = append(wantStack, at(d))
			}
			var want string
			if r := tc.refused; r != nil {
				switch {
				case r.file == "":
					want = fmt.Sprintf("the AGGREGA_CATALOG_DIRS environment variable: the entry %q names %s, %s; %s, or remove the entry from the variable: workspace configuration is invalid",
						at(r.written), at(r.dir), r.what, r.remedy)
				default:
					want = fmt.Sprintf("%s:%d: the %s entry %q names %s, %s; %s, or remove the entry: workspace configuration is invalid",
						at(r.file), r.line, r.setting, r.written, at(r.dir), r.what, r.remedy)
				}
			}
			refusedAs := func(reader string, err error) {
				t.Helper()
				switch {
				case want == "" && err != nil:
					t.Errorf("%s refused: %v; want no refusal", reader, err)
				case want != "" && err == nil:
					t.Errorf("%s refused nothing; want %q", reader, want)
				case want != "" && !errors.Is(err, workspaceconfig.ErrConfigInvalid()):
					t.Errorf("%s refused with %v, which is not ErrConfigInvalid", reader, err)
				case want != "" && err.Error() != want:
					t.Errorf("%s refused with\n%q\nwant\n%q", reader, err.Error(), want)
				}
			}

			stack, err := rw.ResolveCatalogDirs(release, flags)
			refusedAs("the stack", err)
			if !slices.Equal(stack, wantStack) {
				t.Errorf("the stack is %v; want %v", stack, wantStack)
			}
			named, err := rw.NamedCatalogDirs(release, flags)
			refusedAs("the named part", err)
			for _, d := range named {
				if !slices.Contains(stack, d) {
					t.Errorf("the named part names %s, which the stack %v does not hold", d, stack)
				}
			}
			if release == "" {
				_, err = rw.WholeCatalogPasses(nil, flags)
				refusedAs("the whole-catalog passes", err)
			} else {
				_, err = rw.ReleaseReach(release, flags)
				refusedAs("the Release's reach", err)
				_, err = rw.WholeCatalogPasses(func() []string { return []string{tc.release} }, flags)
				refusedAs("the whole-catalog passes", err)
			}
		})
	}
}

// stackOf returns the stack rw resolves for release and flags, failing the test
// on a refusal: a row that means to read a stack lists directories that are there.
func stackOf(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, release string, flags []string) []string {
	t.Helper()
	dirs, err := rw.ResolveCatalogDirs(release, flags)
	if err != nil {
		t.Fatalf("the stack for %q was refused: %v", release, err)
	}
	return dirs
}

// namedOf returns the named part of the stack rw resolves for release and flags,
// failing the test on a refusal, as stackOf does.
func namedOf(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, release string, flags []string) []string {
	t.Helper()
	dirs, err := rw.NamedCatalogDirs(release, flags)
	if err != nil {
		t.Fatalf("the named part of the stack for %q was refused: %v", release, err)
	}
	return dirs
}

// passesOf returns the whole-catalog passes rw runs, failing the test on a
// refusal, as stackOf does.
func passesOf(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, releases func() []string, flags []string) []workspaceconfig.CatalogPass {
	t.Helper()
	passes, err := rw.WholeCatalogPasses(releases, flags)
	if err != nil {
		t.Fatalf("the whole-catalog passes were refused: %v", err)
	}
	return passes
}
