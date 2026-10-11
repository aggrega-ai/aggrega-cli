// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// countingChartArchiveServer serves a minimal, deterministic Helm chart archive at
// every path and counts the requests it answers, so a row can say whether the pin
// step fetched its Recipe's source.
func countingChartArchiveServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{
		{"pinchart/Chart.yaml", "apiVersion: v2\nname: pinchart\nversion: 1.0.0\n"},
		{"pinchart/templates/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-pin\n"},
	} {
		hdr := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	blob := buf.Bytes()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(blob)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestThePinToolIsRefusedInAFolderNoWorkspaceResolvesFrom calls the pin tool on a
// server started, with no launch root, in a folder that holds a Recipe whose
// remote source the counting chart server serves — once where nobody marked the
// folder and the call names no root, and once in the same layout marked as a
// workspace.
//
// Marked, the step fetches the source, caches it under the folder and writes the
// lock. Unmarked, the call is refused before anything is resolved, with the
// refusal the command line gives naming the folder: the server answers nothing,
// no lock is written and no .aggrega appears.
//
// 🔴 PLANTED DEFECT: delete the CacheWriteRefusal block in the pin step's
// planNamed, and the unmarked row fails — the call completes, fetches the source
// and creates the folder's .aggrega directory.
//
// Not parallel: the server resolves from the process working directory, and the
// rows set the offline posture's variable so the marked row may reach the server.
func TestThePinToolIsRefusedInAFolderNoWorkspaceResolvesFrom(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marked bool
	}{
		{name: "a folder nobody marked and nothing named"},
		{name: "the same layout marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, hits := countingChartArchiveServer(t)
			folder := aDirectoryNoWorkspaceResolvesFrom(t)
			entity := filepath.Join("catalog", "recipe", "helm", "server", "1.0.0", "recipe.yaml")
			writeNoticeFixture(t, filepath.Join(folder, entity), "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\n"+
				"metadata:\n  name: server\n  version: \"1.0.0\"\nspec:\n  capability: helm\n  implements: server\n"+
				"  source:\n    kind: helm-https-chart\n    url: "+srv.URL+"/pinchart.tgz\n    version: \"1.0.0\"\n")
			if tc.marked {
				writeNoticeFixture(t, filepath.Join(folder, workspaceconfig.ConfigFilename), "workspace: {}\n")
			}
			t.Setenv("AGGREGA_OFFLINE", "false")
			t.Chdir(folder)
			rw, err := workspaceconfig.Resolve(folder, "")
			if err != nil {
				t.Fatalf("resolve %s: %v", folder, err)
			}
			if rw.FoundNoWorkspace() == tc.marked {
				t.Fatalf("precondition: %s resolves as found-nothing=%v (marker %v), the row needs %v",
					folder, rw.FoundNoWorkspace(), rw.Marker(), !tc.marked)
			}

			res := callTool(t, unlaunchedServer(""), "pin", pinArgs{Entity: entity})

			if tc.marked {
				if res.IsError {
					t.Fatalf("the pin tool in a workspace was refused, so the refused row proves nothing: %s", res.Content[0].Text)
				}
				if hits.Load() == 0 {
					t.Fatal("the pin tool in a workspace fetched nothing, so the refused row's empty fetch count proves nothing")
				}
				if _, serr := os.Stat(filepath.Join(folder, ".aggrega")); serr != nil {
					t.Fatalf("the pin tool in a workspace left no artifact cache under it (%v), so the refused row's missing cache proves nothing", serr)
				}
				return
			}
			want := rw.CacheWriteRefusal()
			if !res.IsError {
				t.Fatalf("the pin tool in a folder that is no workspace completed: %s", res.Content[0].Text)
			}
			if text := res.Content[0].Text; want == nil || !strings.Contains(text, want.Error()) {
				t.Errorf("the refusal does not carry the cache refusal\nrefusal:\n%s\nwant it to carry:\n%v", text, want)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("the refused call fetched the source %d time(s)", n)
			}
			if _, serr := os.Stat(recipepin.LockPath(filepath.Join(folder, entity))); !errors.Is(serr, fs.ErrNotExist) {
				t.Errorf("the refused call wrote the lock (stat: %v)", serr)
			}
			if _, serr := os.Stat(filepath.Join(folder, ".aggrega")); !errors.Is(serr, fs.ErrNotExist) {
				t.Errorf("%s/.aggrega exists after the refused call (stat: %v)", folder, serr)
			}
		})
	}
}
