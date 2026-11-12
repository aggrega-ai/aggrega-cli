// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package externalinputs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestAPinnedInputIsReadOnlyFromWhereItsWriteIsHeld drives the read a render makes
// of a pinned external input through the service's one entry, Fetch, over a store
// laid out one way per row: plainly inside the workspace; behind a cache directory
// that is a link leading out; with the stored blob a link leading out; and beneath a
// cache directory the configuration names outside the workspace, plainly and with
// the blob linking out of it.
//
// A refused row is held to the read hold's condition, to the link it names, and to
// what the service does next: the refusal is not taken for a missing blob, so no
// refill is attempted through the same link — the transport-free service here would
// otherwise answer with the offline refusal instead.
func TestAPinnedInputIsReadOnlyFromWhereItsWriteIsHeld(t *testing.T) {
	const url = "https://inputs.example.invalid/data.json"
	payload := []byte(`{"pinned": true}`)
	hexDigest := payloadHex(payload)
	type place struct{ ws, cacheDir, cache, elsewhere string }
	blobAt := func(p place) string { return filepath.Join(StoreRoot(p.ws, p.cacheDir), "sha256", hexDigest) }
	write := func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, payload, 0o644); err != nil {
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
	rows := []struct {
		name     string
		cacheDir string
		layout   func(t *testing.T, p place) string
	}{
		{"control: the store inside the workspace", ".aggrega/cache", func(t *testing.T, p place) string {
			write(t, blobAt(p))
			return ""
		}},
		{"a cache directory that is a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			link(t, p.elsewhere, p.cache)
			write(t, blobAt(p))
			return p.cache
		}},
		{"the stored blob a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			write(t, filepath.Join(p.elsewhere, "blob"))
			link(t, filepath.Join(p.elsewhere, "blob"), blobAt(p))
			return blobAt(p)
		}},
		{"control: a cache directory the configuration names outside the workspace", "../shared-cache", func(t *testing.T, p place) string {
			write(t, blobAt(p))
			return ""
		}},
		{"the blob linking out of a cache directory named outside the workspace", "../shared-cache", func(t *testing.T, p place) string {
			write(t, filepath.Join(p.elsewhere, "blob"))
			link(t, filepath.Join(p.elsewhere, "blob"), blobAt(p))
			return blobAt(p)
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
			lf := &lockfile.Lockfile{ExternalInputs: []lockfile.ExternalInput{{URL: url, Sha256: hexDigest, Size: int64(len(payload))}}}

			got, err := NewFrozen(ws, row.cacheDir, lf).Fetch(context.Background(), url, "")
			if want == "" {
				if err != nil {
					t.Fatalf("refused a blob its root holds: %v", err)
				}
				if string(got) != string(payload) {
					t.Errorf("served %q, want the pinned %q", got, payload)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read hold's refusal, got %v", err)
			}
			if errors.Is(err, egress.ErrOfflineEgress()) || errors.Is(err, ErrStoreCorrupt()) {
				t.Errorf("the refusal must be returned as itself, not as a refill or a corrupt store: %v", err)
			}
			if got != nil {
				t.Errorf("a refused read served %q", got)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name the link %s: %v", want, err)
			}
		})
	}
}
