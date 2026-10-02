// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
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
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "release-entity-check")
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
