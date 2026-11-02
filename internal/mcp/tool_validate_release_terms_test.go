// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"fmt"
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
// catalog directory, so a call naming no Release reads no catalog directory at
// all.
func releaseEntityCheckWorkspace(t *testing.T) string {
	t.Helper()
	return entityCheckWorkspaceWith(t, "workspace:\n  releases:\n    release-sized.yaml:\n      catalog_dirs:\n        - variant-sized\n"+
		"    release-plain.yaml:\n      catalog_dirs:\n        - variant-plain\n")
}

// entityCheckWorkspaceWith copies the same fixture under the workspace
// configuration given and returns its root.
func entityCheckWorkspaceWith(t *testing.T, config string) string {
	t.Helper()
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "release-entity-check")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// TestValidateChecksAnEntityInTheTermsOfTheReleaseNamed is the tool protocol's
// half of the one-entity proof: the verdicts the command line gives for the same
// fixture, read off the structured payload.
//
// The subject is a Component whose pillar refers to a library only one variant
// carries, so whether the reference resolves is decided by which stack the check
// reads and by nothing else. The row naming no Release is the answer before a
// Release could be sent beside an entity, and the row naming the other Release is
// its control. The refusal rows each carry the one fault of the Release argument.
func TestValidateChecksAnEntityInTheTermsOfTheReleaseNamed(t *testing.T) {
	t.Parallel()
	root := releaseEntityCheckWorkspace(t)
	const (
		sized          = "variant-sized/component/db.yaml"
		libraryMissing = "SchemaLibrary Sizing not found in catalog"
	)
	cases := []struct {
		name string
		args validateArgs
		// valid is the verdict a call that ran must report, and refusal the phrases
		// a call refused before the check ran must carry instead.
		valid   bool
		finding string
		release string
		refusal []string
	}{
		{name: "no Release reads the workspace's own stack, which carries no library",
			args: validateArgs{Entity: sized}, finding: libraryMissing},
		{name: "the Release whose directories carry the library resolves it",
			args:  validateArgs{Entity: sized, Release: "release-sized.yaml"},
			valid: true, release: "release-sized.yaml"},
		{name: "a Release whose directories do not carry the library fails naming it",
			args:    validateArgs{Entity: sized, Release: "release-plain.yaml"},
			finding: libraryMissing, release: "release-plain.yaml"},
		{name: "catalog_dirs is prepended to the Release's stack for one call",
			args:  validateArgs{Entity: sized, Release: "release-plain.yaml", CatalogDirs: []string{filepath.Join(root, "variant-sized")}},
			valid: true, release: "release-plain.yaml"},
		{name: "a Release that is not there is refused",
			args:    validateArgs{Entity: sized, Release: "release-gone.yaml"},
			refusal: []string{"validate's 'release' names no Release", "does not exist"}},
		{name: "a directory is refused",
			args:    validateArgs{Entity: sized, Release: "variant-sized"},
			refusal: []string{"validate's 'release' names no Release", "is a directory"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := tc.args
			args.WorkspaceRoot = root
			res := callTool(t, newTestServer(t), "validate", args)
			if len(tc.refusal) > 0 {
				if !res.IsError {
					t.Fatalf("validate answered; want a refusal carrying %v", tc.refusal)
				}
				for _, phrase := range tc.refusal {
					if !strings.Contains(res.Content[0].Text, phrase) {
						t.Errorf("refusal %q does not carry %q", res.Content[0].Text, phrase)
					}
				}
				return
			}
			if res.IsError {
				t.Fatalf("validate was refused: %s", res.Content[0].Text)
			}
			// Over the process's own streams a location is told as the call's
			// argument resolved against the workspace root, so each is read by the
			// name it ends in rather than against a spelling of the root this test
			// does not control.
			payload := unmarshalValidatePayload(t, res)
			if payload.Scope != validateScopeEntity {
				t.Errorf("scope = %q, want %q", payload.Scope, validateScopeEntity)
			}
			if !strings.HasSuffix(told(payload.Target), string(filepath.Separator)+filepath.FromSlash(sized)) {
				t.Errorf("target = %q, want the entity %q", told(payload.Target), sized)
			}
			switch {
			case tc.release == "" && payload.Release != nil:
				t.Errorf("release = %q, want none on a check naming no Release", *payload.Release)
			case tc.release != "" && !strings.HasSuffix(told(payload.Release), string(filepath.Separator)+tc.release):
				t.Errorf("release = %q, want the Release %q", told(payload.Release), tc.release)
			}
			if payload.Valid != tc.valid {
				t.Fatalf("valid = %v, want %v; diagnostics %v", payload.Valid, tc.valid, payload.Diagnostics)
			}
			if tc.finding != "" && (len(payload.Diagnostics) != 1 || !strings.Contains(payload.Diagnostics[0], tc.finding)) {
				t.Errorf("diagnostics = %v, want one carrying %q", payload.Diagnostics, tc.finding)
			}
			if tc.valid && len(payload.Diagnostics) != 0 {
				t.Errorf("a valid answer carries diagnostics %v", payload.Diagnostics)
			}
		})
	}
}

