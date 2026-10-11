// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// launchRootInsideAWorkspace writes a workspace whose own configuration names a
// signing key, with a folder inside it carrying a configuration file of its
// own, and returns the workspace root, the folder, and the two files.
func launchRootInsideAWorkspace(t *testing.T) (ws, sub, wsConfig, subConfig string) {
	t.Helper()
	ws = t.TempDir()
	sub = filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("create %s: %v", sub, err)
	}
	wsConfig = filepath.Join(ws, workspaceconfig.ConfigFilename)
	subConfig = filepath.Join(sub, workspaceconfig.ConfigFilename)
	for path, body := range map[string]string{
		wsConfig:  "workspace: {}\nsigning:\n  key_path: /held/key.pem\n",
		subConfig: "offline: true\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return ws, sub, wsConfig, subConfig
}

// TestALaunchRootIsTheWorkspaceRootOfEveryResolutionNamingNone drives each
// resolution this server makes over the workspace it serves, for a server
// launched with a folder inside a workspace as its root: a call's, a listing's,
// and the listener's own reading of that workspace as it starts. Each takes the
// launch root as the workspace root — that folder, reading its own
// configuration file and not the enclosing workspace's — while a call naming a
// root of its own is answered in that one.
//
// The enclosing workspace names a signing key and the folder names none, so the
// listener's reading is told apart by whether it reports a key: it must not,
// and its control, launched over the enclosing workspace itself, must.
//
// 🔴 PLANTED DEFECT: have resolutionInputs hand a call naming no root the launch
// root as where resolution begins and nothing as the root, and every row naming
// none answers for the enclosing workspace, reads its file, and reports its key.
func TestALaunchRootIsTheWorkspaceRootOfEveryResolutionNamingNone(t *testing.T) {
	t.Parallel()
	ws, sub, wsConfig, subConfig := launchRootInsideAWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: sub, transport: transportStdio}

	for _, tc := range []struct {
		name     string
		resolve  func() (*toolWorkspace, error)
		wantRoot string
		wantFile string
		notFile  string
	}{
		{
			name:     "a call naming no root",
			resolve:  func() (*toolWorkspace, error) { return s.resolveToolWorkspace("", nil) },
			wantRoot: sub, wantFile: subConfig, notFile: wsConfig,
		},
		{
			name:     "a listing naming no root",
			resolve:  func() (*toolWorkspace, error) { return s.resolveListingWorkspace("release-candidates", "", nil) },
			wantRoot: sub, wantFile: subConfig, notFile: wsConfig,
		},
		{
			name:     "a call naming its own root is answered there",
			resolve:  func() (*toolWorkspace, error) { return s.resolveToolWorkspace(ws, nil) },
			wantRoot: ws, wantFile: wsConfig, notFile: subConfig,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tw, err := tc.resolve()
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if tw.root != tc.wantRoot {
				t.Errorf("answered for %q; want %q", tw.root, tc.wantRoot)
			}
			files := tw.rw.ContributingFiles()
			if !slices.Contains(files, tc.wantFile) || slices.Contains(files, tc.notFile) {
				t.Errorf("read the configuration files %q; want %s and not %s", files, tc.wantFile, tc.notFile)
			}
		})
	}

	t.Run("the listener's reading of a signing key as it starts", func(t *testing.T) {
		t.Parallel()
		for _, row := range []struct {
			launchedWith string
			wantNote     bool
		}{
			{launchedWith: sub, wantNote: false},
			{launchedWith: ws, wantNote: true}, // the control: the enclosing workspace names a key
		} {
			var written bytes.Buffer
			noteAnUnusedSigningKey(lifecycleLog{w: &written}, row.launchedWith, workspaceconfig.FlagOverlay{}, "127.0.0.1:0")
			if got := bytes.Contains(written.Bytes(), []byte(eventSigningKeyIgnored)); got != row.wantNote {
				t.Errorf("launched with %s, the listener reported an unused signing key: %v; want %v\n%s",
					row.launchedWith, got, row.wantNote, written.String())
			}
		}
	})
}
