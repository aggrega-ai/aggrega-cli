// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseVariantWorkspace copies the shared fixture keeping two variants of one
// Component side by side, one per Release, and returns its root.
//
// The fixture's catalog-a/ and catalog-b/ each declare demo/datastore/db, and only
// catalog-b/ declares demo/datastore/cache; its external/ directory, copied to a
// directory of its own outside the workspace, holds the Feature demo/audit-trail.
// The configuration written here gives release-a.yaml catalog-a/ and that outside
// directory, and release-b.yaml catalog-b/, with no workspace list and no
// conventional catalog directory, so a lookup naming no Release reads no catalog
// directory at all.
func releaseVariantWorkspace(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "internal", "compiler", "testdata", "release-variant-catalogs"))
	if err != nil {
		t.Fatalf("locate the fixture: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join(src, "workspace"))); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	external := t.TempDir()
	if err := os.CopyFS(external, os.DirFS(filepath.Join(src, "external"))); err != nil {
		t.Fatalf("copy the fixture's outside directory: %v", err)
	}
	config := fmt.Sprintf("workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs:\n        - catalog-a\n"+
		"        - %s\n    release-b.yaml:\n      catalog_dirs:\n        - catalog-b\n", external)
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// TestInspectLooksANameUpInTheTermsOfTheReleaseNamed is the command line's half
// of the lookup proof, driven through the real command tree with every sink a
// run speaks through read: cobra's two writers, the process error stream, and
// the status.
//
// The two resolving rows disagree in the one thing that tells the variants apart,
// what each says about itself. The refusal row is paired with a control asking
// the same name of the Release that does declare it, so the refusal is about the
// Release named. The remaining rows each carry one fault of the flag itself: a
// Release that is not there, and a subject that has no catalog stack to choose.
//
// The Release is passed absolute, because the flag is a path a person types and
// is read against the working directory, which a test does not control.
func TestInspectLooksANameUpInTheTermsOfTheReleaseNamed(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr,
	// and the environment seams below are process-global too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := releaseVariantWorkspace(t)
	at := func(name string) string { return filepath.Join(root, name) }

	cases := []struct {
		name string
		args []string
		code int
		// carries are the phrases the run's streams must carry, and lacks the ones
		// they must not.
		carries []string
		lacks   []string
	}{
		{name: "release-a.yaml resolves its own variant",
			args:    []string{"inspect", "demo/datastore/db", "--release", at("release-a.yaml")},
			carries: []string{"the variant release-a.yaml builds with"},
			lacks:   []string{"the variant release-b.yaml builds with"}},
		{name: "release-b.yaml resolves its own variant",
			args:    []string{"inspect", "demo/datastore/db", "--release", at("release-b.yaml")},
			carries: []string{"the variant release-b.yaml builds with"},
			lacks:   []string{"the variant release-a.yaml builds with"}},
		{name: "a name only the other Release's directories declare is refused with that Release's account",
			args: []string{"inspect", "demo/datastore/cache", "--release", at("release-a.yaml")},
			code: 1,
			carries: []string{`"demo/datastore/cache" matched no catalog entity of any kind`,
				"local entities for the Release `release-a.yaml`", workspaceconfig.ReleaseCatalogsSetting}},
		{name: "the same name resolves for the Release that declares it",
			args:    []string{"inspect", "demo/datastore/cache", "--release", at("release-b.yaml")},
			carries: []string{"declared only among release-b.yaml's directories"}},
		{name: "no Release is answered over the workspace's own stack",
			args: []string{"inspect", "demo/datastore/db"},
			code: 1,
			carries: []string{`"demo/datastore/db" matched no catalog entity of any kind`,
				workspaceconfig.CatalogDirsSetting},
			lacks: []string{"local entities for the Release"}},
		{name: "a Release that is not there is refused",
			args:    []string{"inspect", "demo/datastore/db", "--release", at("release-gone.yaml")},
			code:    1,
			carries: []string{"the Release release-gone.yaml does not exist"}},
		{name: "a file has no stack to choose and refuses the flag",
			args:    []string{"inspect", at("catalog-a/component/db.yaml"), "--release", at("release-a.yaml")},
			code:    1,
			carries: []string{"--release applies to a catalog name, and a file is read as it stands"}},
		{name: "a registry reference has no stack to choose and refuses the flag",
			args:    []string{"inspect", "oci://registry.example.com/component/demo/datastore/db:1.0.0", "--release", at("release-a.yaml")},
			code:    1,
			carries: []string{"--release applies to a catalog name, and a registry reference is read as it stands"}},
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
