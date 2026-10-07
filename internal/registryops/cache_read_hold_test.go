// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestARefreshReusesNoRecordTheReadHoldRefuses drives the read a registry update
// makes of the index it is about to refresh, over an index that is not built, one
// built inside the workspace, and one behind a cache directory that is a link
// leading out. The first refreshes in full, the second offers its records for reuse,
// and the third is refused with the read hold, naming the link: a record reused from
// it would carry whatever the link leads to into the index the refresh writes.
func TestARefreshReusesNoRecordTheReadHoldRefuses(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const host = "registry.example.invalid"
	idx := HostIndex{Host: host, Records: []registryindex.Record{{
		URI: "oci://" + host + "/component/core/api", Name: "component/core/api", Version: "1.0.0", Kind: "Component",
	}}}
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rows := []struct {
		name   string
		layout func(t *testing.T, ws, elsewhere string) string
		want   int
	}{
		{name: "control: no index built", layout: func(*testing.T, string, string) string { return "" }},
		{name: "control: an index inside the workspace", want: 1, layout: func(t *testing.T, ws, _ string) string {
			if err := WriteHostIndex(ws, cacheDir, idx, stamp); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
		{name: "an index behind a cache directory that is a link leading out", layout: func(t *testing.T, ws, elsewhere string) string {
			if err := WriteHostIndex(elsewhere, ".", idx, stamp); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(ws, ".aggrega"), 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(ws, cacheDir)
			if err := os.Symlink(elsewhere, link); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			return link
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, elsewhere := t.TempDir(), t.TempDir()
			link := row.layout(t, ws, elsewhere)

			prior, err := loadPriorRecords(ws, cacheDir, host)
			if link == "" {
				if err != nil {
					t.Fatalf("refused an index the workspace holds: %v", err)
				}
				if len(prior) != row.want {
					t.Errorf("offered %d records for reuse, want %d", len(prior), row.want)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read hold's refusal, got %v", err)
			}
			if prior != nil {
				t.Errorf("a refused read offered %d records for reuse", len(prior))
			}
			if !strings.Contains(err.Error(), link) {
				t.Errorf("the refusal does not name the link %s: %v", link, err)
			}
		})
	}
}