// TestValidateRefusesAListedCatalogDirectoryWhereNothingExists is the tool
// protocol's half of the proof `aggrega validate` gives: what one entity's check
// answers when a catalog directory its stack reads is listed and nothing exists
// there.
//
// The call is refused before the check reads anything, and its text carries the
// resolver's refusal whole — the entry as written, the path it resolved to, the
// configuration file and line listing it and the setting — so no verdict over
// fewer catalogs than the configuration lists is given.
//
// The first row is the control: every listed directory is there and the check
// answers valid. Each row after it carries one listing where nothing exists,
// somewhere different; the last checks the entity in no Release's terms, over the
// workspace's own stack.
func TestValidateRefusesAListedCatalogDirectoryWhereNothingExists(t *testing.T) {
	t.Parallel()
	const sized = "variant-sized/component/db.yaml"
	cases := []struct {
		name    string
		config  string
		release string
		// absent is the listed directory where nothing exists, empty for the
		// control; line is its line in config and setting the list it sits in.
		absent  string
		line    int
		setting string
	}{
		{name: "control: every directory the Release reads exists, and the check answers valid",
			config:  "workspace:\n  catalog_dirs:\n    - variant-sized\n",
			release: "release-sized.yaml"},
		{name: "a directory the workspace's list names where nothing exists is refused",
			config:  "workspace:\n  catalog_dirs:\n    - variant-sized\n    - no-such-catalog\n",
			release: "release-sized.yaml", absent: "no-such-catalog", line: 4, setting: "`workspace.catalog_dirs`"},
		{name: "a directory the Release's own list names where nothing exists is refused",
			config:  "workspace:\n  releases:\n    release-sized.yaml:\n      catalog_dirs:\n        - variant-sized\n        - gone-for-sized\n",
			release: "release-sized.yaml", absent: "gone-for-sized", line: 6,
			setting: "`catalog_dirs` of the `workspace.releases` entry \"release-sized.yaml\""},
		{name: "an entity checked in no Release's terms is refused over a directory the workspace's list names where nothing exists",
			config: "workspace:\n  catalog_dirs:\n    - variant-sized\n    - no-such-catalog\n",
			absent: "no-such-catalog", line: 4, setting: "`workspace.catalog_dirs`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := entityCheckWorkspaceWith(t, tc.config)
			s := newTestServer(t)
			res := callTool(t, s, "validate", validateArgs{Entity: sized, Release: tc.release, WorkspaceRoot: root})
			if tc.absent == "" {
				if res.IsError {
					t.Fatalf("validate was refused: %s", res.Content[0].Text)
				}
				if payload := unmarshalValidatePayload(t, res); !payload.Valid {
					t.Fatalf("valid = false over a stack whose directories are all there; diagnostics %v", payload.Diagnostics)
				}
				return
			}
			want := fmt.Sprintf("%s:%d: the %s entry %q names %s, which does not exist; create that directory, or remove the entry: %v",
				filepath.Join(root, workspaceconfig.ConfigFilename), tc.line, tc.setting, tc.absent, filepath.Join(root, tc.absent),
				workspaceconfig.ErrConfigInvalid())
			if !res.IsError {
				t.Fatalf("validate answered over a listed directory where nothing exists: %s", res.Content[0].Text)
			}
			if text := res.Content[0].Text; !strings.Contains(text, want) {
				t.Errorf("the refusal does not carry the resolver's refusal\n%s\nin:\n%s", want, text)
			}
		})
	}
}
