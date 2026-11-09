// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

const invalidationComponentYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/x
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

// newInvalidationServer builds a server whose lazy catalog walks tmp, seeds a
// core/x Component file there, and resolves it once so the lazy hot cache holds
// it. It returns the server and the component's on-disk path. The workspace-reset
// branch of handleInvalidate is then observable behaviorally: after the file is
// removed, a Component("core/x") lookup HITS the cache if the branch was skipped
// (granularity=file) and MISSES if the branch fired (Reset re-walks the index).
func newInvalidationServer(t *testing.T) (*Server, string) {
	t.Helper()
	tmp := t.TempDir()
	compPath := filepath.Join(tmp, "comp.yaml")
	if err := os.WriteFile(compPath, []byte(invalidationComponentYAML), 0o644); err != nil {
		t.Fatalf("write component fixture: %v", err)
	}
	srv := newTestServer(t, tmp, nil, nil)
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{tmp}, nil)
	if _, ok := srv.lazyCatalog.Component("core/x"); !ok {
		t.Fatalf("setup: core/x should resolve before invalidation")
	}
	return srv, compPath
}

// TestInvalidateFileGranularityPreservesWorkspaceCache covers the per-file
// granularity branch: aggrega/invalidate with granularity="file" drops the
// per-file parse + diagnostic caches for the named file but MUST NOT reset the
// workspace-level lazy catalog. The preserved lazy cache is verified behaviorally:
// core/x still resolves from the hot cache after its file is removed.
func TestInvalidateFileGranularityPreservesWorkspaceCache(t *testing.T) {
	t.Parallel()
	srv, compPath := newInvalidationServer(t)
	srv.parseCache.Put("/file.yaml", &yaml.Node{}, []byte(""))
	srv.setDiagnostics("/file.yaml", []Diagnostic{
		{Line: 1, Column: 1, Severity: "error", Message: "stale"},
	})
	if err := os.Remove(compPath); err != nil {
		t.Fatalf("remove component fixture: %v", err)
	}

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{
		File:        "/file.yaml",
		Granularity: "file",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("invalidate returned error: %+v", resp.Error)
	}

	// Per-file drops fired.
	if _, ok := srv.parseCache.Get("/file.yaml"); ok {
		t.Errorf("parseCache entry for /file.yaml should be dropped")
	}
	if srv.snapshotDiagnostics("/file.yaml") != nil {
		t.Errorf("diagnostics entry for /file.yaml should be dropped")
	}

	// Workspace branch skipped: the lazy catalog is NOT reset, so core/x still
	// resolves from the hot cache despite its file being gone.
	if _, ok := srv.lazyCatalog.Component("core/x"); !ok {
		t.Errorf("workspace lazy catalog should be preserved under granularity=file; core/x no longer resolves")
	}
}

// assertWorkspaceReset drives an aggrega/invalidate with the given granularity
// and asserts the workspace-reset branch fired: after the component file is
// removed and the lazy catalog reset, core/x no longer resolves (the index
// re-walked and found nothing).
func assertWorkspaceReset(t *testing.T, granularity string) {
	t.Helper()
	srv, compPath := newInvalidationServer(t)
	if err := os.Remove(compPath); err != nil {
		t.Fatalf("remove component fixture: %v", err)
	}

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{
		File:        "/file.yaml",
		Granularity: granularity,
	}))
	if resp := runOneRequest(t, srv, &in, &out); resp.Error != nil {
		t.Fatalf("invalidate returned error: %+v", resp.Error)
	}

	if _, ok := srv.lazyCatalog.Component("core/x"); ok {
		t.Errorf("workspace lazy catalog should be reset under granularity=%q; core/x still resolves after its file was removed", granularity)
	}
}

// TestInvalidateWorkspaceGranularityClearsAll verifies an explicit
// granularity="workspace" resets the workspace-level lazy catalog.
func TestInvalidateWorkspaceGranularityClearsAll(t *testing.T) {
	t.Parallel()
	assertWorkspaceReset(t, "workspace")
}

// TestInvalidateDefaultGranularityClearsAll verifies the backward-compat default:
// a missing Granularity field behaves like "workspace".
func TestInvalidateDefaultGranularityClearsAll(t *testing.T) {
	t.Parallel()
	assertWorkspaceReset(t, "")
}

// TestInvalidateUnknownGranularityFallsThroughToWorkspace covers the
// forward-compatibility hatch: any unrecognized granularity (including the
// reserved "entity" value and any free-form string) behaves like "workspace".
// "WORKSPACE" exercises the case-sensitivity contract — only the lowercase
// canonical "file" carves out.
func TestInvalidateUnknownGranularityFallsThroughToWorkspace(t *testing.T) {
	t.Parallel()
	for _, granularity := range []string{"entity", "gibberish", "WORKSPACE"} {
		granularity := granularity
		t.Run(granularity, func(t *testing.T) {
			t.Parallel()
			assertWorkspaceReset(t, granularity)
		})
	}
}
