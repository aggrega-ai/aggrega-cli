// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// perReleaseLintWorkspace stands a workspace keeping two variants of one
// Component side by side, each read by its own Release, a third Release sharing
// the second variant, and a fourth whose entry switches its catalog off. Only the
// second variant breaks a convention. It returns the root and the offending
// file's workspace-relative path.
func perReleaseLintWorkspace(t *testing.T) (root, offending string) {
	t.Helper()
	root = t.TempDir()
	writeFixtureFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), `workspace:
  releases:
    release-a.yaml:
      catalog_dirs:
        - ./variant-a
    release-b.yaml:
      catalog_dirs:
        - ./variant-b
    release-b2.yaml:
      catalog_dirs:
        - ./variant-b
    release-off.yaml:
      catalog_dirs: []
`)
	for _, r := range []string{"release-a.yaml", "release-b.yaml", "release-b2.yaml", "release-off.yaml"} {
		writeFixtureFile(t, filepath.Join(root, r),
			"apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: probe/"+strings.TrimSuffix(r, ".yaml")+"\n  version: 1.0.0\nspec: {}\n")
	}
	writeFixtureFile(t, filepath.Join(root, "variant-a", "component", "svc.yaml"), conventionsCleanComponent)
	offending = filepath.Join("variant-b", "component", "svc.yaml")
	writeFixtureFile(t, filepath.Join(root, offending), conventionsBadComponent)
	return root, offending
}

// rawLintAnswer drives one aggrega/lint request through the production dispatch
// and returns the result exactly as it went on the wire, so a key's absence is
// read off the bytes rather than off a decoded zero value.
func rawLintAnswer(t *testing.T, srv *Server, params lintParams) json.RawMessage {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/lint", params))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("aggrega/lint refused the request: [%d] %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

// TestLintRequestOverTheWholeCatalogRunsOncePerRelease pins the daemon's
// whole-catalog answer in a workspace whose Releases read catalog directories of
// their own: one pass for each set of directories, each saying which Releases it
// was run for and what it read, with the finding inside the pass whose variant
// carries it and nowhere else; the Releases sharing a variant on one pass; the
// pass reading nothing answered with an empty list rather than failing the
// others; and today's keys covering every pass together, so a client reading
// only those still reads the whole run.
func TestLintRequestOverTheWholeCatalogRunsOncePerRelease(t *testing.T) {
	t.Parallel()
	root, offending := perReleaseLintWorkspace(t)
	srv := conventionsServer(t, root, false)

	res := lintOverTheWire(t, srv, lintParams{})

	if res.Halted {
		t.Error("the run reports a halting verdict; the whole-catalog check is advisory")
	}
	if res.Checked != 2 {
		t.Errorf("Checked = %d; the two variants hold one entity each", res.Checked)
	}
	if len(res.Files) != 1 || res.Files[0].File != offending {
		t.Errorf("the run's files are %+v; want the one offending document %q", res.Files, offending)
	}
	if len(res.Passes) != 3 {
		t.Fatalf("the answer carries %d pass(es); want 3 — one per set of directories: %+v", len(res.Passes), res.Passes)
	}
	type wantPass struct {
		releases, dirs string
		checked        int
		file           string
	}
	want := []wantPass{
		{"release-a.yaml", filepath.Join("variant-a"), 1, ""},
		{"release-b.yaml,release-b2.yaml", filepath.Join("variant-b"), 1, offending},
		{"release-off.yaml", "", 0, ""},
	}
	for i, w := range want {
		got := res.Passes[i]
		if strings.Join(got.Releases, ",") != w.releases {
			t.Errorf("pass %d was run for %v; want %s", i, got.Releases, w.releases)
		}
		if strings.Join(got.CatalogDirs, ",") != w.dirs {
			t.Errorf("pass %d read %v; want %q, relative to the workspace root", i, got.CatalogDirs, w.dirs)
		}
		if got.Checked != w.checked {
			t.Errorf("pass %d checked %d; want %d", i, got.Checked, w.checked)
		}
		switch {
		case w.file == "" && len(got.Files) != 0:
			t.Errorf("pass %d carries findings %+v; its directories hold nothing that breaks a convention", i, got.Files)
		case w.file != "" && (len(got.Files) != 1 || got.Files[0].File != w.file || got.Findings == 0):
			t.Errorf("pass %d carries %+v (%d findings); want the findings on %q", i, got.Files, got.Findings, w.file)
		}
	}

	// Decoding JSON null leaves a nil slice and [] a non-nil empty one, so this
	// reads whether the pass reading nothing said so with a list.
	if res.Passes[2].CatalogDirs == nil || res.Passes[2].Releases == nil {
		t.Errorf("the pass reading nothing carries a null list: %+v", res.Passes[2])
	}
}

// TestLintRequestOverASingleCatalogCarriesNoPasses is the other shape of
// workspace: one that names no Release under `workspace.releases` is checked once
// over its own directories, and its answer carries no passes key at all, so a
// client reading it gets exactly the answer it always got.
func TestLintRequestOverASingleCatalogCarriesNoPasses(t *testing.T) {
	t.Parallel()
	root, _ := conventionsWorkspace(t, conventionsBadComponent)
	writeFixtureFile(t, filepath.Join(root, "release.yaml"),
		"apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: probe/one\n  version: 1.0.0\nspec: {}\n")
	srv := conventionsServer(t, root, false)

	raw := rawLintAnswer(t, srv, lintParams{})
	if strings.Contains(string(raw), `"passes"`) {
		t.Errorf("a workspace naming no Release under workspace.releases was answered with passes: %s", raw)
	}
	var res lintResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Checked != 1 || len(res.Files) != 1 {
		t.Errorf("the single-catalog run checked %d and grouped %d file(s); want 1 and 1 (positive control): %s",
			res.Checked, len(res.Files), raw)
	}
}
