// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseEntityCheckWorkspace copies the shared fixture keeping two variants of
// one Component side by side, one per Release, and returns its root.
//
// The fixture's variant-sized/ declares demo/datastore/db with its storage drawn
// from the SchemaLibrary Sizing, which variant-sized/ alone carries;
// variant-plain/ declares the same Component referring to no library. The
// configuration written here gives release-sized.yaml variant-sized/ and
// release-plain.yaml variant-plain/, with no workspace list and no conventional
// catalog directory, so a check naming no Release reads no catalog directory at
// all.
func releaseEntityCheckWorkspace(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "internal", "compiler", "testdata", "release-entity-check"))
	if err != nil {
		t.Fatalf("locate the fixture: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	config := "workspace:\n  releases:\n    release-sized.yaml:\n      catalog_dirs:\n        - variant-sized\n" +
		"    release-plain.yaml:\n      catalog_dirs:\n        - variant-plain\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// TestValidateChecksAnEntityInTheTermsOfTheReleaseNamed is the command line's
// proof that one entity's check reads the Release it is given, driven through the
// real command tree with every sink a run speaks through read: cobra's two
// writers, the process error stream, and the status.
//
// The subject is a Component whose pillar refers to a library only one variant
// carries, so whether the reference resolves is decided by which stack the check
// reads and by nothing else. The row naming no Release is the answer before
// --release could be given beside an entity, and the row naming the other Release
// is its control: both fail naming the library, while the Release carrying it
// passes. The remaining rows each carry one fault of the flag itself, or of a
// flag that has nothing to apply to beside an entity.
//
// Paths are passed absolute, because the entity and the flag are paths a person
// types and are read against the working directory, which a test does not
// control.
func TestValidateChecksAnEntityInTheTermsOfTheReleaseNamed(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr,
	// and the environment seams below are process-global too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := releaseEntityCheckWorkspace(t)
	at := func(name string) string { return filepath.Join(root, name) }
	sized := at("variant-sized/component/db.yaml")
	const libraryMissing = "SchemaLibrary Sizing not found in catalog"

	cases := []struct {
		name string
		args []string
		code int
		// carries are the phrases the run's streams must carry, and lacks the ones
		// they must not.
		carries []string
		lacks   []string
	}{
		{name: "no Release reads the workspace's own stack, which carries no library",
			args:    []string{"validate", sized},
			code:    1,
			carries: []string{libraryMissing, "E0121"},
			lacks:   []string{"Pillars: valid"}},
		{name: "the Release whose directories carry the library resolves it",
			args:    []string{"validate", sized, "--release", at("release-sized.yaml")},
			carries: []string{"Kind:   Component", "Pillars: valid"},
			lacks:   []string{libraryMissing}},
		{name: "a Release whose directories do not carry the library fails naming it",
			args:    []string{"validate", sized, "--release", at("release-plain.yaml")},
			code:    1,
			carries: []string{libraryMissing, "E0121"},
			lacks:   []string{"Pillars: valid"}},
		{name: "--catalog is prepended to the Release's stack for one invocation",
			args:    []string{"validate", sized, "--release", at("release-plain.yaml"), "--catalog", at("variant-sized")},
			carries: []string{"Pillars: valid"},
			lacks:   []string{libraryMissing}},
		{name: "the other variant is checked in its own Release's terms",
			args:    []string{"validate", at("variant-plain/component/db.yaml"), "--release", at("release-plain.yaml")},
			carries: []string{"Pillars: valid"}},
		{name: "a Release that is not there is refused",
			args:    []string{"validate", sized, "--release", at("release-gone.yaml")},
			code:    1,
			carries: []string{"the Release release-gone.yaml does not exist"},
			lacks:   []string{"Kind:   Component"}},
		{name: "a directory is refused",
			args:    []string{"validate", sized, "--release", at("variant-sized")},
			code:    1,
			carries: []string{"the Release variant-sized is a directory"},
			lacks:   []string{"Kind:   Component"}},
		{name: "the graph has nothing to come from beside an entity",
			args:    []string{"validate", sized, "--release", at("release-sized.yaml"), "--ir-graph"},
			code:    1,
			carries: []string{"--ir-graph applies to a Release's dependency closure, and an entity argument checks that one entity"},
			lacks:   []string{"Kind:   Component"}},
		{name: "a plugin binary still takes no Release",
			args:    []string{"validate", sized, "--release", at("release-sized.yaml"), "--plugin", at("absent.wasm")},
			code:    1,
			carries: []string{"--plugin is mutually exclusive with --release"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRootCmd()
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetIn(strings.NewReader(""))
			cmd.SetArgs(append([]string{"--workspace-root", root}, tc.args...))

			var code int
			process := captureStderr(t, func() {
				code = executeRoot(context.Background(), cmd)
			})
			streams := out.String() + errOut.String() + process
			if code != tc.code {
				t.Fatalf("`aggrega %s` exited %d; want %d\nstreams:\n%s", strings.Join(tc.args, " "), code, tc.code, streams)
			}
			for _, phrase := range tc.carries {
				if !strings.Contains(streams, phrase) {
					t.Errorf("`aggrega %s` does not carry %q:\n%s", strings.Join(tc.args, " "), phrase, streams)
				}
			}
			for _, phrase := range tc.lacks {
				if strings.Contains(streams, phrase) {
					t.Errorf("`aggrega %s` carries %q:\n%s", strings.Join(tc.args, " "), phrase, streams)
				}
			}
		})
	}
}
