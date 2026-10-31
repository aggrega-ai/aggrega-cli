// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestWithPullLibraryRemedy_NamesTheActionForThePosture drives the decoration a
// pull puts on a library discovery that found nothing, over a view whose
// artifact cache holds a library no lockfile entry pins and no catalog directory
// supplies. The line it adds must name that library and the action that works
// for the pull that failed — offline, reaching a registry; connected, authoring
// the library, since a pull that found it nowhere has nothing to record — and the
// failure must keep its condition. A failure of any other kind, and one over a
// cache holding nothing to name, is returned unchanged.
func TestWithPullLibraryRemedy_NamesTheActionForThePosture(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const library = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Sizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      type: object
`
	view := func(t *testing.T, cached bool) *catalog.LazyLookup {
		t.Helper()
		ws := t.TempDir()
		catalogDir := filepath.Join(ws, "catalog")
		if err := os.MkdirAll(catalogDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if cached {
			slot := filepath.Join(ws, cacheDir, "entities", "Sizing", "1.0.0")
			if err := os.MkdirAll(slot, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(slot, "library.yaml"), []byte(library), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		return catalog.NewLazyLookup(ws, cacheDir, []string{catalogDir}, nil)
	}
	unresolved := fmt.Errorf("schema-library discovery: %w", fmt.Errorf("%w: library %q", closure.ErrUnresolvableURI(), "Sizing"))
	otherFailure := errors.New("schema-library discovery: resolve: resolve library \"Sizing\": registry unavailable")

	tests := []struct {
		name      string
		err       error
		cached    bool
		offline   bool
		unchanged bool
		must      []string
		mustNot   []string
	}{
		{
			name:    "offline, the pull is told to run where it can reach a registry",
			err:     unresolved,
			cached:  true,
			offline: true,
			must:    []string{`SchemaLibrary "Sizing"`, "this pull runs offline", "where it can reach a registry", "author the library under a catalog directory"},
		},
		{
			name:    "connected, the pull is not sent back to itself",
			err:     unresolved,
			cached:  true,
			must:    []string{`SchemaLibrary "Sizing"`, "no catalog directory or registry this pull reads serves the library", "Author the library under a catalog directory"},
			mustNot: []string{"aggrega pull --release"},
		},
		{name: "a cache holding nothing to name leaves the failure as it was", err: unresolved, offline: true, unchanged: true},
		{name: "a failure of another kind is left as it was", err: otherFailure, cached: true, offline: true, unchanged: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := withPullLibraryRemedy(tc.err, view(t, tc.cached), tc.offline)
			if tc.unchanged {
				if got != tc.err {
					t.Errorf("the failure was decorated where there was nothing to say:\n%v", got)
				}
				return
			}
			if !errors.Is(got, closure.ErrUnresolvableURI()) {
				t.Errorf("the decorated failure no longer matches its condition: %v", got)
			}
			text := got.Error()
			if !strings.HasPrefix(text, tc.err.Error()+"\n  remediation: ") {
				t.Errorf("the failure's own text does not lead, followed by the line:\n%s", text)
			}
			for _, m := range tc.must {
				if !strings.Contains(text, m) {
					t.Errorf("the line lacks %q:\n%s", m, text)
				}
			}
			for _, m := range tc.mustNot {
				if strings.Contains(text, m) {
					t.Errorf("the line carries %q, which does not work for this pull:\n%s", m, text)
				}
			}
		})
	}
}
