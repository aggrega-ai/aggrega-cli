// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAHostIndexIsReadOnlyFromWhereItsWriteIsHeld drives the one read of a registry
// host's local index, and the withdrawal screen a pull builds from it, over an index
// laid out one way per row: plainly inside the workspace; not built at all; behind a
// cache directory that is a link leading out; with the host's directory, a record or
// the timestamp a link leading out; and beneath a cache directory the configuration
// names outside the workspace, plainly and with a record linking out of it.
//
// A refused row is held to the read hold's condition and to the link it names, and
// it is neither an unbuilt index nor a read fault: the screen returns it rather than
// leaving the pull unscreened over an index it may not read.
func TestAHostIndexIsReadOnlyFromWhereItsWriteIsHeld(t *testing.T) {
	const host = "registry.example.invalid"
	record, err := json.Marshal(Record{URI: "oci://" + host + "/component/core/api", Name: "component/core/api", Version: "1.0.0", Kind: "Component"})
	if err != nil {
		t.Fatal(err)
	}
	const stamp = "2026-01-02T03:04:05Z\n"
	type place struct{ ws, cacheDir, cache, elsewhere string }
	put := func(t *testing.T, path string, body []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	base := func(p place) string { return oci.IndexBasePath(p.ws, p.cacheDir, host) }
	recordAt := func(p place) string { return filepath.Join(base(p), "component%2Fcore%2Fapi.json") }
	build := func(t *testing.T, p place) {
		t.Helper()
		put(t, recordAt(p), record)
		put(t, base(p)+".updated", []byte(stamp))
	}
	rows := []struct {
		name     string
		cacheDir string
		layout   func(t *testing.T, p place) string
		unbuilt  bool
	}{
		{name: "control: the index inside the workspace", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			build(t, p)
			return ""
		}},
		{name: "control: no index built", cacheDir: ".aggrega/cache", unbuilt: true, layout: func(*testing.T, place) string { return "" }},
		{name: "a cache directory that is a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			link(t, p.elsewhere, p.cache)
			build(t, p)
			return p.cache
		}},
		{name: "the host's directory a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			put(t, filepath.Join(p.elsewhere, "component%2Fcore%2Fapi.json"), record)
			link(t, p.elsewhere, base(p))
			put(t, base(p)+".updated", []byte(stamp))
			return base(p)
		}},
		{name: "a record a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			build(t, p)
			if err := os.Remove(recordAt(p)); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(p.elsewhere, "record.json"), record)
			link(t, filepath.Join(p.elsewhere, "record.json"), recordAt(p))
			return recordAt(p)
		}},
		{name: "the timestamp a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			put(t, recordAt(p), record)
			put(t, filepath.Join(p.elsewhere, "updated"), []byte(stamp))
			link(t, filepath.Join(p.elsewhere, "updated"), base(p)+".updated")
			return base(p) + ".updated"
		}},
		{name: "control: a cache directory the configuration names outside the workspace", cacheDir: "../shared-cache", layout: func(t *testing.T, p place) string {
			build(t, p)
			return ""
		}},
		{name: "a record linking out of a cache directory named outside the workspace", cacheDir: "../shared-cache", layout: func(t *testing.T, p place) string {
			put(t, base(p)+".updated", []byte(stamp))
			put(t, filepath.Join(p.elsewhere, "record.json"), record)
			link(t, filepath.Join(p.elsewhere, "record.json"), recordAt(p))
			return recordAt(p)
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			p := place{ws: ws, cacheDir: row.cacheDir, cache: filepath.Join(ws, row.cacheDir), elsewhere: t.TempDir()}
			want := row.layout(t, p)

			loaded, err := LoadHost(ws, row.cacheDir, host)
			switch {
			case row.unbuilt:
				if !IsNotBuilt(err) {
					t.Fatalf("an index never built must read as unbuilt; got %v", err)
				}
			case want == "":
				if err != nil {
					t.Fatalf("refused an index its root holds: %v", err)
				}
				if len(loaded.Records) != 1 || !loaded.HasTimestamp {
					t.Errorf("loaded %d records (timestamp %v), want the one record and its timestamp", len(loaded.Records), loaded.HasTimestamp)
				}
			default:
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("want the read hold's refusal, got %v", err)
				}
				if IsNotBuilt(err) {
					t.Errorf("the refusal must not read as an unbuilt index: %v", err)
				}
				if len(loaded.Records) != 0 {
					t.Errorf("a refused read handed back %d records", len(loaded.Records))
				}
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name the link %s: %v", want, err)
				}
			}
		})
	}
}

// TestAWithdrawalScreenHaltsOnAnIndexTheReadHoldRefuses drives the screen a pull and
// a plugin addition build, over an index that is not built, one built inside the
// workspace, and one behind a cache directory that is a link leading out. The first
// two leave selection to the screen they build — none, where nothing verified is
// recorded — and the third returns the read hold rather than an unscreened
// selection.
func TestAWithdrawalScreenHaltsOnAnIndexTheReadHoldRefuses(t *testing.T) {
	const host = "registry.example.invalid"
	rows := []struct {
		name    string
		layout  func(t *testing.T, ws, elsewhere string)
		refused bool
	}{
		{name: "control: no index built", layout: func(*testing.T, string, string) {}},
		{name: "control: an index inside the workspace", layout: func(t *testing.T, ws, _ string) {
			dir := oci.IndexBasePath(ws, ".aggrega/cache", host)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "an index behind a cache directory that is a link leading out", refused: true, layout: func(t *testing.T, ws, elsewhere string) {
			if err := os.MkdirAll(filepath.Join(elsewhere, "index", host), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(ws, ".aggrega"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, filepath.Join(ws, ".aggrega", "cache")); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, elsewhere := t.TempDir(), t.TempDir()
			row.layout(t, ws, elsewhere)
			rw, err := workspaceconfig.Resolve(ws, ws, func(string) string { return "" })
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			var out bytes.Buffer
			screen, err := NewScreen(rw, host, &out)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused an index its root holds: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read hold's refusal, got %v", err)
			}
			if screen != nil {
				t.Errorf("a refused index formed a screen: %v", screen)
			}
		})
	}
}
