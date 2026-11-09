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

// The two phrases the workspace's account of where it reads local entities is
// recognised by: the sentence it always opens with, and the clause only its
// counting branch writes.
const (
	adviceOpening = "local entities are read from"
	adviceCount   = "declaring an entity kind"
)

// rootEntityDocument and rootForeignDocument are what a workspace root carries
// in the two shapes that decide whether a count is reported: a document
// declaring an entity kind, and one declaring nothing this product reads.
const (
	rootEntityDocument  = "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: loose/at-the-root\n"
	rootForeignDocument = "services:\n  web:\n    image: nginx\n"
)

// adviceWorkspaceRoot authors a workspace root: the config body verbatim, the
// named documents at the root itself, and the named documents inside the
// conventional catalog directory. It returns the root.
//
// The config body is written rather than assembled through the shared options
// helper because the shape under test IS the config text — an absent key and an
// authored empty list are different documents.
func adviceWorkspaceRoot(t *testing.T, config string, atRoot, inCatalog map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	for name, body := range atRoot {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s at the root: %v", name, err)
		}
	}
	if len(inCatalog) > 0 {
		dir := filepath.Join(root, workspaceconfig.ConventionalCatalogDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir the conventional catalog directory: %v", err)
		}
		for name, body := range inCatalog {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatalf("write %s under the conventional directory: %v", name, err)
			}
		}
	}
	return root
}

// TestByNameLookupCarriesTheLocalCatalogAccount drives `aggrega inspect` through
// the real command tree and reads every sink a run can speak through: cobra's
// two writers, the process error stream, and the status.
//
// The process error stream is read because that is where the one formatted
// diagnostic lands. Cobra's own error printing is silenced on the root, so a
// failed run reaches the reader through executeRoot, which writes to the process
// stream rather than to the writer the command was handed. Reading the command's
// writers alone would put every assertion here on a sink the account is never
// written to — the rows expecting the account would fail against an empty
// buffer, and, worse, the rows expecting none would pass against the same empty
// buffer while proving nothing.
//
// It is the axis the workspace's own tests structurally cannot reach: whether
// the account is attached to a lookup that FAILED and withheld from one that
// succeeded. The workspace composes the account with no knowledge of any lookup,
// so only a surface that performs one can hold it to firing on a failure.
//
// 🔴 The fourth row is the positive control for that axis, and without it the
// other three prove nothing. An account that were attached unconditionally would
// satisfy the first three exactly as one attached to the failure does — and an
// account that were never attached at all would satisfy the third and the fourth.
// The rows have to disagree with each other for any of them to mean anything.
//
// The status is read on every row because the account is a note and not a
// verdict: the miss decides the status, and a row that gained one would be
// reporting that appending text changed what the run decided.
func TestByNameLookupCarriesTheLocalCatalogAccount(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr,
	// and the environment seams below are process-global too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cases := []struct {
		name      string
		config    string
		atRoot    map[string]string
		inCatalog map[string]string
		// identifier is what `aggrega inspect` is asked for.
		identifier string
		wantCode   int
		wantNote   bool
		// wantCount is the rendered count, "" when no count is expected.
		wantCount string
	}{
		{
			name:   "a miss, with entity documents loose at the root",
			config: "workspace: {}\n",
			atRoot: map[string]string{
				"component.yaml": rootEntityDocument,
				"second.yaml":    strings.Replace(rootEntityDocument, "loose/at-the-root", "loose/second", 1),
			},
			identifier: "loose/at-the-root",
			wantCode:   1,
			wantNote:   true,
			wantCount:  "2 documents",
		},
		{
			name:       "a miss, with only documents this product does not read at the root",
			config:     "workspace: {}\n",
			atRoot:     map[string]string{"docker-compose.yaml": rootForeignDocument},
			identifier: "no-such-entity",
			wantCode:   1,
			wantNote:   true,
			wantCount:  "",
		},
		{
			name:       "a miss, in a workspace declaring it reads no local catalog",
			config:     "workspace:\n  catalog_dirs: []\n",
			atRoot:     map[string]string{"component.yaml": rootEntityDocument},
			identifier: "loose/at-the-root",
			wantCode:   1,
			wantNote:   false,
		},
		{
			name:       "a lookup that resolved, from the conventional directory",
			config:     "workspace: {}\n",
			inCatalog:  map[string]string{"component.yaml": bareNameComponentFixture},
			identifier: "core/api-server",
			wantCode:   0,
			wantNote:   false,
		},
		{
			name:       "a miss in a workspace whose catalog directory does hold entities",
			config:     "workspace: {}\n",
			inCatalog:  map[string]string{"component.yaml": bareNameComponentFixture},
			identifier: "no-such-entity",
			wantCode:   1,
			wantNote:   true,
			wantCount:  "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			root := adviceWorkspaceRoot(t, tc.config, tc.atRoot, tc.inCatalog)

			cmd := newRootCmd()
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetIn(strings.NewReader(""))
			cmd.SetArgs([]string{"--workspace-root", root, "inspect", tc.identifier})

			var code int
			process := captureStderr(t, func() {
				code = executeRoot(context.Background(), cmd)
			})
			streams := out.String() + errOut.String() + process
			if code != tc.wantCode {
				t.Fatalf("`aggrega inspect %s` exited %d; want %d\nstdout:\n%s\ncommand error writer:\n%s\nprocess error stream:\n%s",
					tc.identifier, code, tc.wantCode, out.String(), errOut.String(), process)
			}
			if !tc.wantNote {
				if strings.Contains(streams, adviceOpening) {
					t.Fatalf("`aggrega inspect %s` carried the local-catalog account:\n%s", tc.identifier, streams)
				}
				return
			}
			if !strings.Contains(streams, adviceOpening) {
				t.Fatalf("`aggrega inspect %s` failed without saying where local entities are read from:\n%s",
					tc.identifier, streams)
			}
			if !strings.Contains(streams, "`"+workspaceconfig.ConventionalCatalogDir+"/`") {
				t.Errorf("the account does not name `%s/`:\n%s", workspaceconfig.ConventionalCatalogDir, streams)
			}
			if !strings.Contains(streams, workspaceconfig.CatalogDirsSetting) {
				t.Errorf("the account does not name %s:\n%s", workspaceconfig.CatalogDirsSetting, streams)
			}
			switch tc.wantCount {
			case "":
				if strings.Contains(streams, adviceCount) {
					t.Errorf("the account counts documents at the root; this root declares no entity kind:\n%s", streams)
				}
			default:
				if !strings.Contains(streams, "holds "+tc.wantCount+" "+adviceCount) {
					t.Errorf("the account does not report %q at the root:\n%s", tc.wantCount, streams)
				}
			}
		})
	}
}
