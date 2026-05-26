// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestInvalidateFileGranularityPreservesWorkspaceCache covers the
// per-file granularity branch: aggrega/invalidate with
// granularity="file" must drop the per-file parse + diagnostic
// caches for the named file but MUST NOT touch the workspace-level
// resolved-catalog cache.
//
// The workspace branch's preservation is verified by sentinel-
// identity check on recipeCatalog. The two workspace-reset lines
// (s.recipeCatalog = nil and s.lazyCatalog.Reset()) live on the
// same branch in handleInvalidate, so recipeCatalog's preserved
// identity is sufficient evidence that the branch was skipped. If
// a future refactor splits those lines onto separate branches,
// this test should be revisited to assert lazyCatalog's preserved
// state directly.
func TestInvalidateFileGranularityPreservesWorkspaceCache(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)

	sentinel := &catalog.Catalog{}
	srv.recipeCatalog = sentinel
	srv.parseCache.Put("/file.yaml", &yaml.Node{}, []byte(""))
	srv.diagnostics["/file.yaml"] = []Diagnostic{
		{Line: 1, Column: 1, Severity: "error", Message: "stale"},
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
	if _, ok := srv.diagnostics["/file.yaml"]; ok {
		t.Errorf("diagnostics entry for /file.yaml should be dropped")
	}

	// Workspace branch skipped: sentinel identity preserved.
	if srv.recipeCatalog != sentinel {
		t.Errorf("workspace recipeCatalog should be preserved under granularity=file; got %v", srv.recipeCatalog)
	}
}

// TestInvalidateWorkspaceGranularityClearsAll verifies that an
// explicit granularity="workspace" drops both per-file and
// workspace-level caches.
func TestInvalidateWorkspaceGranularityClearsAll(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)

	srv.recipeCatalog = &catalog.Catalog{}
	srv.parseCache.Put("/file.yaml", &yaml.Node{}, []byte(""))
	srv.diagnostics["/file.yaml"] = []Diagnostic{
		{Line: 1, Column: 1, Severity: "error", Message: "stale"},
	}

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{
		File:        "/file.yaml",
		Granularity: "workspace",
	}))
	if resp := runOneRequest(t, srv, &in, &out); resp.Error != nil {
		t.Fatalf("invalidate returned error: %+v", resp.Error)
	}

	if _, ok := srv.parseCache.Get("/file.yaml"); ok {
		t.Errorf("parseCache entry should be dropped")
	}
	if _, ok := srv.diagnostics["/file.yaml"]; ok {
		t.Errorf("diagnostics entry should be dropped")
	}
	if srv.recipeCatalog != nil {
		t.Errorf("workspace recipeCatalog should be nil under granularity=workspace; got %v", srv.recipeCatalog)
	}
}

// TestInvalidateDefaultGranularityClearsAll verifies the backward-
// compat default: a missing Granularity field behaves like
// "workspace". Existing callers (file-watcher) that issue
// aggrega/invalidate without setting Granularity continue to
// receive workspace-wide invalidation.
func TestInvalidateDefaultGranularityClearsAll(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)

	srv.recipeCatalog = &catalog.Catalog{}

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{File: "/file.yaml"}))
	if resp := runOneRequest(t, srv, &in, &out); resp.Error != nil {
		t.Fatalf("invalidate returned error: %+v", resp.Error)
	}

	if srv.recipeCatalog != nil {
		t.Errorf("workspace recipeCatalog should be nil under empty granularity (backward-compat default); got %v", srv.recipeCatalog)
	}
}

// TestInvalidateUnknownGranularityFallsThroughToWorkspace covers
// the forward-compatibility hatch: any unrecognized granularity
// (including the reserved "entity" value and any free-form string)
// behaves like "workspace".
func TestInvalidateUnknownGranularityFallsThroughToWorkspace(t *testing.T) {
	t.Parallel()
	cases := []string{"entity", "gibberish", "WORKSPACE"} // "WORKSPACE" exercises the case-sensitivity contract — only the lowercase canonical "file" carves out.
	for _, granularity := range cases {
		granularity := granularity
		t.Run(granularity, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			srv := newTestServer(t, tmp, nil, nil)

			srv.recipeCatalog = &catalog.Catalog{}

			var in, out bytes.Buffer
			in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{
				File:        "/file.yaml",
				Granularity: granularity,
			}))
			if resp := runOneRequest(t, srv, &in, &out); resp.Error != nil {
				t.Fatalf("invalidate returned error: %+v", resp.Error)
			}
			if srv.recipeCatalog != nil {
				t.Errorf("workspace recipeCatalog should be nil under granularity=%q (forward-compat hatch); got %v", granularity, srv.recipeCatalog)
			}
		})
	}
}
