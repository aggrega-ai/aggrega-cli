// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAGraphOverALockfileLeadingOutIsRefusedNamingTheLink drives aggrega/irGraph
// through the production dispatch over a workspace whose Release's lockfile is,
// one row at a time, absent, a link to a lockfile elsewhere inside the workspace,
// and a link to a lockfile outside it.
//
// The lockfile gate every request reading a Release's pins crosses refuses the
// link leading out before anything is read — with the code a present but
// unreadable lockfile answers, the read condition's short code ahead of its own
// sentence, the link and where it leads named, and the lockfile and the Release in
// the data — so the graph is neither drawn from pins read elsewhere nor drawn
// without them. The absent row is the gate's own answer for a missing lockfile,
// and the inside row passes the gate: neither is refused for a link.
func TestAGraphOverALockfileLeadingOutIsRefusedNamingTheLink(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name     string
		arrange  func(t *testing.T, ws, outside string) (leadsTo string)
		wantCode int
		refused  bool
	}{
		{name: "control: no lockfile is the gate's own refusal for absence", wantCode: errLockfileAbsent,
			arrange: func(*testing.T, string, string) string { return "" }},
		{name: "a link to a lockfile elsewhere inside the workspace passes the gate",
			arrange: func(t *testing.T, ws, _ string) string {
				kept := filepath.Join(ws, "kept", "release.lock")
				writeFixtureFile(t, kept, "version: 1\n")
				if err := os.Symlink(kept, filepath.Join(ws, "release.lock")); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
				return ""
			}},
		{name: "a link to a lockfile outside the workspace is refused", wantCode: errEntityNotResolvable, refused: true,
			arrange: func(t *testing.T, ws, outside string) string {
				kept := filepath.Join(outside, "release.lock")
				writeFixtureFile(t, kept, "version: 1\n")
				if err := os.Symlink(kept, filepath.Join(ws, "release.lock")); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
				return kept
			}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws, outside := t.TempDir(), t.TempDir()
			writeFixtureFile(t, filepath.Join(ws, "release.yaml"), "kind: Release\n")
			writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace: {}\n")
			leadsTo := row.arrange(t, ws, outside)
			before, _ := os.ReadFile(filepath.Join(outside, "release.lock"))

			var out bytes.Buffer
			srv := catalogDirsServer(t, ws, &out)
			resp := irGraphAnswer(t, srv, &out, "release.yaml")

			if !row.refused {
				if resp.Error != nil && strings.Contains(resp.Error.Message, "E0321") {
					t.Fatalf("refused a lockfile the workspace holds: %s", resp.Error.Message)
				}
				if row.wantCode != 0 && (resp.Error == nil || resp.Error.Code != row.wantCode) {
					t.Fatalf("want code %d, got %+v", row.wantCode, resp.Error)
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("answered over a lockfile leading out of the workspace: %s", resp.Result)
			}
			if resp.Error.Code != row.wantCode {
				t.Errorf("code %d; want %d", resp.Error.Code, row.wantCode)
			}
			lock := filepath.Join(ws, "release.lock")
			if !strings.HasPrefix(resp.Error.Message, "E0321: ") ||
				!strings.Contains(resp.Error.Message, "the link "+lock+" leads to "+leadsTo) {
				t.Errorf("the refusal does not lead with the short code and name the link and where it leads: %s", resp.Error.Message)
			}
			if resp.Error.Data == nil || resp.Error.Data.Lockfile != lock || resp.Error.Data.Release != "release.yaml" {
				t.Errorf("the refusal's data does not name the lockfile and the Release: %+v", resp.Error.Data)
			}
			if after, _ := os.ReadFile(filepath.Join(outside, "release.lock")); !bytes.Equal(after, before) {
				t.Error("the lockfile outside the workspace was changed")
			}
		})
	}
}
